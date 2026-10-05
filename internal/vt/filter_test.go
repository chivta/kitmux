package vt

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// filterChunks feeds chunks to a fresh Filter and returns everything it
// forwarded.
func filterChunks(chunks ...string) string {
	var f Filter
	var out []byte
	for _, chunk := range chunks {
		out = append(out, f.Write([]byte(chunk))...)
	}
	return string(out)
}

// checkAllSplits requires that in gives want when written whole, one byte at a
// time, and in every split into two chunks.
func checkAllSplits(t *testing.T, in, want string) {
	t.Helper()
	got := filterChunks(in)
	if got != want {
		t.Fatalf("whole: Write(%q) = %q, want %q", in, got, want)
	}
	got = filterChunks(strings.Split(in, "")...)
	if got != want {
		t.Fatalf("byte by byte: %q gave %q, want %q", in, got, want)
	}
	for i := 0; i <= len(in); i++ {
		got = filterChunks(in[:i], in[i:])
		if got != want {
			t.Fatalf("split at %d: %q + %q gave %q, want %q", i, in[:i], in[i:], got, want)
		}
	}
}

// filterCase is one input and what the Filter must forward for it.
type filterCase struct {
	name string
	in   string
	want string
}

// kept builds a case whose input must be forwarded unchanged.
func kept(name, in string) filterCase {
	return filterCase{name, in, in}
}

// dropped builds a case whose input must vanish entirely.
func dropped(name, in string) filterCase {
	return filterCase{name, in, ""}
}

