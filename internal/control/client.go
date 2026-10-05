package control

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
)

var (
	// ErrClosed is reported to a command whose connection ended before its
	// reply arrived.
	ErrClosed = errors.New("control connection closed")
	// ErrInvalidCommand is reported for a command that cannot be sent as one
	// control-mode line.
	ErrInvalidCommand = errors.New("invalid control command")
)

const (
	// commandSeparator joins the commands of a batch into one line. tmux runs
	// them back to back, without handling pane output in between.
	commandSeparator = " ; "
	// readBufferSize is the initial line buffer; longer lines still work.
	readBufferSize = 64 * 1024
)

// Reply is the body of one reply block.
type Reply struct {
	Lines [][]byte
	// Failed is true when tmux closed the block with %error; Lines then
	// holds the error message.
	Failed bool
}

// Text joins the reply lines with newlines.
func (r Reply) Text() string {
	return string(bytes.Join(r.Lines, []byte("\n")))
}

// Handler receives everything that is not a command reply. Its methods run on
// the client's reader goroutine, in stream order, interleaved with reply
// callbacks, and must not block.
type Handler interface {
	// Output delivers decoded pane output.
	Output(pane string, data []byte)
	// Notification delivers any other "%name args" line, including "exit".
	Notification(name, args string)
}

// Done is called exactly once per Send or Batch. It normally runs on the
// reader goroutine, in stream order with Handler calls. When the connection is
// already closed it runs on the caller's goroutine with ErrClosed.
//
// replies holds one entry per command that tmux ran. It is shorter than the
// batch when a command failed: tmux skips the commands after a failure, and
// answers a line it cannot parse with a single failed block.
type Done func(replies []Reply, err error)

// batch is one sent line waiting for its reply blocks.
type batch struct {
	want    int
	replies []Reply
	done    Done
}

// Client is one control-mode connection. It writes command lines and matches
// reply blocks to them in order. Send and Batch are safe for concurrent use.
type Client struct {
	handler Handler

	mu      sync.Mutex
	w       io.WriteCloser
	pending []*batch
	closed  bool

	finished   chan struct{}
	sawExit    bool
	exitReason string
}

// NewClient starts reading control-mode output from r and returns a client
// that sends commands to w. The reader goroutine stops at EOF.
func NewClient(r io.Reader, w io.WriteCloser, handler Handler) *Client {
	c := &Client{handler: handler, w: w, finished: make(chan struct{})}
	go c.readLoop(r)
	return c
}

// Send queues one command.
func (c *Client) Send(cmd string, done Done) {
	c.Batch([]string{cmd}, done)
}

// Batch sends several commands as one line, so tmux runs them atomically with
// respect to pane output, and collects their replies into one callback.
func (c *Client) Batch(cmds []string, done Done) {
	if done == nil {
		done = func([]Reply, error) {}
	}
	line := strings.Join(cmds, commandSeparator)
	if len(cmds) == 0 || strings.TrimSpace(line) == "" || strings.ContainsAny(line, "\r\n") {
		done(nil, ErrInvalidCommand)
		return
	}
	b := &batch{want: len(cmds), done: done}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		done(nil, ErrClosed)
		return
	}
	c.pending = append(c.pending, b)
	_, err := io.WriteString(c.w, line+"\n")
	c.mu.Unlock()
	if err != nil {
		// The reader fails every pending batch when the process goes away;
		// closing the writer makes sure it does.
		_ = c.Close()
	}
}

// Close closes the command stream, which makes the tmux client detach. The
// reader goroutine finishes when the process closes its output.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.w == nil {
		return nil
	}
	err := c.w.Close()
	c.w = nopWriteCloser{}
	return err
}

// Finished is closed once the output stream ended and every pending command
// was failed.
func (c *Client) Finished() <-chan struct{} {
	return c.finished
}

// Exit reports whether tmux announced the detach with %exit before the stream
// ended, and the reason it gave. It is valid after Finished is closed. A
// stream that ends without %exit was cut, e.g. by a dropped SSH connection.
func (c *Client) Exit() (bool, string) {
	return c.sawExit, c.exitReason
}

// readLoop parses the output stream until EOF.
func (c *Client) readLoop(r io.Reader) {
	reader := bufio.NewReaderSize(r, readBufferSize)
	var (
		inBlock bool
		guard   Guard
		body    [][]byte
	)
	for {
		raw, err := reader.ReadBytes('\n')
		if len(raw) > 0 && raw[len(raw)-1] == '\n' {
			raw = raw[:len(raw)-1]
			line := ParseLine(raw)
			if inBlock {
				closes := (line.Kind == KindEnd || line.Kind == KindError) && line.Guard == guard
				if !closes {
					body = append(body, raw)
				} else {
					inBlock = false
					if guard.FromClient() {
						c.completeBlock(Reply{Lines: body, Failed: line.Kind == KindError})
					}
					body = nil
				}
			} else {
				switch line.Kind {
				case KindBegin:
					inBlock = true
					guard = line.Guard
				case KindOutput:
					c.handler.Output(line.Pane, line.Data)
				case KindExit:
					c.sawExit = true
					c.exitReason = line.Args
					c.handler.Notification(line.Name, line.Args)
				case KindNotification:
					c.handler.Notification(line.Name, line.Args)
				}
			}
		}
		if err != nil {
			break
		}
	}
	c.failPending()
	close(c.finished)
}

// completeBlock adds one finished reply block to the oldest pending batch and
// fires its callback when the batch is complete or a command failed.
func (c *Client) completeBlock(reply Reply) {
	c.mu.Lock()
	if len(c.pending) == 0 {
		c.mu.Unlock()
		return
	}
	b := c.pending[0]
	b.replies = append(b.replies, reply)
	finished := reply.Failed || len(b.replies) == b.want
	if finished {
		c.pending = c.pending[1:]
	}
	c.mu.Unlock()
	if finished {
		b.done(b.replies, nil)
	}
}

// failPending marks the client closed and fails every batch still waiting.
func (c *Client) failPending() {
	c.mu.Lock()
	c.closed = true
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, b := range pending {
		b.done(b.replies, ErrClosed)
	}
}

// Quote quotes s as one argument of a tmux command line.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// nopWriteCloser replaces the command stream after Close, so late writes fail
// quietly instead of touching a closed file.
type nopWriteCloser struct{}

// Write rejects the write.
func (nopWriteCloser) Write([]byte) (int, error) { return 0, ErrClosed }

// Close does nothing.
func (nopWriteCloser) Close() error { return nil }
