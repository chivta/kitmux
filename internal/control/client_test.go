package control

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

const testTimeout = 2 * time.Second

// recorder collects everything a Client reports, in the order it arrived.
type recorder struct {
	mu     sync.Mutex
	events []string
}

// add appends one event.
func (r *recorder) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf(format, args...))
}

// Output records pane output.
func (r *recorder) Output(pane string, data []byte) {
	r.add("output %s %q", pane, data)
}

// Notification records a notification.
func (r *recorder) Notification(name, args string) {
	r.add("notify %s %q", name, args)
}

// reply returns a Done callback that records the replies under a label.
func (r *recorder) reply(label string) Done {
	return func(replies []Reply, err error) {
		parts := make([]string, 0, len(replies))
		for _, rep := range replies {
			status := "ok"
			if rep.Failed {
				status = "failed"
			}
			parts = append(parts, fmt.Sprintf("%s:%q", status, rep.Text()))
		}
		r.add("reply %s [%s] err=%v", label, strings.Join(parts, " "), err)
	}
}

// snapshot returns a copy of the recorded events.
func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// discardCloser is a command sink that accepts and drops everything.
type discardCloser struct{}

// Write accepts the bytes.
func (discardCloser) Write(p []byte) (int, error) { return len(p), nil }

// Close does nothing.
func (discardCloser) Close() error { return nil }

// runScript feeds a canned tmux output stream to a client after the given
// commands were queued, and returns the recorded events.
func runScript(t *testing.T, queue func(c *Client, r *recorder), stream string) []string {
	t.Helper()
	rec := &recorder{}
	pr, pw := io.Pipe()
	client := NewClient(pr, discardCloser{}, rec)
	queue(client, rec)
	go func() {
		_, _ = io.WriteString(pw, stream)
		_ = pw.Close()
	}()
	select {
	case <-client.Finished():
	case <-time.After(testTimeout):
		t.Fatal("client did not finish")
	}
	return rec.snapshot()
}

