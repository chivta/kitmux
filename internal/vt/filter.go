// Package vt works on the raw terminal byte streams that kitmux moves from
// tmux panes into kitty windows: Filter rewrites live pane output, SGRState
// tracks text attributes across the lines of a captured pane.
package vt

import "bytes"

// Control bytes the parser acts on. Every other C0 control is forwarded as
// data.
const (
	bel = 0x07 // ends an OSC
	esc = 0x1b // starts every sequence
)

// Bytes that follow ESC and decide what kind of sequence it starts.
const (
	csiIntro   = '['
	oscIntro   = ']'
	dcsIntro   = 'P'
	apcIntro   = '_'
	pmIntro    = '^'
	sosIntro   = 'X'
	stFinal    = '\\' // ESC \ is ST, the string terminator
	decidFinal = 'Z'  // ESC Z is DECID, an identify query
)

// introLen is the length of "ESC [", "ESC ]" and "ESC P": the bytes ahead of a
// sequence's content.
const introLen = 2

// Byte ranges of the ECMA-48 sequence grammar.
const (
	intermediateMin = 0x20 // intermediate bytes: 0x20-0x2f
	parameterMin    = 0x30 // parameter bytes: 0x30-0x3f
	privateMin      = 0x3c // '<' '=' '>' '?': a private prefix when first
	csiFinalMin     = 0x40 // CSI final bytes: 0x40-0x7e
	csiFinalMax     = 0x7e
)

// Buffering limits.
const (
	// maxCSILen is the most bytes of a CSI, or of an ESC + intermediates
	// sequence, held while waiting for the final byte. Anything longer is
	// not a real sequence and is forwarded unchanged.
	maxCSILen = 256
	// maxStringLen is the most bytes of an OSC or DCS held while it may
	// still turn out to be a query. A longer string is forwarded and can no
	// longer be dropped.
	maxStringLen = 1 << 20
)

// Final bytes of the CSI sequences that can be queries.
const (
	finalDA            = 'c' // device attributes
	finalDSR           = 'n' // device status report
	finalDECRQM        = 'p' // request mode, with intermediate '$'
	finalXTVERSION     = 'q' // terminal version, with prefix '>'
	finalWindowOp      = 't' // XTWINOPS, a query for some operations
	finalKittyKeyboard = 'u' // kitty keyboard flags, a query as "CSI ? u"
	finalDECREQTPARM   = 'x' // request terminal parameters
)

// Private prefixes and intermediates that tell a query from its look-alikes.
const (
	noPrefix       = 0 // the CSI has no private prefix
	prefixQuestion = '?'
	prefixGreater  = '>'
	interDollar    = '$'
)

// XTWINOPS operations ("CSI Ps t") that make the terminal send a report.
const (
	winopReportState        = 11
	winopReportPosition     = 13
	winopReportPixels       = 14
	winopReportScreenPixels = 15
	winopReportCellPixels   = 16
	winopReportChars        = 18
	winopReportScreenChars  = 19
	winopReportIconLabel    = 20
	winopReportTitle        = 21
)

// OSC numbers whose value a program can ask for instead of set.
const (
	oscPalette          = 4
	oscSpecialColour    = 5
	oscDynamicColourMin = 10 // 10-19: foreground, background, cursor, ...
	oscDynamicColourMax = 19
	oscKittyColours     = 21 // kitty colour protocol, asked with "key=?"
	oscClipboard        = 52
)

// oscQueryField is the OSC field that asks for a value instead of setting it.
const oscQueryField = "?"

// Terminators of string sequences, forwarded behind a string that is kept.
const (
	terminatorST   = "\x1b\\"
	terminatorBEL  = "\a"
	terminatorNone = "" // the string was cut short by another sequence
)

// Limits of parseNumber.
const (
	// notNumber is what parseNumber returns for a field that is not a plain
	// decimal number.
	notNumber = -1
	// maxNumber is the largest value parseNumber reads; no sequence the
	// package looks at has a larger parameter.
	maxNumber = 65535
)

var (
	// fieldSeparator splits OSC fields and CSI parameters.
	fieldSeparator = []byte{';'}
	// kittyQuerySuffix ends an OSC 21 field that asks for a colour.
	kittyQuerySuffix = []byte("=?")

	// Starts of DCS content that decide what happens to the string.
	xtgettcapPrefix   = []byte("+q")    // XTGETTCAP request: dropped
	decrqssPrefix     = []byte("$q")    // DECRQSS request: dropped
	passthroughPrefix = []byte("tmux;") // tmux passthrough: unwrapped
)

