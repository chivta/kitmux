package vt

import (
	"strings"
	"testing"
)

// TestSGRState checks the attributes left active after feeding whole lines.
func TestSGRState(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"zero value", nil, ""},
		{"text only", []string{"hello"}, ""},
		{"one attribute", []string{"\x1b[1mbold"}, "\x1b[1m"},
		{"attributes combine into one sequence", []string{"a\x1b[1mb\x1b[31mc\x1b[4md"}, "\x1b[1;4;31m"},
		{"several parameters in one sequence", []string{"\x1b[1;31m\x1b[42m"}, "\x1b[1;31;42m"},
		{"all flags", []string{"\x1b[1;2;3;5;7;8;9;53m"}, "\x1b[1;2;3;5;7;8;9;53m"},
		{"rapid blink is blink", []string{"\x1b[6m"}, "\x1b[5m"},
		{"bright colours", []string{"\x1b[91m\x1b[104m"}, "\x1b[91;104m"},

		// A colour replaces the previous one in its slot, so the state does
		// not grow with the number of colour changes.
		{"colour replaces colour", []string{"\x1b[31m\x1b[32m\x1b[38;5;125m\x1b[38;5;219m"}, "\x1b[38;5;219m"},
		{"default colours clear their slot", []string{"\x1b[1;31;44m\x1b[39m\x1b[49m"}, "\x1b[1m"},
		{"underline colour", []string{"\x1b[4m\x1b[58;5;9m"}, "\x1b[4;58;5;9m"},
		{"default underline colour", []string{"\x1b[4m\x1b[58;5;9m\x1b[59m"}, "\x1b[4m"},

		// Attributes turned off one by one.
		{"22 clears bold and dim", []string{"\x1b[1;2;3m\x1b[22m"}, "\x1b[3m"},
		{"23 clears italic", []string{"\x1b[1;3m\x1b[23m"}, "\x1b[1m"},
		{"24 clears underline", []string{"\x1b[1;4m\x1b[24m"}, "\x1b[1m"},
		{"25 clears blink", []string{"\x1b[1;5m\x1b[25m"}, "\x1b[1m"},
		{"27 clears reverse", []string{"\x1b[1;7m\x1b[27m"}, "\x1b[1m"},
		{"28 clears hidden", []string{"\x1b[1;8m\x1b[28m"}, "\x1b[1m"},
		{"29 clears strike", []string{"\x1b[1;9m\x1b[29m"}, "\x1b[1m"},
		{"55 clears overline", []string{"\x1b[1;53m\x1b[55m"}, "\x1b[1m"},

		// Resets.
		{"reset zero", []string{"\x1b[1m\x1b[31m\x1b[0m"}, ""},
		{"reset empty", []string{"\x1b[1m\x1b[31m\x1b[m"}, ""},
		{"reset double zero", []string{"\x1b[1m\x1b[00m"}, ""},
		{"reset then attributes", []string{"\x1b[4m\x1b[0;1;31m"}, "\x1b[1;31m"},
		{"reset as empty first parameter", []string{"\x1b[4m\x1b[;1m"}, "\x1b[1m"},
		{"reset as empty last parameter", []string{"\x1b[4m\x1b[1;m"}, ""},
		{"reset in the middle", []string{"\x1b[4m\x1b[1;0;31m"}, "\x1b[31m"},
		{"attributes after a reset", []string{"\x1b[1m\x1b[0m\x1b[32m\x1b[3m"}, "\x1b[3;32m"},

		// Extended colours take their arguments.
		{"38;5;0 is not a reset", []string{"\x1b[1m\x1b[38;5;0m"}, "\x1b[1;38;5;0m"},
		{"48;5;0 is not a reset", []string{"\x1b[1m\x1b[48;5;0m"}, "\x1b[1;48;5;0m"},
		{"58;5;0 is not a reset", []string{"\x1b[1m\x1b[58;5;0m"}, "\x1b[1;58;5;0m"},
		{"38;2;0;0;0 is not a reset", []string{"\x1b[1m\x1b[38;2;0;0;0m"}, "\x1b[1;38;2;0;0;0m"},
		{"48;2;0;0;0 is not a reset", []string{"\x1b[1m\x1b[48;2;0;0;0m"}, "\x1b[1;48;2;0;0;0m"},
		{"two extended colours", []string{"\x1b[1m\x1b[38;5;0;48;2;0;0;0m"}, "\x1b[1;38;5;0;48;2;0;0;0m"},
		{"attribute after an indexed colour", []string{"\x1b[38;5;0;1m"}, "\x1b[1;38;5;0m"},
		{"reset after an indexed colour", []string{"\x1b[1m\x1b[38;5;0;0m"}, ""},
		{"reset before an indexed colour", []string{"\x1b[1m\x1b[0;38;5;0m"}, "\x1b[38;5;0m"},
		{"truncated extended colour is ignored", []string{"\x1b[1m\x1b[38;5m"}, "\x1b[1m"},
		{"38 without arguments is ignored", []string{"\x1b[1m\x1b[38m"}, "\x1b[1m"},
		{"38 with an unknown colour kind is ignored", []string{"\x1b[1m\x1b[38;7m"}, "\x1b[1;7m"},

		// Colon forms belong to their parameter.
		{"colon rgb", []string{"\x1b[1m\x1b[38:2::255:0:0m"}, "\x1b[1;38:2::255:0:0m"},
		{"colon indexed zero", []string{"\x1b[1m\x1b[48:5:0m"}, "\x1b[1;48:5:0m"},
		{"colon underline style", []string{"\x1b[1m\x1b[4:3m"}, "\x1b[1;4:3m"},
		{"colon underline off", []string{"\x1b[1;4m\x1b[4:0m"}, "\x1b[1m"},
		{"colon underline colour", []string{"\x1b[4:3m\x1b[58:2::1:2:3m"}, "\x1b[4:3;58:2::1:2:3m"},
		{"colon form next to a reset", []string{"\x1b[1m\x1b[0;4:3m"}, "\x1b[4:3m"},
		{"double underline", []string{"\x1b[21m"}, "\x1b[21m"},

		// Carry across lines.
		{"carry across lines", []string{"\x1b[1mbold", "still \x1b[31mred", "plain text"}, "\x1b[1;31m"},
		{"reset on a later line", []string{"\x1b[1mbold", "x\x1b[0my"}, ""},
		{"reset then attribute on later lines", []string{"\x1b[1m", "\x1b[0m", "\x1b[32m"}, "\x1b[32m"},

		// Everything that is not an SGR sequence is ignored.
		{"private mode", []string{"\x1b[1m\x1b[?25l"}, "\x1b[1m"},
		{"private prefix with final m", []string{"\x1b[1m\x1b[>4;1m"}, "\x1b[1m"},
		{"intermediate with final m", []string{"\x1b[1m\x1b[0 m"}, "\x1b[1m"},
		{"other csi", []string{"\x1b[1m\x1b[2K\x1b[10;20H\x1b[0J"}, "\x1b[1m"},
		{"osc 8 hyperlink", []string{"\x1b[1m\x1b]8;;http://x/?m=0m\x1b\\link\x1b]8;;\x1b\\"}, "\x1b[1m"},
		{"unfinished sequence", []string{"\x1b[1m\x1b[0"}, "\x1b[1m"},
		{"lone ESC", []string{"\x1b[1m\x1b"}, "\x1b[1m"},
		{"ESC inside a sequence", []string{"\x1b[1\x1b[31m"}, "\x1b[31m"},
		{"m as text", []string{"\x1b[1mm0m"}, "\x1b[1m"},
		{"unknown parameter is ignored", []string{"\x1b[1;73m"}, "\x1b[1m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s SGRState
			for _, line := range tc.lines {
				s.Feed([]byte(line))
			}
			got := string(s.Active())
			if got != tc.want {
				t.Fatalf("after %q: Active() = %q, want %q", tc.lines, got, tc.want)
			}
		})
	}
}

