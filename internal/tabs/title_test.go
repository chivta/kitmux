package tabs

import "testing"

func TestTitle(t *testing.T) {
	cases := []struct {
		name   string
		target string
		index  int
		window string
		want   string
	}{
		{"local", "local", 0, "zsh", "tmux-0 zsh"},
		{"local empty name", "local", 3, "", "tmux-3"},
		{"local name of only stripped characters", "local", 3, "\x1b\u200b ", "tmux-3"},
		{"remote host", "build1", 2, "logs", "build1:tmux-2 logs"},
		{"remote with user", "artem@build1.example.com", 12, "vim", "build1.example.com:tmux-12 vim"},
		{"remote empty name", "artem@build1", 1, "", "build1:tmux-1"},
		{"name with spaces kept", "local", 1, "my project", "tmux-1 my project"},
		{"control characters stripped", "local", 1, "a\x07b\x1b[31mc\r\n", "tmux-1 ab[31mc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Title(tc.target, tc.index, tc.window)
			if got != tc.want {
				t.Fatalf("Title(%q, %d, %q) = %q, want %q", tc.target, tc.index, tc.window, got, tc.want)
			}
		})
	}
}

func TestHost(t *testing.T) {
	cases := []struct {
		target string
		want   string
	}{
		{"host", "host"},
		{"user@host", "host"},
		{"user@10.0.0.5", "10.0.0.5"},
		{"odd@user@host", "host"},
		{"alias", "alias"},
	}
	for _, tc := range cases {
		got := Host(tc.target)
		if got != tc.want {
			t.Errorf("Host(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestSanitize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "editor", "editor"},
		{"unicode kept", "логи ✓ 日本", "логи ✓ 日本"},
		{"c0 controls", "a\x00b\tc\x7fd", "abcd"},
		{"c1 controls", "a\u0085b\u009bc", "abc"},
		{"bidi overrides", "safe\u202eevil\u202c", "safeevil"},
		{"bidi isolates and marks", "\u2066a\u2069\u200eb\u200f\u061cc", "abc"},
		{"zero width", "a\u200bb\u200cc\u200dd\u2060e\ufefff", "abcdef"},
		{"line separators", "a\u2028b\u2029c", "abc"},
		{"soft hyphen", "co\u00adop", "coop"},
		{"invalid utf8", "a\xffb\xc3", "ab"},
		{"trimmed", "  padded  ", "padded"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Sanitize(tc.in)
			if got != tc.want {
				t.Fatalf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
