package kitty

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

//go:embed closetab.py
var closeTabScript string

// keySpecPattern is what a key spec printed by the close_tab script must look
// like before it goes into the overlay configuration.
var keySpecPattern = regexp.MustCompile(`^[a-z0-9_.,;'/\\\[\]=` + "`" + `-]+(\+[a-z0-9_.,;'/\\\[\]=` + "`" + `-]+)*$`)

// CloseTabKeys asks kitty which keys stay bound to close_tab once the given
// configuration files are loaded. It runs kitty's own config loader, so it
// sees defaults, kitty_mod and every override exactly as kitty does.
func CloseTabKeys(ctx context.Context, bin string, configs []string) ([]string, error) {
	args := append([]string{"+runpy", closeTabScript}, configs...)
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("list close_tab keys: %w", err)
	}
	var keys []string
	for _, line := range strings.Split(string(out), "\n") {
		key := strings.TrimSpace(line)
		if keySpecPattern.MatchString(key) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// WithCloseTab returns the keys with closeTab added, minus any key kitmux
// already binds to something else: on a clash the table's explicit binding
// (e.g. close split on cmd+w) wins.
func (k Keys) WithCloseTab(closeTab []string) Keys {
	taken := map[string]bool{
		normalizeKey(k.NewTab): true, normalizeKey(k.SplitSide): true,
		normalizeKey(k.SplitStacked): true, normalizeKey(k.CloseSplit): true,
	}
	k.CloseTab = nil
	for _, key := range closeTab {
		if !taken[normalizeKey(key)] {
			k.CloseTab = append(k.CloseTab, key)
		}
	}
	return k
}

// modifierAliases maps kitty's alternative modifier names to one spelling.
var modifierAliases = map[string]string{
	"cmd": "super", "command": "super", "opt": "alt", "option": "alt", "control": "ctrl",
}

// normalizeKey makes key specs comparable: modifiers get one spelling and a
// fixed order, so "shift+ctrl+w" equals "ctrl+shift+w" and "cmd+w" equals
// "super+w".
func normalizeKey(key string) string {
	parts := strings.Split(key, "+")
	mods := parts[:len(parts)-1]
	for i, mod := range mods {
		alias, ok := modifierAliases[mod]
		if ok {
			mods[i] = alias
		}
	}
	sort.Strings(mods)
	return strings.Join(append(mods, parts[len(parts)-1]), "+")
}