// TestFilter runs every rule of the Filter, and the look-alike sequences each
// rule must leave alone, through every way of cutting the input.
func TestFilter(t *testing.T) {
	cases := []filterCase{
		// Data and sequences that are not queries.
		kept("empty", ""),
		kept("plain text", "hello world"),
		kept("utf8", "héllo — 日本語 ✓"),
		kept("c0 controls", "a\r\n\tb\a\b\x00c\x0e\x0f"),
		kept("utf8 encoded c1 is data", "\u009b6n\u009d11;?\u009c"),
		kept("raw 8-bit csi byte is data", "\x9b6n"),
		kept("sgr", "\x1b[1;31mred\x1b[0m"),
		kept("cursor and modes", "\x1b[2J\x1b[H\x1b[?25l\x1b[?1049h\x1b[10;20H"),
		kept("two byte escapes", "\x1b7\x1b8\x1bM\x1bc\x1b=\x1b>\x1b\\"),
		kept("escape with intermediates", "\x1b(B\x1b#8\x1b(0\x1b F"),

		// Device attributes.
		dropped("DA", "\x1b[c"),
		dropped("DA with zero", "\x1b[0c"),
		dropped("DA2", "\x1b[>c"),
		dropped("DA2 with zero", "\x1b[>0c"),
		dropped("DA3", "\x1b[=c"),

		// Device status report.
		dropped("DSR status", "\x1b[5n"),
		dropped("DSR cursor position", "\x1b[6n"),
		dropped("DSR without parameter", "\x1b[n"),
		dropped("DECXCPR", "\x1b[?6n"),
		dropped("DSR colour scheme", "\x1b[?996n"),
		kept("modifyOtherKeys reset is not DSR", "\x1b[>4n"),
		kept("n with intermediate", "\x1b[6 n"),

		// DECRQM.
		dropped("DECRQM private", "\x1b[?2026$p"),
		dropped("DECRQM ansi", "\x1b[4$p"),
		kept("DECSTR", "\x1b[!p"),
		kept("DECSCL", "\x1b[62;1\"p"),
		kept("XTSMPOINTER", "\x1b[>2p"),
		kept("parameter after intermediate", "\x1b[$1p"),

		// XTVERSION.
		dropped("XTVERSION", "\x1b[>q"),
		dropped("XTVERSION with zero", "\x1b[>0q"),
		kept("cursor shape", "\x1b[2 q"),
		kept("DECLL", "\x1b[1q"),

		// Kitty keyboard protocol.
		dropped("kitty keyboard query", "\x1b[?u"),
		kept("kitty keyboard push", "\x1b[>1u"),
		kept("kitty keyboard pop", "\x1b[<u"),
		kept("kitty keyboard pop count", "\x1b[<1u"),
		kept("kitty keyboard set", "\x1b[=1;1u"),
		kept("restore cursor", "\x1b[u"),

		// Window operations.
		dropped("window report with more parameters", "\x1b[14;2t"),
		kept("title stack push", "\x1b[22;0t"),
		kept("title stack pop", "\x1b[23;0t"),
		kept("resize window", "\x1b[8;24;80t"),
		kept("t without parameter", "\x1b[t"),
		kept("deiconify", "\x1b[1t"),
		kept("t 12 is not a report", "\x1b[12t"),
		kept("t 17 is not a report", "\x1b[17t"),
		kept("XTSMTITLE", "\x1b[>14t"),
		kept("t with intermediate", "\x1b[14 t"),
		kept("t with a huge parameter", "\x1b[99999999999999999999t"),

		// DECREQTPARM.
		dropped("DECREQTPARM", "\x1b[x"),
		dropped("DECREQTPARM with parameter", "\x1b[1x"),
		kept("DECFRA", "\x1b[65;1;1;2;2$x"),
		kept("DECSACE", "\x1b[2*x"),

		// DECID.
		dropped("DECID", "\x1bZ"),

		// OSC queries and the OSCs that look like them.
		dropped("osc background query with BEL", "\x1b]11;?\a"),
		dropped("osc background query with ST", "\x1b]11;?\x1b\\"),
		dropped("osc palette query", "\x1b]4;1;?\a"),
		dropped("osc palette set and query", "\x1b]4;1;rgb:ff/00/00;2;?\x1b\\"),
		dropped("osc two queries", "\x1b]10;?;?\a"),
		dropped("osc clipboard query", "\x1b]52;c;?\a"),
		dropped("osc clipboard query without selection", "\x1b]52;;?\x1b\\"),
		dropped("osc 21 query", "\x1b]21;foreground=?\x1b\\"),
		dropped("osc 21 set and query", "\x1b]21;foreground=red;background=?\a"),
		kept("osc title with BEL", "\x1b]0;title\a"),
		kept("osc title with ST", "\x1b]2;what?\x1b\\"),
		kept("osc title that is a question mark", "\x1b]2;?\a"),
		kept("osc icon name", "\x1b]1;?\a"),
		kept("osc cwd", "\x1b]7;file://host/dir\x1b\\"),
		kept("osc hyperlink", "\x1b]8;;https://example.com/?q=1\x1b\\link\x1b]8;;\x1b\\"),
		kept("osc background set", "\x1b]11;rgb:00/00/00\a"),
		kept("osc palette set", "\x1b]4;1;rgb:ff/00/00\x1b\\"),
		kept("osc clipboard write", "\x1b]52;c;aGVsbG8=\a"),
		kept("osc prompt mark", "\x1b]133;A\a"),
		kept("osc 21 set", "\x1b]21;foreground=red\a"),
		kept("osc colour reset", "\x1b]104\a"),
		kept("osc without value", "\x1b]11\a"),
		kept("osc reset background", "\x1b]111;?\a"),
		kept("osc field longer than a question mark", "\x1b]11;?x\a"),
		kept("osc number with a letter", "\x1b]11x;?\a"),
		kept("osc huge number", "\x1b]99999999999999999999;?\a"),
		kept("osc empty", "\x1b]\a"),

		// DCS.
		dropped("XTGETTCAP", "\x1bP+q544e\x1b\\"),
		dropped("DECRQSS", "\x1bP$qm\x1b\\"),
		dropped("BEL does not end a DCS", "\x1bP+q54\a4e\x1b\\"),
		kept("synchronized update begin", "\x1bP=1s\x1b\\"),
		kept("synchronized update end", "\x1bP=2s\x1b\\"),
		kept("sixel", "\x1bPq#0;2;0;0;0#0~~@@vv\x1b\\"),
		kept("sixel with parameters", "\x1bP0;0;0q~~\x1b\\"),
		kept("XTSETTCAP", "\x1bP+p544e=1\x1b\\"),
		kept("dcs that only starts like passthrough", "\x1bPtmu\x1b\\"),
		kept("dcs that only starts like a request", "\x1bP+\x1b\\"),
		kept("dcs empty", "\x1bP\x1b\\"),

		// APC, PM and SOS always pass.
		kept("apc kitty graphics", "\x1b_Gf=100,a=q;AAAA\x1b\\"),
		kept("apc with BEL inside", "\x1b_a\ab\x1b\\"),
		kept("apc that looks like passthrough", "\x1b_tmux;abc\x1b\\"),
		kept("apc that looks like a request", "\x1b_+q544e\x1b\\"),
		kept("pm", "\x1b^hello\x1b\\"),
		kept("sos", "\x1bXhello\x1b\\"),

		// Queries next to other output.
		{"query between text", "a\x1b[6nb", "ab"},
		{"osc query with BEL between text", "x\x1b]11;?\ay", "xy"},
		{"osc query with ST between text", "x\x1b]11;?\x1b\\y", "xy"},
		{"dcs query between text", "x\x1bP+q544e\x1b\\y", "xy"},
		{"DECID between text", "a\x1bZb", "ab"},
		{"several queries", "\x1b[c\x1b[>c\x1b[6n\x1b[?u\x1b]10;?\a", ""},
		{"query between kept sequences", "\x1b[1m\x1b[6n\x1b[0m", "\x1b[1m\x1b[0m"},

		// tmux passthrough.
		{"passthrough text", "a\x1bPtmux;hi\x1b\\b", "ahib"},
		{"passthrough empty", "\x1bPtmux;\x1b\\", ""},
		{"passthrough doubled escape", "\x1bPtmux;\x1b\x1b[1mx\x1b\\", "\x1b[1mx"},
		{"passthrough two doubled escapes in a row", "\x1bPtmux;\x1b\x1b\x1b\x1bx\x1b\\", "\x1b\x1bx"},
		{"passthrough keeps csi query", "\x1bPtmux;\x1b\x1b[6n\x1b\\", "\x1b[6n"},
		{"passthrough keeps osc query with BEL", "\x1bPtmux;\x1b\x1b]52;c;?\a\x1b\\", "\x1b]52;c;?\a"},
		{"passthrough osc with ST", "\x1bPtmux;\x1b\x1b]11;?\x1b\x1b\\\x1b\\", "\x1b]11;?\x1b\\"},
		{"passthrough kitty graphics", "\x1bPtmux;\x1b\x1b_Gi=1,a=q;AAAA\x1b\x1b\\\x1b\\", "\x1b_Gi=1,a=q;AAAA\x1b\\"},
		{"passthrough utf8 and controls", "\x1bPtmux;é\r\n\a\x1b\\", "é\r\n\a"},
		{"passthrough single escape loses the escape", "\x1bPtmux;\x1b[6n\x1b\\", "[6n"},
		{"query after passthrough", "\x1bPtmux;\x1b\x1b[c\x1b\\\x1b[c!", "\x1b[c!"},

		// Aborted and malformed sequences.
		kept("CAN aborts csi", "\x1b[6\x18n"),
		kept("SUB aborts csi", "\x1b[6\x1an"),
		kept("other c0 ends csi", "\x1b[6\nn"),
		kept("8-bit byte ends csi", "\x1b[6\xc3\xa9n"),
		{"ESC inside csi then query", "\x1b[12\x1b[6nX", "\x1b[12X"},
		kept("ESC inside csi then sgr", "\x1b[1\x1b[31mX"),
		{"ESC before a query", "\x1b\x1b[6n", "\x1b"},
		kept("ESC inside escape with intermediates", "\x1b(\x1b(B"),
		{"ESC inside osc", "\x1b]0;title\x1b[6nX", "\x1b]0;titleX"},
		{"ESC inside osc query", "\x1b]11;?\x1b[6nX", "X"},
		{"ESC inside osc query then kept sequence", "\x1b]11;?\x1b[mX", "\x1b[mX"},
		kept("double ESC inside osc", "\x1b]0;a\x1b\x1b\\"),
		{"ESC inside dcs query", "\x1bP+q54\x1b[mX", "\x1b[mX"},
		{"ESC inside streamed dcs", "\x1bPqabc\x1b[cX", "\x1bPqabcX"},
		{"ESC inside apc", "\x1b_abc\x1b[6nX", "\x1b_abcX"},

		// CSI length limit.
		kept("csi over the limit", "\x1b["+strings.Repeat("1", 300)+"n"),
		kept("csi one byte over the limit", "\x1b["+strings.Repeat("1", maxCSILen-introLen+1)+"n"),
		dropped("csi at the limit", "\x1b["+strings.Repeat("1", maxCSILen-introLen)+"n"),
		kept("escape intermediates over the limit", "\x1b"+strings.Repeat(" ", 300)+"B"),

		// Unfinished sequences are held.
		{"unfinished csi", "abc\x1b[6", "abc"},
		{"unfinished osc", "abc\x1b]0;title", "abc"},
		{"unfinished escape", "abc\x1b", "abc"},
		{"unfinished passthrough intro", "abc\x1bPtmux", "abc"},
		{"unfinished passthrough payload", "abc\x1bPtmux;def\x1b", "abcdef"},
		{"unfinished apc", "abc\x1b_Gdef\x1b", "abc\x1b_Gdef"},
	}
	for _, op := range []int{11, 13, 14, 15, 16, 18, 19, 20, 21} {
		cases = append(cases, dropped(fmt.Sprintf("window report %d", op), fmt.Sprintf("\x1b[%dt", op)))
	}
	for _, number := range []int{5, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19} {
		cases = append(cases, dropped(fmt.Sprintf("osc %d query", number), fmt.Sprintf("\x1b]%d;?\x1b\\", number)))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkAllSplits(t, tc.in, tc.want)
		})
	}
}

