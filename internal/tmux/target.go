// Package tmux reaches the tmux server of one target, the local machine or an
// SSH host: it prepares the session (version check, cold-start restore) and
// opens the control-mode connection.
package tmux

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kitmux/internal/control"
	"kitmux/internal/tabs"
)

//go:embed prepare.sh
var prepareScript string

const (
	tmuxBinary = "tmux"
	shBinary   = "sh"

	// MinMajor and MinMinor are the oldest tmux kitmux supports: 3.2 added
	// per-window sizes for control clients and attach-session -f.
	MinMajor = 3
	MinMinor = 2

	// sshExitTransport is ssh's own exit status for a failed connection.
	sshExitTransport = 255

	sshControlPathName = "kitmux-%C"
	sshDir             = ".ssh"
	sshDirMode         = 0o700

	// closeGrace is how long a control connection gets to end on its own
	// after its command stream was closed.
	closeGrace = 2 * time.Second
	// stderrLimit bounds how much of a failed command's stderr is kept for
	// the error detail.
	stderrLimit = 512

	keyVersion   = "version"
	keyState     = "state"
	keyContinuum = "continuum"
	keyHome      = "home"
	keyError     = "error"
	keyDetail    = "detail"

	scriptErrorNoTmux  = "no-tmux"
	scriptErrorOldTmux = "old-tmux"
)

// Mode tells Prepare what to do when the session is not there.
type Mode string

const (
	// ModeStart is the first connect: the session is created if missing,
	// and a cold tmux server gets its tmux-resurrect save restored.
	ModeStart Mode = "start"
	// ModeLost is a reconnect after the connection was cut: a cold server
	// is restored (the host rebooted), a missing session on a running
	// server means the session ended.
	ModeLost Mode = "lost"
	// ModeExit is a reconnect after tmux detached the client itself:
	// nothing is created.
	ModeExit Mode = "exit"
)

// SessionState is what Prepare found or did.
type SessionState string

const (
	// StateReady means the session was already there.
	StateReady SessionState = "ready"
	// StateCreated means the session was created empty.
	StateCreated SessionState = "created"
	// StateRestored means a cold server was started and tmux-resurrect's
	// restore script ran.
	StateRestored SessionState = "restored"
	// StateGone means the session does not exist and was not created.
	StateGone SessionState = "gone"
)

var (
	// ErrUnreachable means the command could not be run on the target at
	// all, e.g. ssh could not connect.
	ErrUnreachable = errors.New("target unreachable")
	// ErrNoTmux means tmux is not installed on the target.
	ErrNoTmux = errors.New("tmux not found")
	// ErrOldTmux means the target's tmux is older than MinMajor.MinMinor.
	ErrOldTmux = errors.New("tmux too old")
	// ErrSession means the session could not be listed or created.
	ErrSession = errors.New("session unavailable")
)

// Error is one of the sentinel errors above plus a detail for the user: the
// ssh error text, the tmux version, tmux's own message.
type Error struct {
	Kind   error
	Detail string
}

// Error renders the kind and detail.
func (e *Error) Error() string {
	return fmt.Sprintf("%v: %s", e.Kind, e.Detail)
}

// Unwrap returns the sentinel, so errors.Is works on it.
func (e *Error) Unwrap() error {
	return e.Kind
}

// Target is one tmux server kitmux mirrors.
type Target struct {
	// Index is the target's position on the command line.
	Index int
	// Name is "local" or the SSH destination as given.
	Name string
	// Session is the tmux session name. It must already be validated: it
	// is interpolated into remote shell commands.
	Session string
	// Lang is the validated LANG value tmux runs with.
	Lang string
	// SSH is the ssh command and its leading arguments.
	SSH []string
	// Home is the local home directory, where SSH control sockets live.
	Home string
	// Batch makes ssh fail instead of asking for a password. The daemon
	// sets it because it has no terminal to ask on.
	Batch bool
}

// Prepared is the result of preparing a target.
type Prepared struct {
	// Version is the output of `tmux -V`.
	Version string
	State   SessionState
	// ContinuumSave is the path of tmux-continuum's save script on the
	// target, empty when the plugin is not installed.
	ContinuumSave string
	// Home is the home directory of the target's user, empty when the
	// target did not report one. New tabs start there.
	Home string
}

// IsLocal reports whether the target is the machine kitmux runs on.
func (t Target) IsLocal() bool {
	return t.Name == tabs.LocalTarget
}

// Prepare checks the tmux version on the target and makes sure the session is
// there, following mode. It returns an *Error for every failure a user can
// act on.
func (t Target) Prepare(ctx context.Context, mode Mode) (Prepared, error) {
	cmd := t.command(ctx, []string{shBinary, "-s", "--", t.Session, t.Lang, string(mode), strconv.Itoa(MinMajor), strconv.Itoa(MinMinor)})
	cmd.Stdin = strings.NewReader(prepareScript)
	var stdout bytes.Buffer
	stderr := &limitedBuffer{limit: stderrLimit}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	// ErrWaitDelay only says that something kept the output pipes open after
	// the command itself finished fine.
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return Prepared{}, t.runError(err, stderr.String())
	}
	return parsePrepared(stdout.String())
}

