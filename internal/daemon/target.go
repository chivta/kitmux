package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"kitmux/internal/control"
	"kitmux/internal/layout"
	"kitmux/internal/tabs"
	"kitmux/internal/tmux"
	"kitmux/internal/vt"
)

const (
	// listFormat prints one tmux window per line; the name comes last
	// because it may contain spaces.
	listFormat     = "#{window_id} #{window_index} #{window_layout} #{window_name}"
	listFieldCount = 4

	paneIDPrefix   = "%"
	windowIDPrefix = "@"

	// keysPerCommand is how many input bytes go into one send-keys command.
	keysPerCommand = 512

	prepareTimeout    = 20 * time.Second
	reconnectMinDelay = time.Second
	reconnectMaxDelay = 15 * time.Second
	paintRetryDelay   = 500 * time.Millisecond

	notificationExit = "exit"
)

// targetStatus is where a target is in its connection life cycle.
type targetStatus int

const (
	// statusLive: the control connection is up.
	statusLive targetStatus = iota
	// statusReconnecting: the connection ended and kitmux is trying to get
	// it back. The window list is kept, so tabs stay.
	statusReconnecting
	// statusGone: the session ended. The target has no tabs any more.
	statusGone
)

// windowKey identifies a tmux window across targets.
type windowKey struct {
	target int
	// id is the tmux window id, e.g. "@3".
	id string
}

// window is one tmux window as last listed.
type window struct {
	id    string
	index int
	name  string
	// layout is tmux's layout string, with checksum.
	layout string
	tree   *layout.Node
}

// paneSize is a pane's size in cells.
type paneSize struct {
	cols, rows int
}

// syncState tracks what kitmux last told tmux about one window's layout, so
// the two sides converge instead of chasing each other.
type syncState struct {
	// busy is true while a layout command is waiting for its reply.
	busy bool
	// applied is the layout last sent that tmux's window list has not shown
	// yet. If tmux keeps reporting something else, it rejected or adjusted
	// the layout, and sending it again would loop.
	applied string
	// size is the window size last set for this control client.
	size string
}

// target is one tmux server and its control connection. Everything except
// conn is owned by the main loop.
type target struct {
	d    *Daemon
	spec tmux.Target

	status targetStatus
	// conn is the live control connection, nil while disconnected. It is
	// read by pane connection goroutines to forward input.
	conn atomic.Pointer[tmux.Conn]
	// generation counts attaches. Callbacks from an older connection carry
	// an older number and are ignored.
	generation int

	// windows is the last window list, sorted by index.
	windows []*window
	// listed is true once the current connection delivered a window list.
	listed bool
	// listBusy and listDirty collapse bursts of notifications into one
	// list command at a time.
	listBusy  bool
	listDirty bool
	// resync makes the next window list repaint every pane; set after a
	// reconnect.
	resync bool

	paneSizes map[int]paneSize
	sync      map[string]*syncState

	// continuumSave is tmux-continuum's save script on the target, empty
	// when it is not installed.
	continuumSave string
	// home is the target user's home directory, where new tabs start.
	// Empty when unknown; new tabs then start in the session's directory.
	home string
}

// connHandler receives one control connection's output and notifications on
// that connection's reader goroutine.
type connHandler struct {
	t          *target
	generation int
}

// Output hands pane output to the hub.
func (h connHandler) Output(pane string, data []byte) {
	number, ok := parsePaneID(pane)
	if !ok {
		return
	}
	h.t.d.hub.output(paneKey{target: h.t.spec.Index, pane: number}, data)
}

// Notification forwards a notification to the main loop.
func (h connHandler) Notification(name, _ string) {
	h.t.d.events.post(func() { h.t.onNotification(h.generation, name) })
}

// attach opens the control connection and asks for the window list.
func (t *target) attach(prepared tmux.Prepared) error {
	t.generation++
	generation := t.generation
	conn, err := t.spec.Attach(connHandler{t: t, generation: generation})
	if err != nil {
		return err
	}
	t.conn.Store(conn)
	t.status = statusLive
	t.continuumSave = prepared.ContinuumSave
	t.home = prepared.Home
	t.listed = false
	t.listBusy = false
	t.listDirty = false
	// Per-window sizes belong to the control client and are gone with it.
	t.sync = map[string]*syncState{}
	go func() {
		<-conn.Finished()
		sawExit, reason := conn.Exit()
		t.d.events.post(func() { t.onDisconnect(generation, conn, sawExit, reason) })
	}()
	t.requestList()
	return nil
}

