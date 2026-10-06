// Package proxy holds the kitmux subcommands that kitty runs: the pane proxy
// that every kitmux window runs, and the key actions behind kitmux's key
// bindings. All of them talk to the daemon over its unix socket.
package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"kitmux/internal/cli"
	"kitmux/internal/ipc"
)

const (
	inputBufferSize = 4096
	// dialTimeout bounds the wait for the daemon's socket.
	dialTimeout = 5 * time.Second
	// actionTimeout bounds a key action's round trip.
	actionTimeout = 10 * time.Second

	keyYes      = 'y'
	keyYesUpper = 'Y'
)

// ErrDaemonGone is returned when the daemon cannot be reached.
var ErrDaemonGone = errors.New("kitmux daemon not reachable")

// RunPane is `kitmux _pane SOCKET TARGET PANE`: it shows one tmux pane in the
// kitty window it runs in. The terminal goes into raw mode; pane output from
// the daemon is written to it as is, and every byte typed is sent back. It
// returns when the daemon says the pane is gone or the connection ends.
func RunPane(socket string, target int, pane string) error {
	conn, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDaemonGone, err)
	}
	defer conn.Close()

	stdin := int(os.Stdin.Fd())
	if term.IsTerminal(stdin) {
		state, rawErr := term.MakeRaw(stdin)
		if rawErr == nil {
			defer term.Restore(stdin, state)
		}
	}
	cols, rows := terminalSize()
	err = ipc.WriteJSON(conn, ipc.Request{Op: ipc.OpPane, Target: target, Pane: pane, Cols: cols, Rows: rows})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDaemonGone, err)
	}

	var writeMu sync.Mutex
	send := func(kind ipc.FrameType, payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return ipc.WriteFrame(conn, kind, payload)
	}
	go forwardInput(send)
	go forwardResizes(send)

	reader := bufio.NewReader(conn)
	for {
		kind, payload, readErr := ipc.ReadFrame(reader)
		if readErr != nil {
			return nil
		}
		switch kind {
		case ipc.FrameOutput:
			_, err = os.Stdout.Write(payload)
			if err != nil {
				return nil
			}
		case ipc.FrameExit:
			return nil
		}
	}
}

// forwardInput sends everything read from the terminal to the daemon.
func forwardInput(send func(ipc.FrameType, []byte) error) {
	buf := make([]byte, inputBufferSize)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			sendErr := send(ipc.FrameInput, append([]byte(nil), buf[:n]...))
			if sendErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// forwardResizes tells the daemon the terminal's size whenever kitty resizes
// the window.
func forwardResizes(send func(ipc.FrameType, []byte) error) {
	changes := make(chan os.Signal, 1)
	signal.Notify(changes, syscall.SIGWINCH)
	for range changes {
		cols, rows := terminalSize()
		err := send(ipc.FrameResize, ipc.EncodeSize(cols, rows))
		if err != nil {
			return
		}
	}
}

// terminalSize returns the size of the terminal on stdout, or 0x0.
func terminalSize() (int, int) {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0, 0
	}
	return cols, rows
}

// RunAction is `kitmux _split` and `kitmux _newtab`: it sends one key action
// for the kitty window it was triggered in. kitty runs these in the
// background, so there is nobody to show an error to; the daemon logs it.
func RunAction(socket string, req ipc.Request) error {
	_, err := call(socket, req)
	return err
}

// RunConfirm is `kitmux _confirm SOCKET SCOPE WINDOW`, run in a kitty overlay
// on the window whose pane (or tab) is to be closed. It asks once and kills
// in tmux only on "y" or "Y"; any other key, Esc, or closing the overlay
// leaves everything as it is.
func RunConfirm(socket string, scope ipc.Scope, window int) error {
	resp, err := call(socket, ipc.Request{Op: ipc.OpDescribe, Window: window, Scope: scope})
	if err != nil {
		showAndWait(cli.DaemonGoneMessage())
		return err
	}
	if !resp.OK {
		showAndWait(cli.ActionMessage(resp.Code))
		return nil
	}
	fmt.Print(cli.KillPrompt(scope, resp.Title))
	key, err := readKey()
	if err != nil || (key != keyYes && key != keyYesUpper) {
		return nil
	}
	resp, err = call(socket, ipc.Request{Op: ipc.OpKill, Window: window, Scope: scope})
	if err != nil {
		showAndWait(cli.DaemonGoneMessage())
		return err
	}
	if !resp.OK {
		showAndWait(cli.ActionMessage(resp.Code))
	}
	return nil
}

// call sends one request and reads its response.
func call(socket string, req ipc.Request) (ipc.Response, error) {
	conn, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		return ipc.Response{}, fmt.Errorf("%w: %v", ErrDaemonGone, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(actionTimeout))
	err = ipc.WriteJSON(conn, req)
	if err != nil {
		return ipc.Response{}, fmt.Errorf("%w: %v", ErrDaemonGone, err)
	}
	var resp ipc.Response
	err = ipc.ReadJSON(bufio.NewReader(conn), &resp)
	if err != nil {
		return ipc.Response{}, fmt.Errorf("%w: %v", ErrDaemonGone, err)
	}
	return resp, nil
}

// readKey reads one key press from the terminal in raw mode and returns its
// first byte.
func readKey() (byte, error) {
	stdin := int(os.Stdin.Fd())
	state, err := term.MakeRaw(stdin)
	if err != nil {
		return 0, err
	}
	defer term.Restore(stdin, state)
	buf := make([]byte, inputBufferSize)
	n, err := os.Stdin.Read(buf)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return buf[0], nil
}

// showAndWait prints a message and waits for a key, so the overlay does not
// vanish before it can be read.
func showAndWait(message string) {
	fmt.Println(message)
	_, _ = readKey()
}