// Attach starts `tmux -C attach` for the session and returns the control
// connection. Pane output and notifications go to handler.
func (t Target) Attach(handler control.Handler) (*Conn, error) {
	target := "=" + t.Session
	argv := []string{tmuxBinary, "-C", "attach-session", "-t", target, "-f", "ignore-size"}
	if !t.IsLocal() {
		// The remote shell parses this line; in zsh an unquoted "=name"
		// is a command lookup. The session name is validated, so plain
		// single quotes are enough.
		argv = []string{"env", "LANG=" + t.Lang, tmuxBinary, "-C", "attach-session", "-t", "'" + target + "'", "-f", "ignore-size"}
	}
	cmd := t.command(context.Background(), argv)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("control stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("control stdout: %w", err)
	}
	stderr := &limitedBuffer{limit: stderrLimit}
	cmd.Stderr = stderr
	err = cmd.Start()
	if err != nil {
		return nil, &Error{Kind: ErrUnreachable, Detail: err.Error()}
	}
	conn := &Conn{Client: control.NewClient(stdout, stdin, handler), cmd: cmd, stderr: stderr, waited: make(chan struct{})}
	go conn.reap()
	return conn, nil
}

// command builds the process that runs argv on the target: directly for the
// local target, through ssh otherwise. Every element of argv ends up in one
// remote shell line, so callers pass only validated or quoted words.
func (t Target) command(ctx context.Context, argv []string) *exec.Cmd {
	if t.IsLocal() {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Env = localEnv(t.Lang)
		return cmd
	}
	args := append([]string{}, t.SSH[1:]...)
	args = append(args,
		"-o", "ControlMaster=auto",
		"-o", "ControlPath="+filepath.Join(t.Home, sshDir, sshControlPathName),
		"-o", "ControlPersist=10m",
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
	)
	if t.Batch {
		args = append(args, "-o", "BatchMode=yes")
	}
	args = append(args, "-T", t.Name)
	args = append(args, argv...)
	cmd := exec.CommandContext(ctx, t.SSH[0], args...)
	// A persistent ssh master started by this command inherits its output
	// pipes; without a wait delay, waiting for the command would block
	// until the master exits.
	cmd.WaitDelay = time.Second
	return cmd
}

// EnsureControlDir creates the directory for SSH control sockets when it does
// not exist yet.
func EnsureControlDir(home string) error {
	return os.MkdirAll(filepath.Join(home, sshDir), sshDirMode)
}

// runError turns a failed Prepare command into an *Error.
func (t Target) runError(err error, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = err.Error()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && (t.IsLocal() || exit.ExitCode() != sshExitTransport) {
		return &Error{Kind: ErrSession, Detail: detail}
	}
	return &Error{Kind: ErrUnreachable, Detail: detail}
}

// parsePrepared reads the key=value lines the prepare script prints.
func parsePrepared(output string) (Prepared, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if found {
			values[key] = value
		}
	}
	scriptError, failed := values[keyError]
	if failed {
		switch scriptError {
		case scriptErrorNoTmux:
			return Prepared{}, &Error{Kind: ErrNoTmux}
		case scriptErrorOldTmux:
			return Prepared{}, &Error{Kind: ErrOldTmux, Detail: values[keyVersion]}
		}
		return Prepared{}, &Error{Kind: ErrSession, Detail: scriptError + ": " + values[keyDetail]}
	}
	prepared := Prepared{
		Version:       values[keyVersion],
		State:         SessionState(values[keyState]),
		ContinuumSave: values[keyContinuum],
		Home:          values[keyHome],
	}
	switch prepared.State {
	case StateReady, StateCreated, StateRestored, StateGone:
		return prepared, nil
	}
	return Prepared{}, &Error{Kind: ErrSession, Detail: "unexpected reply: " + strings.TrimSpace(output)}
}

// localEnv is the environment for local tmux commands: the daemon's own, with
// LANG forced to a UTF-8 locale and the variables that would make tmux think
// it is nested removed.
func localEnv(lang string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "TMUX" || name == "TMUX_PANE" || name == "LANG" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "LANG="+lang)
}

// Conn is a running control-mode connection: the control client plus the
// tmux (or ssh) process behind it.
type Conn struct {
	*control.Client
	cmd    *exec.Cmd
	stderr *limitedBuffer
	waited chan struct{}
}

// reap waits for the process once its output ended, so it does not stay a
// zombie.
func (c *Conn) reap() {
	<-c.Finished()
	_ = c.cmd.Wait()
	close(c.waited)
}

// Stderr returns what the process wrote to stderr, for the log.
func (c *Conn) Stderr() string {
	return strings.TrimSpace(c.stderr.String())
}

// Shutdown detaches: it closes the command stream and, if the process does
// not end by itself, kills it.
func (c *Conn) Shutdown() {
	_ = c.Close()
	select {
	case <-c.waited:
	case <-time.After(closeGrace):
		_ = c.cmd.Process.Kill()
	}
}

// limitedBuffer keeps the first limit bytes written to it and drops the rest.
// It is safe for concurrent use, because a process's stderr is read while the
// process is still writing it.
type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

// Write stores what still fits and reports the whole chunk as written.
func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.limit - b.buf.Len()
	if room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// String returns what was kept.
func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