// onNotification reacts to any tmux notification by listing the windows
// again. The notifications themselves are not trusted: tmux reports some
// changes misleadingly and others not at all.
func (t *target) onNotification(generation int, name string) {
	if generation != t.generation || name == notificationExit {
		return
	}
	t.requestList()
}

// requestList asks tmux for the session's windows. Requests made while one is
// running collapse into a single follow-up.
func (t *target) requestList() {
	conn := t.conn.Load()
	if conn == nil {
		return
	}
	if t.listBusy {
		t.listDirty = true
		return
	}
	t.listBusy = true
	generation := t.generation
	cmd := "list-windows -t " + control.Quote("="+t.spec.Session) + " -F " + control.Quote(listFormat)
	conn.Send(cmd, func(replies []control.Reply, err error) {
		t.d.events.post(func() { t.onList(generation, replies, err) })
	})
}

// onList takes a window list reply.
func (t *target) onList(generation int, replies []control.Reply, err error) {
	if generation != t.generation {
		return
	}
	t.listBusy = false
	if t.listDirty {
		t.listDirty = false
		t.requestList()
	}
	if err != nil {
		// The connection closed; onDisconnect takes it from here.
		return
	}
	if len(replies) != 1 || replies[0].Failed {
		// Usually the session just ended and %exit is about to arrive.
		log.Warn().Str("target", t.spec.Name).Msg("window list failed")
		return
	}
	t.applyWindows(parseWindows(replies[0].Lines))
}

// applyWindows replaces the window list and reacts to what changed for panes:
// proxies of panes that are gone are told to exit, and panes that are new,
// changed size, or need a resync are repainted.
func (t *target) applyWindows(windows []*window) {
	sizes := map[int]paneSize{}
	for _, w := range windows {
		for _, pane := range w.tree.Panes() {
			leaf := w.tree.Leaf(pane)
			sizes[pane] = paneSize{cols: leaf.W, rows: leaf.H}
		}
	}
	for pane := range t.paneSizes {
		_, still := sizes[pane]
		if !still {
			t.d.hub.exit(paneKey{target: t.spec.Index, pane: pane})
		}
	}
	for pane, size := range sizes {
		old, known := t.paneSizes[pane]
		if known && old == size && !t.resync {
			continue
		}
		key := paneKey{target: t.spec.Index, pane: pane}
		_, _, painted, connected := t.d.hub.info(key)
		if connected {
			t.paint(pane, !painted)
		}
	}
	t.resync = false

	seen := map[string]bool{}
	for _, w := range windows {
		seen[w.id] = true
		state := t.sync[w.id]
		if state != nil && state.applied == w.layout {
			state.applied = ""
		}
	}
	for id := range t.sync {
		if !seen[id] {
			delete(t.sync, id)
		}
	}

	t.windows = windows
	t.paneSizes = sizes
	t.listed = true
	t.d.scheduleReconcile()
}

// hasPane reports whether the pane is in the last window list.
func (t *target) hasPane(pane int) bool {
	_, ok := t.paneSizes[pane]
	return ok
}

// windowOf returns the window that holds the pane, or nil.
func (t *target) windowOf(pane int) *window {
	for _, w := range t.windows {
		if w.tree.Leaf(pane) != nil {
			return w
		}
	}
	return nil
}

// windowByID returns the window with the given tmux id, or nil.
func (t *target) windowByID(id string) *window {
	for _, w := range t.windows {
		if w.id == id {
			return w
		}
	}
	return nil
}

// syncOf returns the layout sync state of a window, creating it on first use.
func (t *target) syncOf(id string) *syncState {
	state := t.sync[id]
	if state == nil {
		state = &syncState{}
		t.sync[id] = state
	}
	return state
}