// TestSGRStateReplay checks the property a repaint relies on: resetting and
// then applying Active gives the same attributes as the original sequences.
func TestSGRStateReplay(t *testing.T) {
	input := "\x1b[1m\x1b[38;5;125m\x1b[44m\x1b[4:3m\x1b[39m\x1b[92m\x1b[22m\x1b[3m"
	var direct SGRState
	direct.Feed([]byte(input))

	var replayed SGRState
	replayed.Feed(append([]byte("\x1b[0m"), direct.Active()...))
	if direct != replayed {
		t.Fatalf("replayed state %+v, want %+v", replayed, direct)
	}
}

// TestSGRStateStaysSmall feeds many colour changes without a reset, the way
// tmux prints coloured scrollback, and checks that Active does not grow.
func TestSGRStateStaysSmall(t *testing.T) {
	var s SGRState
	line := []byte(strings.Repeat("\x1b[38;5;125mx\x1b[39m\x1b[48;5;17my\x1b[49m\x1b[1mz\x1b[22m", 50))
	for i := 0; i < 1000; i++ {
		s.Feed(line)
	}
	if len(s.Active()) != 0 {
		t.Fatalf("Active() = %q, want empty", s.Active())
	}
	s.Feed([]byte("\x1b[38;2;1;2;3m"))
	got := string(s.Active())
	if got != "\x1b[38;2;1;2;3m" {
		t.Fatalf("Active() = %q", got)
	}
}
