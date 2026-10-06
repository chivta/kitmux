package kitty

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed overlay.conf.tmpl
var overlayTemplate string

// Keys are the key combinations, in kitty.conf syntax, that kitmux binds
// inside its tabs.
type Keys struct {
	NewTab       string
	SplitSide    string
	SplitStacked string
	CloseSplit   string
	// CloseTab are the keys bound to close_tab after the user's
	// configuration was applied; there may be none.
	CloseTab []string
}

// DefaultKeys returns the bindings for an operating system (runtime.GOOS):
// the cmd-based ones on macOS, the ctrl+shift-based ones everywhere else.
func DefaultKeys(goos string) Keys {
	if goos == "darwin" {
		return Keys{NewTab: "cmd+t", SplitSide: "cmd+d", SplitStacked: "cmd+shift+d", CloseSplit: "cmd+w"}
	}
	return Keys{NewTab: "ctrl+shift+t", SplitSide: "ctrl+shift+d", SplitStacked: "ctrl+alt+d", CloseSplit: "ctrl+shift+w"}
}

// overlayData is what the overlay template is rendered with.
type overlayData struct {
	TargetVar string
	Bin       string
	Socket    string
	Keys      Keys
}

// RenderOverlay returns the overlay configuration. bin is the kitmux
// executable the bindings run and socket the daemon's socket they talk to.
func RenderOverlay(bin, socket string, keys Keys) (string, error) {
	tmpl, err := template.New("overlay").Parse(overlayTemplate)
	if err != nil {
		return "", fmt.Errorf("parse overlay template: %w", err)
	}
	var out bytes.Buffer
	err = tmpl.Execute(&out, overlayData{TargetVar: VarTarget, Bin: QuoteArg(bin), Socket: QuoteArg(socket), Keys: keys})
	if err != nil {
		return "", fmt.Errorf("render overlay: %w", err)
	}
	return out.String(), nil
}

// RenderSession returns a kitty session file that opens one tab in the splits
// layout running command, with the given tab title and user variables
// (NAME=VALUE). kitmux starts kitty with it so the first window is already a
// pane and no stray shell appears.
func RenderSession(title string, vars, command []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "new_tab %s\n", title)
	fmt.Fprintf(&b, "layout %s\n", LayoutSplits)
	b.WriteString("launch")
	for _, v := range vars {
		b.WriteString(" --var ")
		b.WriteString(QuoteArg(v))
	}
	for _, arg := range command {
		b.WriteByte(' ')
		b.WriteString(QuoteArg(arg))
	}
	b.WriteByte('\n')
	return b.String()
}

// QuoteArg quotes one argument for kitty's shell-like parsing of launch
// command lines in kitty.conf and session files.
func QuoteArg(arg string) string {
	safe := arg != ""
	for _, r := range arg {
		plain := r == '/' || r == '.' || r == '_' || r == '-' || r == '=' || r == '%' || r == '@' || r == ':' || r == '+' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !plain {
			safe = false
			break
		}
	}
	if safe {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
