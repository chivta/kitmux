package vt

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// StateFormat is the tmux format that prints the pane state ParseState reads:
// one line of comma-separated fields. A tmux too old to know a field prints
// it empty, which ParseState reads as zero.
const StateFormat = "#{pane_width},#{pane_height},#{history_size},#{alternate_on}," +
	"#{cursor_x},#{cursor_y},#{alternate_saved_x},#{alternate_saved_y}," +
	"#{cursor_flag},#{insert_flag},#{origin_flag},#{wrap_flag}," +
	"#{keypad_cursor_flag},#{keypad_flag}," +
	"#{mouse_standard_flag},#{mouse_button_flag},#{mouse_all_flag},#{mouse_sgr_flag},#{mouse_utf8_flag}," +
	"#{bracket_paste_flag},#{scroll_region_upper},#{scroll_region_lower}," +
	"#{cursor_shape},#{cursor_blinking}"

const (
	stateFieldSeparator = ","
	stateFieldCount     = 24
)

// tmux's names for cursor shapes (#{cursor_shape}).
const (
	cursorShapeBlock     = "block"
	cursorShapeUnderline = "underline"
	cursorShapeBar       = "bar"
)

// DECSCUSR parameters: the steady form of each shape; the blinking form is
// one less.
const (
	cursorStyleBlock     = 2
	cursorStyleUnderline = 4
	cursorStyleBar       = 6
)

// Escape sequences a repaint writes.
const (
	seqReset           = "\x1bc"   // RIS: back to power-on state
	seqClearScrollback = "\x1b[3J" // drop kitty's scrollback
	seqClearScreen     = "\x1b[H\x1b[2J"
	seqHome            = "\x1b[H"
	// seqScreenToScrollback is kitty's ED 22: move what is on screen into
	// the scrollback and clear the screen, leaving no blank lines behind.
	seqScreenToScrollback = "\x1b[22J"
	seqResetAttributes    = "\x1b[0m"
	seqNewline            = "\r\n"
	seqAlternateScreenOn  = "\x1b[?1049h"
	seqResetScrollRegion  = "\x1b[r"
	seqOriginOn           = "\x1b[?6h"
	seqOriginOff          = "\x1b[?6l"
	seqInsertOn           = "\x1b[4h"
	seqInsertOff          = "\x1b[4l"
	seqWrapOff            = "\x1b[?7l"
	seqCursorKeysApp      = "\x1b[?1h"
	seqKeypadApp          = "\x1b="
	seqMouseStandard      = "\x1b[?1000h"
	seqMouseButton        = "\x1b[?1002h"
	seqMouseAll           = "\x1b[?1003h"
	seqMouseSGR           = "\x1b[?1006h"
	seqMouseUTF8          = "\x1b[?1005h"
	seqBracketedPaste     = "\x1b[?2004h"
	seqCursorHide         = "\x1b[?25l"
	seqSyncBegin          = "\x1b[?2026h" // hold rendering until seqSyncEnd
	seqSyncEnd            = "\x1b[?2026l"
)

// ErrInvalidState is returned for a line that StateFormat did not produce.
var ErrInvalidState = errors.New("invalid pane state")

// PaneState is what tmux knows about a pane's terminal state beyond the cell
// contents. Coordinates are zero-based.
type PaneState struct {
	Width, Height int
	// HistorySize is the number of lines above the visible screen.
	HistorySize int
	// AlternateOn is true while the pane shows the alternate screen.
	AlternateOn      bool
	CursorX, CursorY int
	// SavedX and SavedY are the cursor of the normal screen while the
	// alternate screen is shown.
	SavedX, SavedY int
	CursorVisible  bool
	Insert         bool
	Origin         bool
	Wrap           bool
	CursorKeysApp  bool
	KeypadApp      bool
	MouseStandard  bool
	MouseButton    bool
	MouseAll       bool
	MouseSGR       bool
	MouseUTF8      bool
	BracketedPaste bool
	// ScrollTop and ScrollBottom are the first and last row of the scroll
	// region.
	ScrollTop, ScrollBottom int
	// CursorShape is tmux's shape name, empty or "default" for the
	// terminal's own.
	CursorShape string
	CursorBlink bool
}

// ParseState reads a line printed with StateFormat.
func ParseState(line string) (PaneState, error) {
	fields := strings.Split(line, stateFieldSeparator)
	if len(fields) != stateFieldCount {
		return PaneState{}, fmt.Errorf("%w: %d fields in %q", ErrInvalidState, len(fields), line)
	}
	numbers := make([]int, len(fields))
	for i, field := range fields {
		if field == "" {
			continue
		}
		value, err := strconv.Atoi(field)
		if err == nil {
			numbers[i] = value
		}
	}
	flag := func(i int) bool { return numbers[i] != 0 }
	state := PaneState{
		Width: numbers[0], Height: numbers[1], HistorySize: numbers[2], AlternateOn: flag(3),
		CursorX: numbers[4], CursorY: numbers[5], SavedX: numbers[6], SavedY: numbers[7],
		CursorVisible: flag(8) || fields[8] == "", Insert: flag(9), Origin: flag(10), Wrap: flag(11) || fields[11] == "",
		CursorKeysApp: flag(12), KeypadApp: flag(13),
		MouseStandard: flag(14), MouseButton: flag(15), MouseAll: flag(16), MouseSGR: flag(17), MouseUTF8: flag(18),
		BracketedPaste: flag(19), ScrollTop: numbers[20], ScrollBottom: numbers[21],
		CursorShape: fields[22], CursorBlink: flag(23),
	}
	if state.Width <= 0 || state.Height <= 0 {
		return PaneState{}, fmt.Errorf("%w: no size in %q", ErrInvalidState, line)
	}
	if fields[21] == "" {
		state.ScrollBottom = state.Height - 1
	}
	return state, nil
}