// onDisconnect handles the end of a control connection. tmux announces a
// detach of its own with %exit; a stream that just stops was cut.
func (t *target) onDisconnect(generation int, conn *tmux.Conn, sawExit bool, reason string) {
	if generation != t.generation || t.d.stopping {
		return
	}
	log.Warn().Str("target", t.spec.Name).Bool("exit", sawExit).Str("reason", reason).
		Str("stderr", conn.Stderr()).Msg("control connection ended")
	t.conn.Store(nil)
	t.status = statusReconnecting
	// Whatever reaches the panes from now on is stale; the reconnect ends
	// with a full repaint.
	t.d.hub.unpaint(t.spec.Index)
	mode := tmux.ModeLost
	if sawExit {
		mode = tmux.ModeExit
	} else {
		t.notice()
	}
	go t.reconnect(generation, mode)
}

// reconnect prepares the target again, retrying with backoff while it cannot
// be reached, and hands the outcome to the main loop. It runs on its own
// goroutine.
func (t *target) reconnect(generation int, mode tmux.Mode) {
	delay := reconnectMinDelay
	for {
		ctx, cancel := context.WithTimeout(t.d.ctx, prepareTimeout)
		prepared, err := t.spec.Prepare(ctx, mode)
		cancel()
		if t.d.ctx.Err() != nil {
			return
		}
		if err == nil {
			t.d.events.post(func() { t.onPrepared(generation, prepared) })
			return
		}
		unreachable := errors.Is(err, tmux.ErrUnreachable) || errors.Is(err, context.DeadlineExceeded)
		if !unreachable {
			t.d.events.post(func() { t.markGone(generation, err.Error()) })
			return
		}
		log.Info().Str("target", t.spec.Name).Err(err).Dur("retry_in", delay).Msg("target unreachable")
		t.d.events.post(func() {
			if generation == t.generation {
				t.notice()
			}
		})
		select {
		case <-time.After(delay):
		case <-t.d.ctx.Done():
			return
		}
		delay = min(delay*2, reconnectMaxDelay)
	}
}

// onPrepared continues a reconnect once the target answered.
func (t *target) onPrepared(generation int, prepared tmux.Prepared) {
	if generation != t.generation || t.d.stopping {
		return
	}
	if prepared.State == tmux.StateGone {
		t.markGone(generation, "session ended")
		return
	}
	log.Info().Str("target", t.spec.Name).Str("state", string(prepared.State)).Msg("reconnected")
	t.resync = true
	err := t.attach(prepared)
	if err != nil {
		log.Error().Err(err).Str("target", t.spec.Name).Msg("attach after reconnect failed")
		t.status = statusReconnecting
		go t.reconnect(t.generation, tmux.ModeLost)
	}
}

// markGone drops the target: its session ended. The pane proxies exit, which
// closes their kitty windows and tabs.
func (t *target) markGone(generation int, reason string) {
	if generation != t.generation || t.d.stopping {
		return
	}
	log.Info().Str("target", t.spec.Name).Str("reason", reason).Msg("session gone")
	t.status = statusGone
	t.windows = nil
	t.paneSizes = nil
	t.listed = true
	for _, key := range t.d.hub.keys(t.spec.Index) {
		t.d.hub.exit(key)
	}
	t.d.scheduleReconcile()
}

// notice prints a one-line reconnect notice at the bottom of every pane of
// the target, leaving the cursor where it was. The repaint after the
// reconnect removes it.
func (t *target) notice() {
	text := fmt.Sprintf("\x1b7\x1b[999;1H\x1b[0;7m kitmux: reconnecting to %s \x1b[0m\x1b[K\x1b8", tabs.Host(t.spec.Name))
	for _, key := range t.d.hub.keys(t.spec.Index) {
		t.d.hub.write(key, []byte(text))
	}
}

// sendKeys forwards bytes typed into a pane's kitty window. The bytes go to
// the pane exactly as kitty produced them (send-keys -H), so escape sequences,
// UTF-8 and mouse reports arrive unchanged. It is called from pane connection
// goroutines.
func (t *target) sendKeys(pane int, data []byte) {
	conn := t.conn.Load()
	if conn == nil {
		return
	}
	for len(data) > 0 {
		n := min(len(data), keysPerCommand)
		var b strings.Builder
		b.WriteString("send-keys -H -t ")
		b.WriteString(paneID(pane))
		for _, c := range data[:n] {
			b.WriteByte(' ')
			b.WriteString(strconv.FormatUint(uint64(c), 16))
		}
		conn.Send(b.String(), nil)
		data = data[n:]
	}
}

