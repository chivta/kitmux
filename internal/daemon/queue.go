// Package daemon is the kitmux bridge process. It keeps one tmux control-mode
// connection per target, mirrors each tmux window as a kitty tab and each
// pane as a kitty window, and moves pane output and keyboard input between
// the two.
//
// All state that describes tmux and kitty is owned by one goroutine, the main
// loop, which runs functions posted to its queue. Other goroutines (control
// connection readers, pane connections, timers) never touch that state; they
// post. Pane output is the exception: it goes straight from a control reader
// to the pane's connection through the hub, without waiting for the main
// loop.
package daemon

import "sync"

// queue is the main loop's inbox: an unbounded list of functions to run in
// order. Posting never blocks, so a goroutine the main loop is waiting on can
// always post.
type queue struct {
	mu    sync.Mutex
	items []func()
	// wake holds a token while items may be non-empty.
	wake chan struct{}
}

// newQueue returns an empty queue.
func newQueue() *queue {
	return &queue{wake: make(chan struct{}, 1)}
}

// post appends fn and wakes the main loop.
func (q *queue) post(fn func()) {
	q.mu.Lock()
	q.items = append(q.items, fn)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// take removes and returns everything posted so far.
func (q *queue) take() []func() {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}
