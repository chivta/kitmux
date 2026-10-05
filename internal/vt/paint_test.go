package vt

import (
	"errors"
	"testing"
)

// State lines below were printed by tmux 3.7c with StateFormat.
const (
	stateAlternate = "80,24,18,1,3,2,0,23,1,0,0,1,1,1,1,0,0,1,0,1,4,9,default,0"
	statePlain     = "80,24,0,0,0,2,4294967295,4294967295,1,0,0,1,0,0,0,0,0,0,0,0,0,23,default,0"
)

// lines turns strings into capture output.
func lines(rows ...string) [][]byte {
	out := make([][]byte, len(rows))
	for i, row := range rows {
		out[i] = []byte(row)
	}
	return out
}

func TestParseState(t *testing.T) {
	got, err := ParseState(stateAlternate)
	if err != nil {
		t.Fatalf("ParseState: %v", err)
	}
	want := PaneState{
		Width: 80, Height: 24, HistorySize: 18, AlternateOn: true,
		CursorX: 3, CursorY: 2, SavedX: 0, SavedY: 23,
		CursorVisible: true, Wrap: true, CursorKeysApp: true, KeypadApp: true,
		MouseStandard: true, MouseSGR: true, BracketedPaste: true,
		ScrollTop: 4, ScrollBottom: 9, CursorShape: "default",
	}
	if got != want {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}

	got, err = ParseState(statePlain)
	if err != nil {
		t.Fatalf("ParseState: %v", err)
	}
	if got.AlternateOn || got.HistorySize != 0 || got.CursorY != 2 || got.ScrollBottom != 23 || !got.Wrap {
		t.Fatalf("plain state = %+v", got)
	}
}

func TestParseStateOldTmux(t *testing.T) {
	// A tmux that does not know some formats prints them empty.
	got, err := ParseState("100,30,5,0,1,2,,,,0,0,,0,0,0,0,0,0,0,,,,,")
	if err != nil {
		t.Fatalf("ParseState: %v", err)
	}
	if !got.CursorVisible || !got.Wrap || got.BracketedPaste || got.ScrollTop != 0 || got.ScrollBottom != 29 || got.CursorShape != "" {
		t.Fatalf("state = %+v", got)
	}
}

func TestParseStateErrors(t *testing.T) {
	for _, line := range []string{"", "80,24", "0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,default,0", stateAlternate + ",extra"} {
		_, err := ParseState(line)
		if !errors.Is(err, ErrInvalidState) {
			t.Errorf("ParseState(%q): err = %v, want ErrInvalidState", line, err)
		}
	}
}

func TestFullPaintNormalScreen(t *testing.T) {
	state := PaneState{Width: 10, Height: 3, HistorySize: 2, CursorX: 2, CursorY: 1, CursorVisible: true, Wrap: true, ScrollBottom: 2}
	got := string(FullPaint(state, lines("old one", "old two   "), nil, lines("$ ls", "$ ", "")))
	want := "\x1b[?2026h\x1bc\x1b[3J" +
		"old one\r\nold two\x1b[0m" + // history; padding trimmed
		"\x1b[22J\x1b[H" + // push history into scrollback
		"$ ls\r\n$\r\n\x1b[0m" + // visible rows
		"\x1b[2;3H" + // cursor
		"\x1b[?2026l"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestFullPaintWithoutHistory(t *testing.T) {
	// capture-pane -S - -E -1 prints the first visible row when there is no
	// history; it must not be painted.
	state := PaneState{Width: 10, Height: 2, CursorVisible: true, Wrap: true, ScrollBottom: 1}
	got := string(FullPaint(state, lines("A"), nil, lines("A", "B")))
	want := "\x1b[?2026h\x1bc\x1b[3J" + "A\r\nB\x1b[0m" + "\x1b[1;1H" + "\x1b[?2026l"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestFullPaintAlternateScreen(t *testing.T) {
	state, err := ParseState(stateAlternate)
	if err != nil {
		t.Fatalf("ParseState: %v", err)
	}
	state.Height = 2
	state.SavedY = 1
	state.ScrollTop, state.ScrollBottom = 0, 1
	got := string(FullPaint(state, lines("h1"), lines("n1", "n2"), lines("ALT", "\x1b[7m rev ")))
	want := "\x1b[?2026h\x1bc\x1b[3J" +
		"h1\x1b[0m\x1b[22J\x1b[H" +
		"n1\r\nn2\x1b[0m" + // the normal screen under the alternate one
		"\x1b[2;1H\x1b[?1049h\x1b[H" + // normal cursor, then switch
		"ALT\r\n\x1b[7m rev \x1b[0m\x1b[0m" + // alternate rows
		"\x1b[?1h\x1b=\x1b[?1000h\x1b[?1006h\x1b[?2004h" + // modes
		"\x1b[3;4H" +
		"\x1b[?2026l"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestFullPaintModes(t *testing.T) {
	state := PaneState{
		Width: 10, Height: 5, CursorX: 1, CursorY: 3,
		Insert: true, Origin: true, MouseButton: true, MouseAll: true, MouseUTF8: true,
		ScrollTop: 2, ScrollBottom: 3, CursorShape: "bar", CursorBlink: true,
	}
	got := string(FullPaint(state, nil, nil, lines("x")))
	want := "\x1b[?2026h\x1bc\x1b[3J" + "x\x1b[0m" +
		"\x1b[4h\x1b[?7l\x1b[?1002h\x1b[?1003h\x1b[?1005h\x1b[?25l\x1b[5 q" +
		"\x1b[3;4r\x1b[?6h\x1b[2;2H" + // region, origin mode, cursor relative to the region
		"\x1b[?2026l"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestVisiblePaint(t *testing.T) {
	state := PaneState{Width: 10, Height: 3, CursorX: 4, CursorY: 2, CursorVisible: true, Wrap: true, ScrollBottom: 2}
	got := string(VisiblePaint(state, lines("one", "two", "$ ab")))
	want := "\x1b[?2026h\x1b[0m\x1b[r\x1b[H\x1b[2J" + "one\r\ntwo\r\n$ ab\x1b[0m" + "\x1b[3;5H" + "\x1b[?2026l"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestVisiblePaintKeepsRegionAndModes(t *testing.T) {
	state := PaneState{Width: 10, Height: 4, CursorY: 2, Insert: true, Origin: true, ScrollTop: 1, ScrollBottom: 2}
	got := string(VisiblePaint(state, lines("a")))
	want := "\x1b[?2026h\x1b[0m\x1b[r\x1b[?6l\x1b[4l\x1b[H\x1b[2J" + "a\x1b[0m" + "\x1b[4h" + "\x1b[2;3r\x1b[?6h\x1b[2;1H" + "\x1b[?2026l"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestWriteLinesCarriesAttributes(t *testing.T) {
	state := PaneState{Width: 20, Height: 4, CursorVisible: true, Wrap: true, ScrollBottom: 3}
	visible := lines(
		"\x1b[44mblue     ",    // background runs to the end of the row: keep the padding
		"still blue\x1b[49m  ", // back to default before the padding: trim it
		"plain   ",
		"\x1b[1mbold",
	)
	got := string(VisiblePaint(state, visible))
	want := "\x1b[?2026h\x1b[0m\x1b[r\x1b[H\x1b[2J" +
		"\x1b[44mblue     \x1b[0m\r\n" +
		"\x1b[44mstill blue\x1b[49m\r\n" +
		"plain\r\n" +
		"\x1b[1mbold\x1b[0m\x1b[0m" +
		"\x1b[1;1H\x1b[?2026l"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
