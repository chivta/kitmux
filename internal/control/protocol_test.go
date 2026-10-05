package control

import (
	"bytes"
	"testing"
)

func TestDecodeOctal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"escape and csi", `\033[1mhi\033[0m`, "\x1b[1mhi\x1b[0m"},
		{"crlf", `a\015\012b`, "a\r\nb"},
		{"backslash", `a\134b`, `a\b`},
		{"nul", `\000`, "\x00"},
		{"utf8 passes raw", "é┌─\\033", "é┌─\x1b"},
		{"short tail is literal", `ab\03`, `ab\03`},
		{"non octal digits are literal", `\09x`, `\09x`},
		{"value above a byte is literal", `\777`, `\777`},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecodeOctal([]byte(tc.in))
			if string(got) != tc.want {
				t.Fatalf("DecodeOctal(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Line
	}{
		{
			name: "begin",
			in:   "%begin 1791195618 394 1",
			want: Line{Kind: KindBegin, Guard: Guard{Time: 1791195618, Number: 394, Flags: 1}},
		},
		{
			name: "end",
			in:   "%end 1791195618 394 1",
			want: Line{Kind: KindEnd, Guard: Guard{Time: 1791195618, Number: 394, Flags: 1}},
		},
		{
			name: "error",
			in:   "%error 1791196180 497 0",
			want: Line{Kind: KindError, Guard: Guard{Time: 1791196180, Number: 497}},
		},
		{
			name: "output",
			in:   `%output %12 a\015\012`,
			want: Line{Kind: KindOutput, Pane: "%12", Data: []byte("a\r\n")},
		},
		{
			name: "output with spaces in data",
			in:   "%output %0 two  spaces ",
			want: Line{Kind: KindOutput, Pane: "%0", Data: []byte("two  spaces ")},
		},
		{
			name: "output without data",
			in:   "%output %3",
			want: Line{Kind: KindOutput, Pane: "%3"},
		},
		{
			name: "layout change",
			in:   "%layout-change @0 f91d,120x40,0,0{60x40,0,0,0,59x40,61,0,1} f91d *",
			want: Line{Kind: KindNotification, Name: "layout-change", Args: "@0 f91d,120x40,0,0{60x40,0,0,0,59x40,61,0,1} f91d *"},
		},
		{
			name: "notification without args",
			in:   "%sessions-changed",
			want: Line{Kind: KindNotification, Name: "sessions-changed"},
		},
		{
			name: "exit with reason",
			in:   "%exit server exited",
			want: Line{Kind: KindExit, Name: "exit", Args: "server exited"},
		},
		{
			name: "exit without reason",
			in:   "%exit",
			want: Line{Kind: KindExit, Name: "exit"},
		},
		{
			name: "plain text",
			in:   "@0 0 main",
			want: Line{Kind: KindText, Text: []byte("@0 0 main")},
		},
		{
			name: "malformed guard is text",
			in:   "%begin nope",
			want: Line{Kind: KindText, Text: []byte("%begin nope")},
		},
		{
			name: "lone percent is text",
			in:   "%",
			want: Line{Kind: KindText, Text: []byte("%")},
		},
		{
			name: "empty",
			in:   "",
			want: Line{Kind: KindText, Text: []byte{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseLine([]byte(tc.in))
			if got.Kind != tc.want.Kind || got.Guard != tc.want.Guard || got.Pane != tc.want.Pane ||
				got.Name != tc.want.Name || got.Args != tc.want.Args ||
				!bytes.Equal(got.Data, tc.want.Data) || !bytes.Equal(got.Text, tc.want.Text) {
				t.Fatalf("ParseLine(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestGuardFromClient(t *testing.T) {
	if (Guard{Flags: 0}).FromClient() {
		t.Fatal("flags 0 must not be from the client")
	}
	if !(Guard{Flags: 1}).FromClient() {
		t.Fatal("flags 1 must be from the client")
	}
}

func TestQuote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain", "'plain'"},
		{"two words", "'two words'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
		{"#{pane_id};", "'#{pane_id};'"},
	}
	for _, tc := range cases {
		got := Quote(tc.in)
		if got != tc.want {
			t.Errorf("Quote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
