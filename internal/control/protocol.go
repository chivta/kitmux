// Package control speaks the tmux control-mode protocol: it parses the line
// stream a `tmux -C` client prints and matches command replies to the commands
// that were sent.
package control

import (
	"bytes"
	"strconv"
)

// Kind classifies one line of control-mode output.
type Kind int

const (
	// KindText is a line that is not a protocol line: the body of a reply
	// block.
	KindText Kind = iota
	// KindBegin opens a reply block ("%begin time number flags").
	KindBegin
	// KindEnd closes a reply block of a command that succeeded.
	KindEnd
	// KindError closes a reply block of a command that failed.
	KindError
	// KindOutput carries pane output ("%output %pane data").
	KindOutput
	// KindExit announces that the client is detaching ("%exit [reason]").
	KindExit
	// KindNotification is any other "%name args" line.
	KindNotification
)

const (
	prefixBegin  = "%begin "
	prefixEnd    = "%end "
	prefixError  = "%error "
	prefixOutput = "%output "
	nameExit     = "exit"

	// guardFlagFromClient is set in a block's flags when the block answers a
	// command this client sent, as opposed to one tmux ran on its own.
	guardFlagFromClient = 1

	octalDigits = 3
	escapeByte  = '\\'
)

// Guard holds the three numbers of a %begin, %end or %error line. A block's
// closing line repeats the numbers of its opening line.
type Guard struct {
	Time   int64
	Number int64
	Flags  int
}

// FromClient reports whether the block answers a command sent by this client.
func (g Guard) FromClient() bool {
	return g.Flags&guardFlagFromClient != 0
}

// Line is one parsed line of control-mode output. Only the fields that belong
// to its Kind are set.
type Line struct {
	Kind Kind
	// Guard is set for KindBegin, KindEnd and KindError.
	Guard Guard
	// Pane is the pane id ("%3") of a KindOutput line.
	Pane string
	// Data is the decoded payload of a KindOutput line.
	Data []byte
	// Name is the notification name without the leading "%", for
	// KindNotification and KindExit.
	Name string
	// Args is the rest of a notification line after the name.
	Args string
	// Text is the raw line for KindText.
	Text []byte
}

// ParseLine classifies one line (without its trailing newline). It does not
// know whether the line sits inside a reply block; the caller decides whether
// a "%..." line there is body text.
func ParseLine(line []byte) Line {
	if len(line) == 0 || line[0] != '%' {
		return Line{Kind: KindText, Text: line}
	}
	s := string(line)
	switch {
	case hasPrefix(s, prefixOutput):
		return parseOutput(line)
	case hasPrefix(s, prefixBegin):
		return parseGuard(KindBegin, s[len(prefixBegin):], line)
	case hasPrefix(s, prefixEnd):
		return parseGuard(KindEnd, s[len(prefixEnd):], line)
	case hasPrefix(s, prefixError):
		return parseGuard(KindError, s[len(prefixError):], line)
	}
	name, args := splitWord(s[1:])
	if name == "" {
		return Line{Kind: KindText, Text: line}
	}
	kind := KindNotification
	if name == nameExit {
		kind = KindExit
	}
	return Line{Kind: kind, Name: name, Args: args}
}

// parseOutput parses "%output %pane data".
func parseOutput(line []byte) Line {
	rest := line[len(prefixOutput):]
	sp := bytes.IndexByte(rest, ' ')
	if sp < 0 {
		return Line{Kind: KindOutput, Pane: string(rest)}
	}
	return Line{Kind: KindOutput, Pane: string(rest[:sp]), Data: DecodeOctal(rest[sp+1:])}
}

// parseGuard parses the "time number flags" tail of a guard line. A malformed
// tail makes the line plain text.
func parseGuard(kind Kind, tail string, raw []byte) Line {
	timeStr, rest := splitWord(tail)
	numStr, flagStr := splitWord(rest)
	t, errTime := strconv.ParseInt(timeStr, 10, 64)
	n, errNum := strconv.ParseInt(numStr, 10, 64)
	f, errFlags := strconv.Atoi(flagStr)
	if errTime != nil || errNum != nil || errFlags != nil {
		return Line{Kind: KindText, Text: raw}
	}
	return Line{Kind: kind, Guard: Guard{Time: t, Number: n, Flags: f}}
}

// DecodeOctal turns tmux's output escaping back into raw bytes. tmux writes
// every byte below 0x20 and the backslash as a backslash followed by three
// octal digits; all other bytes are literal.
func DecodeOctal(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c != escapeByte || i+octalDigits >= len(data) {
			out = append(out, c)
			continue
		}
		value, ok := octalValue(data[i+1 : i+1+octalDigits])
		if !ok {
			out = append(out, c)
			continue
		}
		out = append(out, value)
		i += octalDigits
	}
	return out
}

// octalValue converts three octal digits to a byte.
func octalValue(digits []byte) (byte, bool) {
	value := 0
	for _, d := range digits {
		if d < '0' || d > '7' {
			return 0, false
		}
		value = value*8 + int(d-'0')
	}
	if value > 0xff {
		return 0, false
	}
	return byte(value), true
}

// hasPrefix reports whether s starts with prefix.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// splitWord splits s at its first space into the word before it and the rest
// after it.
func splitWord(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