// state is the Filter's position in the escape-sequence grammar.
type state int

const (
	stateGround         state = iota // plain data; the zero value
	stateEscape                      // after ESC
	stateEscapeInter                 // after ESC and intermediate bytes
	stateCSI                         // inside a CSI
	stateString                      // inside a buffered OSC or DCS
	stateStringEsc                   // ESC seen inside a buffered string
	stateStream                      // inside a string forwarded as it arrives
	stateStreamEsc                   // ESC seen inside a streamed string
	statePassthrough                 // inside a tmux passthrough payload
	statePassthroughEsc              // ESC seen inside a passthrough payload
)

// dcsClass is what a DCS turns out to be, judged by the start of its content.
type dcsClass int

const (
	dcsUndecided   dcsClass = iota // too short to tell
	dcsPlain                       // forwarded unchanged
	dcsQuery                       // XTGETTCAP or DECRQSS: dropped
	dcsPassthrough                 // tmux passthrough wrapper: unwrapped
)

// Filter rewrites one pane's raw output stream for display in kitty. It drops
// the terminal queries tmux has already answered, so that kitty does not answer
// them a second time, and unwraps tmux passthrough so that kitty gets the
// payload. It is a streaming state machine: sequences split across Write calls
// are handled, and bytes of an incomplete sequence are held until it completes.
// One Filter per pane; not safe for concurrent use. The zero value is ready to
// use.
type Filter struct {
	state state
	// kind is the introducer of the string being read (oscIntro, dcsIntro,
	// apcIntro, pmIntro or sosIntro); only an OSC ends at BEL.
	kind byte
	// buf holds the start of an incomplete sequence until the Filter knows
	// whether to forward or drop it.
	buf []byte
}

// Write consumes the next chunk and returns the bytes to forward to kitty.
// The returned slice is owned by the caller (never aliases p or internal
// buffers).
func (f *Filter) Write(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for _, b := range p {
		out = f.step(out, b)
	}
	return out
}

// Reset drops any partially buffered sequence and returns to the ground state.
// Used when the pane is repainted from scratch.
func (f *Filter) Reset() {
	f.drop()
}

// step feeds one byte to the state machine and appends to out whatever can now
// be forwarded.
func (f *Filter) step(out []byte, b byte) []byte {
	switch f.state {
	case stateEscape:
		return f.stepEscape(out, b)
	case stateEscapeInter:
		return f.stepEscapeInter(out, b)
	case stateCSI:
		return f.stepCSI(out, b)
	case stateString:
		return f.stepString(out, b)
	case stateStringEsc:
		if b == stFinal {
			return f.endString(out, terminatorST)
		}
		out = f.endString(out, terminatorNone)
		f.holdEscape()
		return f.step(out, b)
	case stateStream:
		return f.stepStream(out, b)
	case stateStreamEsc:
		if b == stFinal {
			f.state = stateGround
			return append(out, esc, b)
		}
		f.holdEscape()
		return f.step(out, b)
	case statePassthrough:
		if b == esc {
			f.state = statePassthroughEsc
			return out
		}
		return append(out, b)
	case statePassthroughEsc:
		if b == stFinal {
			f.state = stateGround
			return out
		}
		// ESC ESC stands for one payload ESC. tmux turns any other ESC x
		// into x the same way, so the payload matches what tmux sends on.
		f.state = statePassthrough
		return append(out, b)
	}
	if b == esc {
		f.holdEscape()
		return out
	}
	return append(out, b)
}

// stepEscape handles the byte after ESC, which decides the kind of sequence.
func (f *Filter) stepEscape(out []byte, b byte) []byte {
	switch {
	case b == esc:
		// The first ESC stands alone; parsing starts over at this one.
		out = f.flush(out)
		f.holdEscape()
	case b == csiIntro:
		f.buf = append(f.buf, b)
		f.state = stateCSI
	case b == oscIntro || b == dcsIntro:
		f.buf = append(f.buf, b)
		f.kind = b
		f.state = stateString
	case b == apcIntro || b == pmIntro || b == sosIntro:
		// Never a query, so there is nothing to hold it back for.
		f.buf = append(f.buf, b)
		out = f.flush(out)
		f.kind = b
		f.state = stateStream
	case b == decidFinal:
		f.drop()
	case isIntermediate(b):
		f.buf = append(f.buf, b)
		f.state = stateEscapeInter
	default:
		out = f.forward(out, b)
	}
	return out
}

