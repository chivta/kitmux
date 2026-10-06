package daemon

import (
	"bufio"
	"net"
	"time"

	"github.com/rs/zerolog/log"

	"kitmux/internal/control"
	"kitmux/internal/ipc"
	"kitmux/internal/tabs"
)

const (
	// requestTimeout bounds how long a new connection may take to send its
	// request line.
	requestTimeout = 5 * time.Second
)

// acceptLoop serves connections from the kitmux subcommands kitty runs. It
// ends when the listener is closed.
func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return
		}
		go d.serve(conn)
	}
}

// serve reads the request line and dispatches the connection.
func (d *Daemon) serve(conn net.Conn) {
	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(requestTimeout))
	var req ipc.Request
	err := ipc.ReadJSON(reader, &req)
	if err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if req.Op == ipc.OpPane {
		d.servePane(conn, reader, req)
		return
	}
	reply := make(chan ipc.Response, 1)
	d.events.post(func() { reply <- d.handleAction(req) })
	select {
	case resp := <-reply:
		_ = ipc.WriteJSON(conn, resp)
	case <-d.ctx.Done():
	}
	_ = conn.Close()
}

// servePane runs a pane proxy connection: it registers the proxy for the
// pane's output and forwards what the proxy sends until it disconnects.
func (d *Daemon) servePane(conn net.Conn, reader *bufio.Reader, req ipc.Request) {
	pane, ok := parsePaneID(req.Pane)
	if !ok || req.Target < 0 || req.Target >= len(d.targets) {
		_ = conn.Close()
		return
	}
	key := paneKey{target: req.Target, pane: pane}
	t := d.targets[req.Target]
	pc := newPaneConn(key, conn, req.Cols, req.Rows)
	d.hub.register(pc)
	d.events.post(func() { d.onPaneRegistered(key) })
	for {
		kind, payload, err := ipc.ReadFrame(reader)
		if err != nil {
			break
		}
		switch kind {
		case ipc.FrameInput:
			t.sendKeys(pane, payload)
		case ipc.FrameResize:
			cols, rows, valid := ipc.DecodeSize(payload)
			if valid {
				d.hub.resize(pc, cols, rows)
				d.events.post(d.scheduleReconcile)
			}
		}
	}
	d.hub.remove(pc)
	// The window is gone: closed by the user, or with its tab or OS window.
	// Give kitty a moment before looking at what is left.
	d.events.post(func() { d.scheduleReconcileIn(closeSettleDelay) })
}

// onPaneRegistered starts a new proxy's display. If tmux's pane already has
// the size of the kitty window it is painted at once; otherwise the layout
// sync resizes the pane first and the paint follows the size change, with a
// timer as the fallback when tmux cannot take the size.
func (d *Daemon) onPaneRegistered(key paneKey) {
	t := d.targets[key.target]
	gone := t.status == statusGone || (t.status == statusLive && t.listed && !t.hasPane(key.pane))
	if gone {
		d.hub.exit(key)
		return
	}
	if t.status != statusLive {
		t.notice()
		return
	}
	cols, rows, _, connected := d.hub.info(key)
	if !connected {
		return
	}
	if t.paneSizes[key.pane] == (paneSize{cols: cols, rows: rows}) {
		t.paint(key.pane, true)
		return
	}
	d.scheduleReconcile()
	d.after(paintFallbackDelay, func() {
		_, _, painted, still := d.hub.info(key)
		if still && !painted && t.status == statusLive && t.hasPane(key.pane) {
			t.paint(key.pane, true)
		}
	})
}

// handleAction runs a key action on the main loop and returns its response.
func (d *Daemon) handleAction(req ipc.Request) ipc.Response {
	key, found := d.resolveWindow(req.Window)
	if !found {
		return ipc.Response{Code: ipc.CodeNotManaged}
	}
	t := d.targets[key.target]
	w := t.windowOf(key.pane)
	if w == nil {
		return ipc.Response{Code: ipc.CodeNotManaged}
	}
	if req.Op == ipc.OpDescribe {
		return ipc.Response{OK: true, Title: tabs.Title(t.spec.Name, w.index, w.name)}
	}
	conn := t.conn.Load()
	if conn == nil {
		return ipc.Response{Code: ipc.CodeDisconnected}
	}
	generation := t.generation
	switch req.Op {
	case ipc.OpNewTab:
		// The reply names the new window, so its tab can take the focus
		// when it appears.
		cmd := "new-window -t " + control.Quote("="+t.spec.Session+":") + " -P -F " + control.Quote("#{window_id}")
		conn.Send(cmd, func(replies []control.Reply, err error) {
			id, created := replyID(replies, err)
			d.events.post(func() {
				if created && generation == t.generation {
					d.focusWindows[windowKey{target: key.target, id: id}] = time.Now()
					t.requestList()
				}
			})
		})
	case ipc.OpSplit:
		direction := "-h"
		if req.Stacked {
			direction = "-v"
		}
		cmd := "split-window " + direction + " -t " + paneID(key.pane) + " -P -F " + control.Quote("#{pane_id}")
		conn.Send(cmd, func(replies []control.Reply, err error) {
			id, created := replyID(replies, err)
			pane, valid := parsePaneID(id)
			d.events.post(func() {
				if created && valid && generation == t.generation {
					d.focusPanes[paneKey{target: key.target, pane: pane}] = time.Now()
					t.requestList()
				}
			})
		})
	case ipc.OpKill:
		// Only tmux is told. The kitty window or tab closes when tmux
		// reports the pane or window gone.
		cmd := "kill-pane -t " + paneID(key.pane)
		if req.Scope == ipc.ScopeWindow {
			cmd = "kill-window -t " + w.id
		}
		conn.Send(cmd, func(replies []control.Reply, err error) {
			_, killed := replyID(replies, err)
			if !killed {
				log.Warn().Str("target", t.spec.Name).Str("command", cmd).Msg("kill failed")
			}
		})
	default:
		return ipc.Response{Code: ipc.CodeBadRequest}
	}
	return ipc.Response{OK: true}
}

// resolveWindow finds the pane a kitty window shows, looking at kitty again
// when the window is not known yet.
func (d *Daemon) resolveWindow(id int) (paneKey, bool) {
	key, ok := d.windowPanes[id]
	if ok {
		return key, true
	}
	_, looked := d.look()
	if !looked {
		return paneKey{}, false
	}
	key, ok = d.windowPanes[id]
	return key, ok
}

// replyID returns the first line of a single successful reply, which for
// commands run with -P is the id of what they created.
func replyID(replies []control.Reply, err error) (string, bool) {
	if err != nil || len(replies) != 1 || replies[0].Failed {
		return "", false
	}
	if len(replies[0].Lines) == 0 {
		return "", true
	}
	return string(replies[0].Lines[0]), true
}
