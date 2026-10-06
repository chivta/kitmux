package cli

import (
	"fmt"

	"kitmux/internal/ipc"
)

// messages maps every error code to its user-facing text. %[1]s is the
// target the error is about (may be empty), %[2]s the detail.
var messages = map[Code]string{
	CodeUsage:           "%[2]s",
	CodeBadSession:      "invalid session name %[2]q: use 1-32 of A-Z a-z 0-9 _ . - (not \".\" or \"..\")",
	CodeBadTarget:       "invalid target %[2]q: a target is \"local\" or an ssh destination and cannot start with \"-\"",
	CodeDuplicateTarget: "target %[2]q is given twice",
	CodeNoHome:          "cannot find the home directory: %[2]s",
	CodeUnreachable:     "cannot reach %[1]s: %[2]s",
	CodeNoTmux:          "tmux is not installed on %[1]s",
	CodeOldTmux:         "%[1]s runs %[2]s; kitmux needs tmux 3.2 or newer",
	CodeSessionFailed:   "cannot open the tmux session on %[1]s: %[2]s",
	CodeDaemonFailed:    "the kitmux daemon did not start: %[2]s",
	CodeKittyFailed:     "cannot start kitty: %[2]s",
	CodeRuntimeDir:      "cannot create a runtime directory: %[2]s",
	CodeInternal:        "%[2]s",
}

// actionMessages maps the result codes of key actions to the text the
// confirmation overlay shows.
var actionMessages = map[ipc.Code]string{
	ipc.CodeNotManaged:   "This window does not show a tmux pane.",
	ipc.CodeDisconnected: "The tmux connection is down; try again once it is back.",
	ipc.CodeBadRequest:   "kitmux did not understand the request.",
}

// Prompt texts of the confirmation overlay; %s is the tab title.
const (
	promptKillPane   = "Kill tmux pane in %s? [y/N] "
	promptKillWindow = "Kill tmux window %s? [y/N] "
	textDaemonGone   = "The kitmux daemon is not running."
)

// KillPrompt returns the question the confirmation overlay asks.
func KillPrompt(scope ipc.Scope, title string) string {
	if scope == ipc.ScopeWindow {
		return fmt.Sprintf(promptKillWindow, title)
	}
	return fmt.Sprintf(promptKillPane, title)
}

// ActionMessage returns the text for a key action's result code.
func ActionMessage(code ipc.Code) string {
	text, ok := actionMessages[code]
	if !ok {
		return string(code)
	}
	return text
}

// DaemonGoneMessage is shown when a key action cannot reach the daemon.
func DaemonGoneMessage() string {
	return textDaemonGone
}
