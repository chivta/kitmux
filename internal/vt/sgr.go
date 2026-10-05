package vt

import (
	"bytes"
	"strconv"
)

// Pieces of the SGR grammar ("ESC [ Ps ; Ps ... m").
const (
	sgrFinal          = 'm'
	subParamSeparator = ':' // ties sub-parameters to a parameter, e.g. "4:3"
)

// SGR parameters the state tracks.
const (
	sgrReset           = 0
	sgrBold            = 1
	sgrDim             = 2
	sgrItalic          = 3
	sgrUnderline       = 4
	sgrBlink           = 5
	sgrRapidBlink      = 6
	sgrReverse         = 7
	sgrHidden          = 8
	sgrStrike          = 9
	sgrDoubleUnderline = 21
	sgrNoBoldDim       = 22
	sgrNoItalic        = 23
	sgrNoUnderline     = 24
	sgrNoBlink         = 25
	sgrNoReverse       = 27
	sgrNoHidden        = 28
	sgrNoStrike        = 29
	sgrForegroundMin   = 30
	sgrForegroundMax   = 37
	sgrForeground      = 38 // 38, 48 and 58 are followed by their arguments
	sgrDefaultFg       = 39
	sgrBackgroundMin   = 40
	sgrBackgroundMax   = 47
	sgrBackground      = 48
	sgrDefaultBg       = 49
	sgrOverline        = 53
	sgrNoOverline      = 55
	sgrUnderlineColour = 58
	sgrDefaultUs       = 59
	sgrBrightFgMin     = 90
	sgrBrightFgMax     = 97
	sgrBrightBgMin     = 100
	sgrBrightBgMax     = 107

	sgrColourRGB     = 2 // "38;2;R;G;B"
	sgrColourIndexed = 5 // "38;5;N"
	sgrRGBArgs       = 4 // parameters after 38 that "2;R;G;B" takes
	sgrIndexedArgs   = 2 // parameters after 38 that "5;N" takes

	// sgrUnderlineOff is the sub-parameter form that turns underline off.
	sgrUnderlineOff = "4:0"
)

// csiStart is how every CSI, and so every SGR sequence, begins.
var csiStart = []byte{esc, csiIntro}

// SGRState tracks the text attributes that are active at the end of the tmux
// capture output fed to it. tmux prints attribute codes only where they
// change, and the state carries over from one captured line to the next; a
// repaint resets attributes before each line break and uses Active to restore
// them on the next line. The zero value is "no attributes".
//
// Attributes are kept per slot (each flag, each colour), so Active stays short
// however many colour changes were fed.
type SGRState struct {
	bold, dim, italic, blink, reverse, hidden, strike, overline bool
	// underline is the parameter that set the underline ("4", "21", "4:3"),
	// empty when there is none.
	underline string
	// fg, bg and us are the parameters that set the foreground, background
	// and underline colour ("31", "38;5;196", "48:2::1:2:3"), empty for the
	// default.
	fg, bg, us string
}

// Feed scans line for SGR sequences (CSI with final 'm', no private prefix, no
// intermediates) and folds them into the state. Everything else in the line
// is ignored.
func (s *SGRState) Feed(line []byte) {
	for {
		start := bytes.Index(line, csiStart)
		if start < 0 {
			return
		}
		end := start + len(csiStart)
		for end < len(line) && isSGRParamByte(line[end]) {
			end++
		}
		if end < len(line) && line[end] == sgrFinal {
			s.apply(line[start+len(csiStart) : end])
		}
		line = line[end:]
	}
}

// Active returns one SGR sequence that restores the current attributes after
// "ESC[0m", or nil when no attribute is active.
func (s *SGRState) Active() []byte {
	var params []string
	flags := []struct {
		on   bool
		code int
	}{
		{s.bold, sgrBold}, {s.dim, sgrDim}, {s.italic, sgrItalic}, {s.blink, sgrBlink},
		{s.reverse, sgrReverse}, {s.hidden, sgrHidden}, {s.strike, sgrStrike}, {s.overline, sgrOverline},
	}
	for _, flag := range flags {
		if flag.on {
			params = append(params, strconv.Itoa(flag.code))
		}
	}
	for _, param := range []string{s.underline, s.fg, s.bg, s.us} {
		if param != "" {
			params = append(params, param)
		}
	}
	if len(params) == 0 {
		return nil
	}
	out := append([]byte{}, csiStart...)
	for i, param := range params {
		if i > 0 {
			out = append(out, fieldSeparator...)
		}
		out = append(out, param...)
	}
	return append(out, sgrFinal)
}

