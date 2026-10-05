// Package tabs holds the rules for how tmux windows appear as kitty tabs:
// what a tab is called and where it sits in the tab bar.
package tabs

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// LocalTarget is the target name for the machine kitmux runs on.
	LocalTarget = "local"

	titlePrefix   = "tmux-"
	hostSeparator = ":"
	userSeparator = "@"
)

// Title returns the kitty tab title for a tmux window: "tmux-<index> <name>"
// for the local target and "<host>:tmux-<index> <name>" for an SSH target,
// where host is the destination without its "user@" part. An empty name
// leaves just "tmux-<index>".
func Title(target string, index int, name string) string {
	var b strings.Builder
	if target != LocalTarget {
		b.WriteString(Host(target))
		b.WriteString(hostSeparator)
	}
	b.WriteString(titlePrefix)
	b.WriteString(strconv.Itoa(index))
	clean := Sanitize(name)
	if clean != "" {
		b.WriteByte(' ')
		b.WriteString(clean)
	}
	return b.String()
}

// Host strips the "user@" prefix from an SSH destination.
func Host(target string) string {
	at := strings.LastIndex(target, userSeparator)
	if at < 0 {
		return target
	}
	return target[at+len(userSeparator):]
}

// Sanitize removes characters that must not reach a tab title: control
// characters, invisible format characters (bidi overrides and marks,
// zero-width spaces and joiners), line and paragraph separators, and invalid
// UTF-8. Surrounding whitespace is trimmed.
func Sanitize(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		i += size
		if r == utf8.RuneError && size == 1 {
			continue
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