// stepEscapeInter handles a byte after ESC and at least one intermediate byte
// (e.g. "ESC ( B"). None of these sequences is a query, so the first byte that
// is not an intermediate ends the sequence and everything is forwarded.
func (f *Filter) stepEscapeInter(out []byte, b byte) []byte {
	switch {
	case b == esc:
		out = f.flush(out)
		f.holdEscape()
	case isIntermediate(b) && len(f.buf) < maxCSILen:
		f.buf = append(f.buf, b)
	default:
		out = f.forward(out, b)
	}
	return out
}

// stepCSI handles a byte inside a CSI. The final byte decides whether the
// sequence is forwarded or dropped.
func (f *Filter) stepCSI(out []byte, b byte) []byte {
	switch {
	case b == esc:
		// The partial CSI is abandoned; parsing starts over at this ESC.
		out = f.flush(out)
		f.holdEscape()
	case b >= csiFinalMin && b <= csiFinalMax:
		if isQueryCSI(f.buf[introLen:], b) {
			f.drop()
		} else {
			out = f.forward(out, b)
		}
	case b >= intermediateMin && b < csiFinalMin && len(f.buf) < maxCSILen:
		f.buf = append(f.buf, b)
	default:
		// CAN, SUB or another byte no CSI contains, or the CSI grew past
		// maxCSILen: this is not a sequence, forward it as data.
		out = f.forward(out, b)
	}
	return out
}

// stepString handles a byte inside an OSC or DCS that is held back because it
// may be a query. A DCS leaves this state as soon as its first bytes show that
// it is a passthrough wrapper or cannot be a query.
func (f *Filter) stepString(out []byte, b byte) []byte {
	if b == esc {
		f.state = stateStringEsc
		return out
	}
	if b == bel && f.kind == oscIntro {
		return f.endString(out, terminatorBEL)
	}
	f.buf = append(f.buf, b)
	if f.kind == dcsIntro {
		switch classifyDCS(f.buf[introLen:]) {
		case dcsPassthrough:
			// The wrapper is dropped; the payload is streamed.
			f.buf = f.buf[:0]
			f.state = statePassthrough
			return out
		case dcsPlain:
			f.state = stateStream
			return f.flush(out)
		}
	}
	if len(f.buf) > maxStringLen {
		f.state = stateStream
		return f.flush(out)
	}
	return out
}

// stepStream handles a byte inside a string that is forwarded as it arrives.
// Only an ESC is held, until the next byte shows whether it is the terminator.
func (f *Filter) stepStream(out []byte, b byte) []byte {
	if b == esc {
		f.state = stateStreamEsc
		return out
	}
	if b == bel && f.kind == oscIntro {
		f.state = stateGround
	}
	return append(out, b)
}

// endString finishes a buffered string and returns to the ground state: a
// query is dropped together with its terminator, anything else is forwarded
// unchanged with term behind it.
func (f *Filter) endString(out []byte, term string) []byte {
	content := f.buf[introLen:]
	query := false
	switch f.kind {
	case oscIntro:
		query = isQueryOSC(content)
	case dcsIntro:
		query = classifyDCS(content) == dcsQuery
	}
	if query {
		f.drop()
		return out
	}
	out = f.flush(out)
	f.state = stateGround
	return append(out, term...)
}

// holdEscape starts a new sequence at an ESC that was just read. The caller
// has already dealt with whatever was held before it.
func (f *Filter) holdEscape() {
	f.buf = append(f.buf[:0], esc)
	f.state = stateEscape
}

// flush forwards the held bytes unchanged and empties the buffer.
func (f *Filter) flush(out []byte) []byte {
	out = append(out, f.buf...)
	f.buf = f.buf[:0]
	return out
}

// forward ends the held sequence with b, forwards all of it unchanged and
// returns to the ground state.
func (f *Filter) forward(out []byte, b byte) []byte {
	out = f.flush(out)
	f.state = stateGround
	return append(out, b)
}

