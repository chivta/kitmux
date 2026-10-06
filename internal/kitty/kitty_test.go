package kitty

import (
	"encoding/json"
	"strings"
	"testing"
)

// lsSample is a trimmed `kitty @ ls` reply from kitty 0.48.2 for one tab with
// three windows: 1 beside (2 above 3).
const lsSample = `[{"id":1,"is_focused":false,"tabs":[{"id":1,"title":"tmux-0 zsh","is_active":true,"layout":"splits",
"layout_state":{"all_windows":{"active_group_idx":2},"class":"Splits","opts":{"split_axis":"horizontal"},
"pairs":{"one":1,"two":{"bias":0.7,"horizontal":false,"one":2,"two":3}}},
"groups":[{"id":1,"windows":[1]},{"id":2,"windows":[2,9]},{"id":3,"windows":[3]}],
"windows":[{"id":1,"columns":94,"lines":61,"user_vars":{}},
{"id":2,"columns":94,"lines":42,"is_focused":true,"user_vars":{"kitmux_pane":"%10","kitmux_target":"local","kitmux_window":"@0"}},
{"id":3,"columns":94,"lines":18,"user_vars":{"kitmux_pane":"%1"}}]}]}]`

func TestDecodeList(t *testing.T) {
	var windows []OSWindow
	err := json.Unmarshal([]byte(lsSample), &windows)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	tab := windows[0].Tabs[0]
	if tab.Layout != LayoutSplits || tab.Title != "tmux-0 zsh" || !tab.IsActive {
		t.Fatalf("tab = %+v", tab)
	}
	if len(tab.Groups) != 3 || tab.Groups[1].ID != 2 || len(tab.Groups[1].Windows) != 2 {
		t.Fatalf("groups = %+v", tab.Groups)
	}
	second := tab.Windows[1]
	if second.Columns != 94 || second.Lines != 42 || !second.IsFocused || second.UserVars[VarPane] != "%10" || second.UserVars[VarTarget] != "local" {
		t.Fatalf("window = %+v", second)
	}

	pairs := tab.LayoutState.Pairs
	if pairs == nil || !pairs.Horizontal {
		t.Fatalf("root pair = %+v", pairs)
	}
	if pairs.One == nil || pairs.One.Pair != nil || pairs.One.Group != 1 {
		t.Fatalf("root.one = %+v", pairs.One)
	}
	nested := pairs.Two.Pair
	if nested == nil || nested.Horizontal || nested.One.Group != 2 || nested.Two.Group != 3 {
		t.Fatalf("root.two = %+v", pairs.Two)
	}
}

func TestDecodePairShapes(t *testing.T) {
	cases := []struct {
		name string
		json string
		ok   func(*Pair) bool
	}{
		{"single window", `{"one":5}`, func(p *Pair) bool { return p.Horizontal && p.One.Group == 5 && p.Two == nil }},
		{"empty", `{}`, func(p *Pair) bool { return p.One == nil && p.Two == nil }},
		{"explicit horizontal", `{"horizontal":true,"one":1,"two":2}`, func(p *Pair) bool { return p.Horizontal && p.Two.Group == 2 }},
		{"only two", `{"horizontal":false,"two":{"one":4}}`, func(p *Pair) bool {
			return !p.Horizontal && p.One == nil && p.Two.Pair.One.Group == 4
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var pair Pair
			err := json.Unmarshal([]byte(tc.json), &pair)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !tc.ok(&pair) {
				t.Fatalf("pair = %+v", pair)
			}
		})
	}
	var pair Pair
	err := json.Unmarshal([]byte(`{"one":"x"}`), &pair)
	if err == nil {
		t.Fatal("a string child must not decode")
	}
}

func TestQuoteArg(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/usr/local/bin/kitmux", "/usr/local/bin/kitmux"},
		{"kitmux_pane=%12", "kitmux_pane=%12"},
		{"kitmux_window=@3", "kitmux_window=@3"},
		{"user@host.example", "user@host.example"},
		{"/Users/me/My Apps/kitmux", "'/Users/me/My Apps/kitmux'"},
		{"it's", `'it'\''s'`},
		{"a$b", "'a$b'"},
		{"", "''"},
	}
	for _, tc := range cases {
		got := QuoteArg(tc.in)
		if got != tc.want {
			t.Errorf("QuoteArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderSession(t *testing.T) {
	got := RenderSession("tmux-0 zsh", []string{"kitmux_target=local", "kitmux_pane=%0"}, []string{"/opt/kit mux/kitmux", "_pane", "/run/d.sock", "0", "%0"})
	want := "new_tab tmux-0 zsh\nlayout splits\nlaunch --var kitmux_target=local --var kitmux_pane=%0 '/opt/kit mux/kitmux' _pane /run/d.sock 0 %0\n"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestRenderOverlay(t *testing.T) {
	keys := DefaultKeys("darwin")
	keys.CloseTab = []string{"super+w", "ctrl+shift+q"}
	got, err := RenderOverlay("/opt/kit mux/kitmux", "/run/d.sock", keys)
	if err != nil {
		t.Fatalf("RenderOverlay: %v", err)
	}
	for _, want := range []string{
		"confirm_os_window_close 0\n",
		"macos_quit_when_last_window_closed yes\n",
		"map --when-focus-on var:kitmux_target --allow-fallback=shifted,ascii cmd+t launch --type=background -- '/opt/kit mux/kitmux' _newtab /run/d.sock @active-kitty-window-id\n",
		"cmd+d launch --type=background -- '/opt/kit mux/kitmux' _split /run/d.sock side @active-kitty-window-id\n",
		"cmd+shift+d launch --type=background -- '/opt/kit mux/kitmux' _split /run/d.sock stacked @active-kitty-window-id\n",
		"cmd+w launch --type=overlay --title kitmux -- '/opt/kit mux/kitmux' _confirm /run/d.sock pane @active-kitty-window-id\n",
		"super+w launch --type=overlay --title kitmux -- '/opt/kit mux/kitmux' _confirm /run/d.sock window @active-kitty-window-id\n",
		"ctrl+shift+q launch --type=overlay --title kitmux -- '/opt/kit mux/kitmux' _confirm /run/d.sock window @active-kitty-window-id\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("overlay lacks %q\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "map ") && !strings.HasPrefix(line, "map --when-focus-on var:kitmux_target ") {
			t.Errorf("unguarded mapping: %s", line)
		}
	}
}

func TestRenderOverlayWithoutCloseTab(t *testing.T) {
	got, err := RenderOverlay("/bin/kitmux", "/run/d.sock", DefaultKeys("linux"))
	if err != nil {
		t.Fatalf("RenderOverlay: %v", err)
	}
	if strings.Contains(got, "_confirm /run/d.sock window") {
		t.Fatalf("no close-tab binding expected:\n%s", got)
	}
	for _, want := range []string{"ctrl+shift+t launch", "ctrl+shift+d launch", "ctrl+alt+d launch", "ctrl+shift+w launch"} {
		if !strings.Contains(got, want) {
			t.Errorf("overlay lacks %q", want)
		}
	}
}