// paint repaints a pane's kitty window from tmux's state. A full paint
// rebuilds everything including scrollback and starts the pane's live output;
// a partial one redraws the visible screen. The state and the captures are
// one command line, so no pane output slips in between them, and the reply's
// position in the stream separates output that the paint already contains
// from output that follows it.
func (t *target) paint(pane int, full bool) {
	conn := t.conn.Load()
	if conn == nil {
		return
	}
	key := paneKey{target: t.spec.Index, pane: pane}
	if full && !t.d.hub.beginPaint(key) {
		return
	}
	id := paneID(pane)
	cmds := []string{"display-message -p -t " + id + " " + control.Quote(vt.StateFormat)}
	if full {
		cmds = append(cmds,
			"capture-pane -p -e -J -S - -E -1 -t "+id, // scrollback as logical lines
			"capture-pane -p -e -N -a -q -t "+id,      // the normal screen under an alternate one
		)
	}
	cmds = append(cmds, "capture-pane -p -e -N -t "+id) // the visible screen, row by row
	generation := t.generation
	conn.Batch(cmds, func(replies []control.Reply, err error) {
		data, buildErr := buildPaint(full, len(cmds), replies, err)
		if buildErr == nil {
			t.d.hub.paint(key, data, full)
			return
		}
		log.Warn().Err(buildErr).Str("target", t.spec.Name).Int("pane", pane).Msg("paint failed")
		if !full {
			return
		}
		t.d.hub.abortPaint(key)
		t.d.after(paintRetryDelay, func() {
			if generation == t.generation && t.hasPane(pane) {
				t.paint(pane, true)
			}
		})
	})
}

// buildPaint turns the replies of a paint batch into terminal bytes.
func buildPaint(full bool, want int, replies []control.Reply, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if len(replies) != want || replies[len(replies)-1].Failed {
		detail := ""
		if len(replies) > 0 {
			detail = replies[len(replies)-1].Text()
		}
		return nil, fmt.Errorf("capture: %s", detail)
	}
	state, err := vt.ParseState(replies[0].Text())
	if err != nil {
		return nil, err
	}
	visible := replies[len(replies)-1].Lines
	if !full {
		return vt.VisiblePaint(state, visible), nil
	}
	return vt.FullPaint(state, replies[1].Lines, replies[2].Lines, visible), nil
}

// saveTick asks tmux-continuum to save. continuum only saves on its own while
// a client draws a status line, and kitmux's control client draws none;
// continuum still throttles the saves to its configured interval.
func (t *target) saveTick() {
	conn := t.conn.Load()
	if conn == nil || t.continuumSave == "" {
		return
	}
	conn.Send("run-shell -b "+control.Quote(shellQuote(t.continuumSave)), nil)
}

// shutdown detaches from tmux.
func (t *target) shutdown() {
	conn := t.conn.Swap(nil)
	if conn != nil {
		conn.Shutdown()
	}
}

// parseWindows reads the lines of a list-windows reply and sorts the windows
// by index. Lines that do not parse are skipped.
func parseWindows(lines [][]byte) []*window {
	var windows []*window
	for _, raw := range lines {
		fields := strings.SplitN(string(raw), " ", listFieldCount)
		if len(fields) < listFieldCount-1 || !strings.HasPrefix(fields[0], windowIDPrefix) {
			continue
		}
		index, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		tree, err := layout.Parse(fields[2])
		if err != nil {
			log.Warn().Err(err).Str("layout", fields[2]).Msg("unreadable window layout")
			continue
		}
		w := &window{id: fields[0], index: index, layout: fields[2], tree: tree}
		if len(fields) == listFieldCount {
			w.name = fields[3]
		}
		windows = append(windows, w)
	}
	sort.SliceStable(windows, func(i, j int) bool { return windows[i].index < windows[j].index })
	return windows
}

// paneID formats a pane number as a tmux pane id.
func paneID(pane int) string {
	return paneIDPrefix + strconv.Itoa(pane)
}

// parsePaneID reads a tmux pane id such as "%12".
func parsePaneID(id string) (int, bool) {
	if !strings.HasPrefix(id, paneIDPrefix) {
		return 0, false
	}
	number, err := strconv.Atoi(id[len(paneIDPrefix):])
	if err != nil || number < 0 {
		return 0, false
	}
	return number, true
}

// shellQuote quotes s for the shell that tmux's run-shell uses.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