// apply folds the parameters of one SGR sequence into the state.
func (s *SGRState) apply(params []byte) {
	fields := bytes.Split(params, fieldSeparator)
	for i := 0; i < len(fields); i++ {
		field := string(fields[i])
		colon := bytes.IndexByte(fields[i], subParamSeparator)
		if colon >= 0 {
			s.applySubParams(parseNumber(fields[i][:colon]), field)
			continue
		}
		code := parseNumber(fields[i])
		if len(fields[i]) == 0 {
			code = sgrReset
		}
		if code == sgrForeground || code == sgrBackground || code == sgrUnderlineColour {
			args := extendedColourArgs(fields[i+1:])
			if args > 0 && i+args < len(fields) {
				s.setColour(code, string(bytes.Join(fields[i:i+args+1], fieldSeparator)))
			}
			i += args
			continue
		}
		s.applyCode(code, field)
	}
}

// applySubParams handles a parameter written with colons, e.g. "4:3" or
// "38:2::255:0:0".
func (s *SGRState) applySubParams(code int, field string) {
	switch code {
	case sgrUnderline:
		s.underline = field
		if field == sgrUnderlineOff {
			s.underline = ""
		}
	case sgrForeground, sgrBackground, sgrUnderlineColour:
		s.setColour(code, field)
	}
}

// setColour stores an extended colour parameter in the slot that code (38, 48
// or 58) selects.
func (s *SGRState) setColour(code int, param string) {
	switch code {
	case sgrForeground:
		s.fg = param
	case sgrBackground:
		s.bg = param
	case sgrUnderlineColour:
		s.us = param
	}
}

// applyCode handles one plain numeric parameter; field is its text.
func (s *SGRState) applyCode(code int, field string) {
	switch {
	case code == sgrReset:
		*s = SGRState{}
	case code == sgrBold:
		s.bold = true
	case code == sgrDim:
		s.dim = true
	case code == sgrItalic:
		s.italic = true
	case code == sgrUnderline, code == sgrDoubleUnderline:
		s.underline = field
	case code == sgrBlink, code == sgrRapidBlink:
		s.blink = true
	case code == sgrReverse:
		s.reverse = true
	case code == sgrHidden:
		s.hidden = true
	case code == sgrStrike:
		s.strike = true
	case code == sgrOverline:
		s.overline = true
	case code == sgrNoBoldDim:
		s.bold, s.dim = false, false
	case code == sgrNoItalic:
		s.italic = false
	case code == sgrNoUnderline:
		s.underline = ""
	case code == sgrNoBlink:
		s.blink = false
	case code == sgrNoReverse:
		s.reverse = false
	case code == sgrNoHidden:
		s.hidden = false
	case code == sgrNoStrike:
		s.strike = false
	case code == sgrNoOverline:
		s.overline = false
	case code >= sgrForegroundMin && code <= sgrForegroundMax, code >= sgrBrightFgMin && code <= sgrBrightFgMax:
		s.fg = field
	case code == sgrDefaultFg:
		s.fg = ""
	case code >= sgrBackgroundMin && code <= sgrBackgroundMax, code >= sgrBrightBgMin && code <= sgrBrightBgMax:
		s.bg = field
	case code == sgrDefaultBg:
		s.bg = ""
	case code == sgrDefaultUs:
		s.us = ""
	}
}

// extendedColourArgs returns how many of the parameters that follow a 38, 48 or
// 58 are its arguments, so that a colour value of 0 is not read as a reset.
func extendedColourArgs(rest [][]byte) int {
	if len(rest) == 0 {
		return 0
	}
	switch parseNumber(rest[0]) {
	case sgrColourIndexed:
		return sgrIndexedArgs
	case sgrColourRGB:
		return sgrRGBArgs
	}
	return 0
}

// isSGRParamByte reports whether b can appear between "ESC[" and the 'm' of an
// SGR sequence: a digit, ':' or ';'. A private prefix or an intermediate byte
// makes the sequence something else.
func isSGRParamByte(b byte) bool {
	return b >= parameterMin && b < privateMin
}