// FullPaint builds the bytes that turn a kitty window in any state into a
// copy of the pane: scrollback, screen contents, modes and cursor.
//
// history is the pane's scrollback as logical lines (capture-pane -J), so
// kitty wraps and reflows it by itself. visible is the screen the pane shows
// now, one entry per row (capture-pane -N). saved is the normal screen's rows
// while the pane is on the alternate screen (capture-pane -a), unused
// otherwise.
func FullPaint(state PaneState, history, saved, visible [][]byte) []byte {
	var b bytes.Buffer
	b.WriteString(seqSyncBegin)
	b.WriteString(seqReset)
	b.WriteString(seqClearScrollback)
	if state.HistorySize > 0 && len(history) > 0 {
		writeLines(&b, history)
		b.WriteString(seqScreenToScrollback)
		b.WriteString(seqHome)
	}
	normal := visible
	if state.AlternateOn {
		normal = saved
	}
	writeLines(&b, normal)
	if state.AlternateOn {
		writeCursor(&b, state.SavedY, state.SavedX)
		b.WriteString(seqAlternateScreenOn)
		b.WriteString(seqHome)
		writeLines(&b, visible)
	}
	writeModes(&b, state)
	writeRegionAndCursor(&b, state)
	b.WriteString(seqSyncEnd)
	return b.Bytes()
}

// VisiblePaint builds the bytes that redraw the screen of a kitty window that
// already mirrors the pane's modes: it replaces the visible rows and puts the
// cursor back. It is used after the pane changed size, when tmux and kitty
// each rewrapped the screen their own way.
func VisiblePaint(state PaneState, visible [][]byte) []byte {
	var b bytes.Buffer
	b.WriteString(seqSyncBegin)
	b.WriteString(seqResetAttributes)
	b.WriteString(seqResetScrollRegion)
	if state.Origin {
		b.WriteString(seqOriginOff)
	}
	if state.Insert {
		b.WriteString(seqInsertOff)
	}
	b.WriteString(seqClearScreen)
	writeLines(&b, visible)
	if state.Insert {
		b.WriteString(seqInsertOn)
	}
	writeRegionAndCursor(&b, state)
	b.WriteString(seqSyncEnd)
	return b.Bytes()
}

// writeLines prints captured lines separated by line breaks, leaving the
// cursor on the last one. Attributes are reset before every break and
// restored after it, because tmux carries them from line to line while a
// break with a coloured background would smear it. Trailing spaces without
// attributes are dropped: they are padding, and kitty would copy them.
func writeLines(b *bytes.Buffer, lines [][]byte) {
	var sgr SGRState
	var carried []byte
	for i, line := range lines {
		b.Write(carried)
		sgr.Feed(line)
		carried = sgr.Active()
		if len(carried) == 0 {
			line = bytes.TrimRight(line, " ")
		}
		b.Write(line)
		if len(carried) > 0 {
			b.WriteString(seqResetAttributes)
		}
		if i < len(lines)-1 {
			b.WriteString(seqNewline)
		}
	}
	// A line can end in default attributes after non-default ones without
	// tmux printing a reset; make sure nothing leaks into what follows.
	b.WriteString(seqResetAttributes)
}

// writeModes turns on the terminal modes the pane's program has set. It runs
// right after a reset, so every mode starts in its default.
func writeModes(b *bytes.Buffer, state PaneState) {
	modes := []struct {
		on  bool
		seq string
	}{
		{state.Insert, seqInsertOn},
		{!state.Wrap, seqWrapOff},
		{state.CursorKeysApp, seqCursorKeysApp},
		{state.KeypadApp, seqKeypadApp},
		{state.MouseStandard, seqMouseStandard},
		{state.MouseButton, seqMouseButton},
		{state.MouseAll, seqMouseAll},
		{state.MouseSGR, seqMouseSGR},
		{state.MouseUTF8, seqMouseUTF8},
		{state.BracketedPaste, seqBracketedPaste},
		{!state.CursorVisible, seqCursorHide},
	}
	for _, mode := range modes {
		if mode.on {
			b.WriteString(mode.seq)
		}
	}
	style := cursorStyle(state.CursorShape, state.CursorBlink)
	if style > 0 {
		fmt.Fprintf(b, "\x1b[%d q", style)
	}
}

// writeRegionAndCursor sets the scroll region and origin mode, then moves the
// cursor to its place. The order matters: setting either one homes the cursor.
func writeRegionAndCursor(b *bytes.Buffer, state PaneState) {
	fullRegion := state.ScrollTop <= 0 && state.ScrollBottom >= state.Height-1
	if !fullRegion {
		fmt.Fprintf(b, "\x1b[%d;%dr", state.ScrollTop+1, state.ScrollBottom+1)
	}
	row := state.CursorY
	if state.Origin {
		b.WriteString(seqOriginOn)
		// With origin mode on, rows count from the top of the region.
		row -= state.ScrollTop
	}
	writeCursor(b, row, state.CursorX)
}

// writeCursor moves the cursor to a zero-based row and column.
func writeCursor(b *bytes.Buffer, row, col int) {
	fmt.Fprintf(b, "\x1b[%d;%dH", max(row, 0)+1, max(col, 0)+1)
}

// cursorStyle maps tmux's cursor shape to a DECSCUSR parameter, or 0 for the
// terminal's default.
func cursorStyle(shape string, blink bool) int {
	style := 0
	switch shape {
	case cursorShapeBlock:
		style = cursorStyleBlock
	case cursorShapeUnderline:
		style = cursorStyleUnderline
	case cursorShapeBar:
		style = cursorStyleBar
	}
	if style > 0 && blink {
		style--
	}
	return style
}