// drop discards the held sequence and returns to the ground state.
func (f *Filter) drop() {
	f.buf = f.buf[:0]
	f.state = stateGround
}

// isIntermediate reports whether b is an ECMA-48 intermediate byte.
func isIntermediate(b byte) bool {
	return b >= intermediateMin && b < parameterMin
}

// isQueryCSI reports whether a CSI is a query that tmux has already answered.
// body is what lies between "ESC [" and the final byte.
func isQueryCSI(body []byte, final byte) bool {
	split := 0
	for split < len(body) && body[split] >= parameterMin {
		split++
	}
	params, inter := body[:split], body[split:]
	prefix := byte(noPrefix)
	if len(params) > 0 && params[0] >= privateMin {
		prefix, params = params[0], params[1:]
	}
	noInter := len(inter) == 0
	switch final {
	case finalDA:
		return true
	case finalDSR:
		return noInter && (prefix == noPrefix || prefix == prefixQuestion)
	case finalDECRQM:
		return len(inter) == 1 && inter[0] == interDollar
	case finalXTVERSION:
		return noInter && prefix == prefixGreater
	case finalKittyKeyboard:
		return noInter && prefix == prefixQuestion && len(params) == 0
	case finalWindowOp:
		return noInter && prefix == noPrefix && isWindowReport(firstParam(params))
	case finalDECREQTPARM:
		return noInter && prefix == noPrefix
	}
	return false
}

// firstParam returns the first ';'-separated parameter of a CSI as a number,
// or notNumber.
func firstParam(params []byte) int {
	first, _, _ := bytes.Cut(params, fieldSeparator)
	return parseNumber(first)
}

// isWindowReport reports whether op, the first parameter of "CSI ... t", makes
// the terminal send a report.
func isWindowReport(op int) bool {
	switch op {
	case winopReportState, winopReportPosition, winopReportPixels,
		winopReportScreenPixels, winopReportCellPixels, winopReportChars,
		winopReportScreenChars, winopReportIconLabel, winopReportTitle:
		return true
	}
	return false
}

// isQueryOSC reports whether an OSC asks for a colour or for the clipboard
// instead of setting it. content is what lies between "ESC ]" and the
// terminator.
func isQueryOSC(content []byte) bool {
	number, rest, more := bytes.Cut(content, fieldSeparator)
	code := parseNumber(number)
	if !isQueryableOSC(code) {
		return false
	}
	for more {
		var field []byte
		field, rest, more = bytes.Cut(rest, fieldSeparator)
		if code == oscKittyColours && bytes.HasSuffix(field, kittyQuerySuffix) {
			return true
		}
		if code != oscKittyColours && string(field) == oscQueryField {
			return true
		}
	}
	return false
}

// isQueryableOSC reports whether code is an OSC number that has a query form.
func isQueryableOSC(code int) bool {
	switch {
	case code == oscPalette, code == oscSpecialColour, code == oscKittyColours, code == oscClipboard:
		return true
	case code >= oscDynamicColourMin && code <= oscDynamicColourMax:
		return true
	}
	return false
}

// classifyDCS decides what a DCS is from the content read so far (what follows
// "ESC P"). It answers dcsUndecided while the content is still a proper prefix
// of one of the deciding starts.
func classifyDCS(content []byte) dcsClass {
	if bytes.HasPrefix(content, xtgettcapPrefix) || bytes.HasPrefix(content, decrqssPrefix) {
		return dcsQuery
	}
	if bytes.HasPrefix(content, passthroughPrefix) {
		return dcsPassthrough
	}
	for _, prefix := range [][]byte{xtgettcapPrefix, decrqssPrefix, passthroughPrefix} {
		if bytes.HasPrefix(prefix, content) {
			return dcsUndecided
		}
	}
	return dcsPlain
}

// parseNumber returns the value of field read as a decimal number, or
// notNumber when field is empty, has a byte that is not a digit, or is larger
// than maxNumber.
func parseNumber(field []byte) int {
	if len(field) == 0 {
		return notNumber
	}
	n := 0
	for _, b := range field {
		if b < '0' || b > '9' {
			return notNumber
		}
		n = n*10 + int(b-'0')
		if n > maxNumber {
			return notNumber
		}
	}
	return n
}
