package daemon

import (
	"net"
	"sync"

	"github.com/rs/zerolog/log"

	"kitmux/internal/ipc"
	"kitmux/internal/vt"
)

const (
	// maxQueuedBytes is how far a pane's kitty window may fall behind its
	// output before kitmux drops the backlog and repaints the pane from
	// tmux's current state instead.
	maxQueuedBytes = 32 << 20
)

// paneKey identifies a tmux pane across targets.
type paneKey struct {
	// target is the target's position on the command line.
	target int
	// pane is the pane number: the tmux pane id without "%".
	pane int
}

// paneConn is the daemon's end of one pane proxy connection. Frames for the
// proxy are queued and written by a goroutine of their own, so a slow kitty
// window never holds up a control-mode reader.
type paneConn struct {
	key  paneKey
	conn net.Conn

	mu     sync.Mutex
	cond   *sync.Cond
	frames []frame
	queued int
	closed bool

	// The fields below are guarded by the hub's lock.

	// painted is false until the pane's first full paint was queued. Until
	// then live output is dropped: the paint will contain it.
	painted bool
	// painting is true while a full paint is on its way.
	painting bool
	filter   vt.Filter
	cols     int
	rows     int
}

// frame is one queued ipc frame.
type frame struct {
	kind    ipc.FrameType
	payload []byte
}

// newPaneConn wraps an accepted proxy connection and starts its writer.
func newPaneConn(key paneKey, conn net.Conn, cols, rows int) *paneConn {
	pc := &paneConn{key: key, conn: conn, cols: cols, rows: rows}
	pc.cond = sync.NewCond(&pc.mu)
	go pc.writeLoop()
	return pc
}

// send queues one frame. Unless force is set, a frame that would grow the
// backlog past maxQueuedBytes makes send drop the whole backlog and report
// false.
func (pc *paneConn) send(kind ipc.FrameType, payload []byte, force bool) bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.closed {
		return true
	}
	if !force && pc.queued+len(payload) > maxQueuedBytes {
		pc.frames = nil
		pc.queued = 0
		return false
	}
	pc.frames = append(pc.frames, frame{kind: kind, payload: payload})
	pc.queued += len(payload)
	pc.cond.Signal()
	return true
}

// close stops the writer after it flushed what is queued, then closes the
// connection.
func (pc *paneConn) close() {
	pc.mu.Lock()
	pc.closed = true
	pc.cond.Signal()
	pc.mu.Unlock()
}

// writeLoop writes queued frames until the connection is closed or fails.
func (pc *paneConn) writeLoop() {
	defer pc.conn.Close()
	for {
		pc.mu.Lock()
		for len(pc.frames) == 0 && !pc.closed {
			pc.cond.Wait()
		}
		if len(pc.frames) == 0 {
			pc.mu.Unlock()
			return
		}
		batch := pc.frames
		pc.frames = nil
		pc.queued = 0
		pc.mu.Unlock()
		for _, f := range batch {
			err := ipc.WriteFrame(pc.conn, f.kind, f.payload)
			if err != nil {
				return
			}
		}
	}
}

// hub routes pane output to pane proxy connections. It is shared between the
// main loop, the control-mode readers and the proxy connection goroutines.
type hub struct {
	mu    sync.Mutex
	conns map[paneKey]*paneConn
	// onOverflow is called (outside the lock) when a pane's backlog was
	// dropped and the pane needs a full repaint.
	onOverflow func(paneKey)
}

// newHub returns an empty hub.
func newHub(onOverflow func(paneKey)) *hub {
	return &hub{conns: map[paneKey]*paneConn{}, onOverflow: onOverflow}
}

// register makes pc the connection of its pane, replacing and closing an
// older one.
func (h *hub) register(pc *paneConn) {
	h.mu.Lock()
	old := h.conns[pc.key]
	h.conns[pc.key] = pc
	h.mu.Unlock()
	if old != nil {
		old.close()
	}
}

// remove forgets pc if it is still its pane's connection, and closes it.
func (h *hub) remove(pc *paneConn) {
	h.mu.Lock()
	if h.conns[pc.key] == pc {
		delete(h.conns, pc.key)
	}
	h.mu.Unlock()
	pc.close()
}

// output forwards live pane output. It is called by control-mode readers.
func (h *hub) output(key paneKey, data []byte) {
	h.mu.Lock()
	pc := h.conns[key]
	if pc == nil || !pc.painted {
		h.mu.Unlock()
		return
	}
	filtered := pc.filter.Write(data)
	ok := len(filtered) == 0 || pc.send(ipc.FrameOutput, filtered, false)
	if !ok {
		pc.painted = false
		pc.painting = false
	}
	h.mu.Unlock()
	if !ok {
		log.Warn().Int("target", key.target).Int("pane", key.pane).Msg("pane fell too far behind, repainting")
		h.onOverflow(key)
	}
}

// beginPaint marks that a full paint was requested for a pane. It reports
// false when the pane has no connection or a full paint is already on its
// way.
func (h *hub) beginPaint(key paneKey) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	pc := h.conns[key]
	if pc == nil || pc.painting {
		return false
	}
	pc.painting = true
	return true
}

// abortPaint clears the mark set by beginPaint after the paint failed.
func (h *hub) abortPaint(key paneKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pc := h.conns[key]
	if pc != nil {
		pc.painting = false
	}
}

// paint delivers repaint bytes. A full paint starts the live output stream of
// a pane; a partial one is dropped unless that stream is already running.
func (h *hub) paint(key paneKey, data []byte, full bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pc := h.conns[key]
	if pc == nil {
		return
	}
	if full {
		pc.filter.Reset()
		pc.painted = true
		pc.painting = false
	} else if !pc.painted {
		return
	}
	pc.send(ipc.FrameOutput, data, true)
}

// write sends raw bytes to a pane's terminal regardless of its paint state,
// for notices.
func (h *hub) write(key paneKey, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pc := h.conns[key]
	if pc != nil {
		pc.send(ipc.FrameOutput, data, true)
	}
}

// exit tells a pane's proxy to quit and forgets the connection. It reports
// whether the pane had one.
func (h *hub) exit(key paneKey) bool {
	h.mu.Lock()
	pc := h.conns[key]
	delete(h.conns, key)
	h.mu.Unlock()
	if pc == nil {
		return false
	}
	pc.send(ipc.FrameExit, nil, true)
	pc.close()
	return true
}

// info returns a pane connection's size and paint state.
func (h *hub) info(key paneKey) (cols, rows int, painted, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pc := h.conns[key]
	if pc == nil {
		return 0, 0, false, false
	}
	return pc.cols, pc.rows, pc.painted, true
}

// resize records the size a proxy reported.
func (h *hub) resize(pc *paneConn, cols, rows int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pc.cols, pc.rows = cols, rows
}

// keys returns the panes of one target that have a connection.
func (h *hub) keys(target int) []paneKey {
	h.mu.Lock()
	defer h.mu.Unlock()
	var keys []paneKey
	for key := range h.conns {
		if key.target == target {
			keys = append(keys, key)
		}
	}
	return keys
}

// unpaint marks every pane of a target as needing a full paint again, and
// stops their live output until it arrives. Used when the target's connection
// was lost.
func (h *hub) unpaint(target int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, pc := range h.conns {
		if key.target == target {
			pc.painted = false
			pc.painting = false
		}
	}
}

// closeAll closes every connection, which makes every proxy exit.
func (h *hub) closeAll() {
	h.mu.Lock()
	conns := h.conns
	h.conns = map[paneKey]*paneConn{}
	h.mu.Unlock()
	for _, pc := range conns {
		pc.close()
	}
}