// expectEvents fails the test when got differs from want.
func expectEvents(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestClientMatchesRepliesInOrder(t *testing.T) {
	stream := strings.Join([]string{
		"%begin 100 1 0", // the attach command's own block: not ours
		"%end 100 1 0",
		"%session-changed $0 main",
		`%output %0 before\015\012`,
		"%begin 100 2 1",
		"@0 0 main",
		"@1 1 logs",
		"%end 100 2 1",
		`%output %0 after`,
		"%begin 100 3 1",
		"%end 100 3 1",
		"",
	}, "\n")
	got := runScript(t, func(c *Client, r *recorder) {
		c.Send("list-windows", r.reply("list"))
		c.Send("refresh-client -C @0:80x24", r.reply("resize"))
	}, stream)
	expectEvents(t, got, []string{
		`notify session-changed "$0 main"`,
		`output %0 "before\r\n"`,
		`reply list [ok:"@0 0 main\n@1 1 logs"] err=<nil>`,
		`output %0 "after"`,
		`reply resize [ok:""] err=<nil>`,
	})
}

func TestClientBatchCollectsOneBlockPerCommand(t *testing.T) {
	stream := strings.Join([]string{
		"%begin 100 2 1", "state", "%end 100 2 1",
		"%begin 100 3 1", "line1", "line2", "%end 100 3 1",
		"%begin 100 4 1", "%end 100 4 1",
		"%begin 100 5 1", "next", "%end 100 5 1",
		"",
	}, "\n")
	got := runScript(t, func(c *Client, r *recorder) {
		c.Batch([]string{"display-message -p x", "capture-pane -p", "capture-pane -p -a -q"}, r.reply("paint"))
		c.Send("display-message -p next", r.reply("next"))
	}, stream)
	expectEvents(t, got, []string{
		`reply paint [ok:"state" ok:"line1\nline2" ok:""] err=<nil>`,
		`reply next [ok:"next"] err=<nil>`,
	})
}

func TestClientBatchStopsAtFailure(t *testing.T) {
	// tmux skips the commands after one that fails, so the batch gets fewer
	// blocks than it has commands.
	stream := strings.Join([]string{
		"%begin 100 2 1", "a", "%end 100 2 1",
		"%begin 100 3 1", "can't find pane: %99", "%error 100 3 1",
		"%begin 100 4 1", "next", "%end 100 4 1",
		"",
	}, "\n")
	got := runScript(t, func(c *Client, r *recorder) {
		c.Batch([]string{"display-message -p a", "kill-pane -t %99", "display-message -p c"}, r.reply("batch"))
		c.Send("display-message -p next", r.reply("next"))
	}, stream)
	expectEvents(t, got, []string{
		`reply batch [ok:"a" failed:"can't find pane: %99"] err=<nil>`,
		`reply next [ok:"next"] err=<nil>`,
	})
}

func TestClientBatchParseErrorIsOneBlock(t *testing.T) {
	stream := strings.Join([]string{
		"%begin 100 2 1", "parse error: unknown command: bogus", "%error 100 2 1",
		"%begin 100 3 1", "next", "%end 100 3 1",
		"",
	}, "\n")
	got := runScript(t, func(c *Client, r *recorder) {
		c.Batch([]string{"display-message -p a", "bogus", "display-message -p c"}, r.reply("batch"))
		c.Send("display-message -p next", r.reply("next"))
	}, stream)
	expectEvents(t, got, []string{
		`reply batch [failed:"parse error: unknown command: bogus"] err=<nil>`,
		`reply next [ok:"next"] err=<nil>`,
	})
}

func TestClientBlockBodyMayLookLikeProtocol(t *testing.T) {
	// Captured pane content can contain anything, including lines that look
	// like notifications or like the guard of another block.
	stream := strings.Join([]string{
		"%begin 100 2 1",
		"%output %0 not real output",
		"%end 100 9 1",
		"%exit",
		"%end 100 2 1",
		"",
	}, "\n")
	got := runScript(t, func(c *Client, r *recorder) {
		c.Send("capture-pane -p", r.reply("capture"))
	}, stream)
	expectEvents(t, got, []string{
		`reply capture [ok:"%output %0 not real output\n%end 100 9 1\n%exit"] err=<nil>`,
	})
}

func TestClientFailsPendingAtEOF(t *testing.T) {
	stream := strings.Join([]string{
		"%begin 100 2 1", "partial", "%end 100 2 1",
		"%exit server exited",
		"",
	}, "\n")
	rec := &recorder{}
	pr, pw := io.Pipe()
	client := NewClient(pr, discardCloser{}, rec)
	client.Batch([]string{"a", "b"}, rec.reply("batch"))
	client.Send("c", rec.reply("single"))
	go func() {
		_, _ = io.WriteString(pw, stream)
		_ = pw.Close()
	}()
	<-client.Finished()
	expectEvents(t, rec.snapshot(), []string{
		`notify exit "server exited"`,
		`reply batch [ok:"partial"] err=control connection closed`,
		`reply single [] err=control connection closed`,
	})
	sawExit, reason := client.Exit()
	if !sawExit || reason != "server exited" {
		t.Fatalf("Exit() = %v, %q", sawExit, reason)
	}

	var late error
	client.Send("after close", func(_ []Reply, err error) { late = err })
	if !errors.Is(late, ErrClosed) {
		t.Fatalf("send after close: err = %v, want ErrClosed", late)
	}
}

func TestClientCutStreamHasNoExit(t *testing.T) {
	got := runScript(t, func(*Client, *recorder) {}, "%session-changed $0 main\n%begin 100 2 1\nhalf")
	expectEvents(t, got, []string{`notify session-changed "$0 main"`})
}

func TestClientRejectsInvalidCommands(t *testing.T) {
	rec := &recorder{}
	pr, pw := io.Pipe()
	client := NewClient(pr, discardCloser{}, rec)
	for _, cmd := range []string{"", "   ", "two\nlines", "carriage\rreturn"} {
		var got error
		client.Send(cmd, func(_ []Reply, err error) { got = err })
		if !errors.Is(got, ErrInvalidCommand) {
			t.Errorf("Send(%q): err = %v, want ErrInvalidCommand", cmd, got)
		}
	}
	var got error
	client.Batch(nil, func(_ []Reply, err error) { got = err })
	if !errors.Is(got, ErrInvalidCommand) {
		t.Errorf("empty batch: err = %v, want ErrInvalidCommand", got)
	}
	_ = pw.Close()
	<-client.Finished()
}

func TestClientWritesBatchAsOneLine(t *testing.T) {
	pr, pw := io.Pipe()
	cmdR, cmdW := io.Pipe()
	client := NewClient(pr, cmdW, &recorder{})
	go client.Batch([]string{"display-message -p a", "capture-pane -p"}, nil)
	buf := make([]byte, 128)
	n, err := cmdR.Read(buf)
	if err != nil {
		t.Fatalf("read command: %v", err)
	}
	want := "display-message -p a ; capture-pane -p\n"
	if string(buf[:n]) != want {
		t.Fatalf("wrote %q, want %q", buf[:n], want)
	}
	_ = pw.Close()
	<-client.Finished()
}