// TestFilterChunking checks on random mixes of sequence bytes that the output
// never depends on where the input is cut.
func TestFilterChunking(t *testing.T) {
	const (
		inputs = 3000
		maxLen = 24
	)
	alphabet := []string{
		"\x1b", "\x1b", "\x1b", "[", "]", "P", "_", "^", "X", "Z", "\\", "\a",
		"\x18", "\n", ";", "?", ">", "=", "$", " ", "+", "q", "tmux;", "c",
		"n", "p", "t", "u", "x", "m", "0", "1", "4", "5", "2", "a", "é",
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range inputs {
		var in strings.Builder
		for range rng.IntN(maxLen) {
			in.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		checkAllSplits(t, in.String(), filterChunks(in.String()))
	}
}

// TestFilterStreaming checks what each Write returns on its own: what is held
// back, and what is forwarded before its sequence has ended.
func TestFilterStreaming(t *testing.T) {
	// write is one Write call and what it must return.
	type write struct {
		in   string
		want string
	}
	cases := []struct {
		name   string
		writes []write
	}{
		{"csi is held until its final byte", []write{
			{"a\x1b[3", "a"},
			{"1", ""},
			{"mb", "\x1b[31mb"},
		}},
		{"osc is held until its terminator", []write{
			{"\x1b]0;ti", ""},
			{"tle", ""},
			{"\a", "\x1b]0;title\a"},
		}},
		{"passthrough payload is not buffered", []write{
			{"\x1bPtmux;", ""},
			{"abc", "abc"},
			{"\x1b", ""},
			{"\x1b", "\x1b"},
			{"def\x1b", "def"},
			{"\\", ""},
			{"\x1b[6n", ""},
		}},
		{"apc is not buffered", []write{
			{"\x1b_Gabc", "\x1b_Gabc"},
			{"def\x1b", "def"},
			{"\\", "\x1b\\"},
		}},
		{"dcs that cannot be a query is not buffered", []write{
			{"\x1bP", ""},
			{"q#0", "\x1bPq#0"},
			{"~~", "~~"},
			{"\x1b\\", "\x1b\\"},
		}},
		{"dcs query is held and dropped", []write{
			{"\x1bP+", ""},
			{"q544e", ""},
			{"\x1b\\x", "x"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f Filter
			for i, w := range tc.writes {
				got := string(f.Write([]byte(w.in)))
				if got != w.want {
					t.Fatalf("write %d: Write(%q) = %q, want %q", i, w.in, got, w.want)
				}
			}
		})
	}
}

// TestFilterReset checks that Reset forgets a sequence cut off in any state,
// so the bytes that follow are read as fresh ground-state input.
func TestFilterReset(t *testing.T) {
	cases := []struct {
		name   string
		before string // written before Reset
		held   string // what that write must forward
		after  string // written after Reset
		want   string // what that write must forward
	}{
		{"mid csi", "a\x1b[6", "a", "nb", "nb"},
		{"after ESC", "a\x1b", "a", "[6n", "[6n"},
		{"mid osc", "\x1b]11;?", "", "x\a", "x\a"},
		{"mid osc at ESC", "\x1b]0;t\x1b", "", "\\x", "\\x"},
		{"mid dcs query", "\x1bP+q54", "", "4e\x1b[6n", "4e"},
		{"mid passthrough", "\x1bPtmux;abc", "abc", "\x1b[6n", ""},
		{"mid passthrough at ESC", "\x1bPtmux;abc\x1b", "abc", "\\x", "\\x"},
		{"mid apc", "\x1b_Gabc", "\x1b_Gabc", "\x1b[6n", ""},
		{"in ground", "abc", "abc", "\x1b[6nd", "d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f Filter
			got := string(f.Write([]byte(tc.before)))
			if got != tc.held {
				t.Fatalf("before Reset: Write(%q) = %q, want %q", tc.before, got, tc.held)
			}
			f.Reset()
			got = string(f.Write([]byte(tc.after)))
			if got != tc.want {
				t.Fatalf("after Reset: Write(%q) = %q, want %q", tc.after, got, tc.want)
			}
		})
	}
}

// TestFilterStringLimit checks the cap on a buffered string: at the cap a
// query is still dropped, one byte over it the string is forwarded unchanged
// and the Filter still finds its terminator.
func TestFilterStringLimit(t *testing.T) {
	const (
		head = "\x1b]52;c;"
		tail = ";?\a"
		rest = "\x1b[6nz"
	)
	// The buffer holds everything but the terminator.
	fill := maxStringLen - len(head) - len(tail) + len(terminatorBEL)
	cases := []filterCase{
		{"at the limit", head + strings.Repeat("A", fill) + tail + rest, "z"},
		{"over the limit", head + strings.Repeat("A", fill+1) + tail + rest, head + strings.Repeat("A", fill+1) + tail + "z"},
		{"over the limit with ST", head + strings.Repeat("A", fill+1) + ";?\x1b\\" + rest, head + strings.Repeat("A", fill+1) + ";?\x1b\\z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []int{len(tc.in), 1, 7, 4096} {
				var chunks []string
				for in := tc.in; len(in) > 0; {
					n := min(size, len(in))
					chunks = append(chunks, in[:n])
					in = in[n:]
				}
				got := filterChunks(chunks...)
				if got != tc.want {
					t.Fatalf("chunk size %d: got %d bytes, want %d bytes", size, len(got), len(tc.want))
				}
			}
		})
	}
}

// TestFilterWriteOwnership checks that a returned slice shares memory neither
// with the input nor with later output.
func TestFilterWriteOwnership(t *testing.T) {
	var f Filter
	in := []byte("abc\x1b[3")
	first := f.Write(in)
	for i := range in {
		in[i] = 'X'
	}
	second := f.Write([]byte("1mdef"))
	for i := range second {
		second[i] = 'Y'
	}
	third := f.Write([]byte("ghi\x1b[0m"))
	if string(first) != "abc" {
		t.Fatalf("first = %q, want %q", first, "abc")
	}
	if string(third) != "ghi\x1b[0m" {
		t.Fatalf("third = %q, want %q", third, "ghi\x1b[0m")
	}
	fourth := f.Write([]byte("\x1b[1"))
	fifth := f.Write([]byte("m"))
	if len(fourth) != 0 || string(fifth) != "\x1b[1m" {
		t.Fatalf("fourth, fifth = %q, %q, want %q, %q", fourth, fifth, "", "\x1b[1m")
	}
}
