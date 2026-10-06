package cli

import (
	"errors"
	"fmt"

	"kitmux/internal/tmux"
)

// Code is a machine-readable error code. Every error kitmux shows to the user
// carries one; the text for it lives in messages (messages.go).
type Code string

const (
	// CodeUsage is a command line that could not be parsed.
	CodeUsage Code = "USAGE"
	// CodeBadSession is a session name outside the allowed pattern.
	CodeBadSession Code = "BAD_SESSION"
	// CodeBadTarget is a target that is empty, looks like an option or has
	// unprintable characters.
	CodeBadTarget Code = "BAD_TARGET"
	// CodeDuplicateTarget is a target named twice.
	CodeDuplicateTarget Code = "DUPLICATE_TARGET"
	// CodeNoHome means the home directory could not be determined.
	CodeNoHome Code = "NO_HOME"
	// CodeUnreachable means a target's command could not be run, e.g. ssh
	// failed to connect.
	CodeUnreachable Code = "TARGET_UNREACHABLE"
	// CodeNoTmux means tmux is not installed on a target.
	CodeNoTmux Code = "NO_TMUX"
	// CodeOldTmux means a target's tmux is older than kitmux supports.
	CodeOldTmux Code = "OLD_TMUX"
	// CodeSessionFailed means the session could not be listed or created on
	// a target.
	CodeSessionFailed Code = "SESSION_FAILED"
	// CodeDaemonFailed means the background daemon did not come up.
	CodeDaemonFailed Code = "DAEMON_FAILED"
	// CodeKittyFailed means kitty could not be started or reached.
	CodeKittyFailed Code = "KITTY_FAILED"
	// CodeRuntimeDir means no usable runtime directory could be created.
	CodeRuntimeDir Code = "RUNTIME_DIR"
	// CodeInternal is anything else.
	CodeInternal Code = "INTERNAL"
)

// Error is an error with a code, the target it concerns and a detail to fill
// into the code's message.
type Error struct {
	Code   Code
	Target string
	Detail string
}

// Error renders the user-facing message.
func (e *Error) Error() string {
	format, ok := messages[e.Code]
	if !ok {
		format = messages[CodeInternal]
	}
	return fmt.Sprintf(format, e.Target, e.Detail)
}

// IsUsage reports whether err is about the command line itself, in which case
// the caller prints the synopsis too.
func IsUsage(err error) bool {
	var coded *Error
	if !errors.As(err, &coded) {
		return false
	}
	switch coded.Code {
	case CodeUsage, CodeBadSession, CodeBadTarget, CodeDuplicateTarget:
		return true
	}
	return false
}

// TargetError turns an error from preparing or attaching a target into the
// coded error shown to the user.
func TargetError(target string, err error) *Error {
	var detailed *tmux.Error
	if !errors.As(err, &detailed) {
		return &Error{Code: CodeSessionFailed, Target: target, Detail: err.Error()}
	}
	code := CodeSessionFailed
	switch {
	case errors.Is(err, tmux.ErrUnreachable):
		code = CodeUnreachable
	case errors.Is(err, tmux.ErrNoTmux):
		code = CodeNoTmux
	case errors.Is(err, tmux.ErrOldTmux):
		code = CodeOldTmux
	}
	return &Error{Code: code, Target: target, Detail: detailed.Detail}
}
