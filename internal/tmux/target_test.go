package tmux

import (
	"errors"
	"testing"
)

func TestVersionSupported(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"tmux 3.7c", true},
		{"tmux 3.2", true},
		{"tmux 3.2a", true},
		{"tmux 3.1c", false},
		{"tmux 2.9", false},
		{"tmux 4.0", true},
		{"tmux next-3.6", true},
		{"tmux next-3.1", false},
		{"tmux master", true},
		{"tmux openbsd-7.5", true},
		{"", false},
	}
	for _, tc := range cases {
		got := versionSupported(tc.version)
		if got != tc.want {
			t.Errorf("versionSupported(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

func TestParsePrepared(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		want    Prepared
		wantErr error
	}{
		{
			name:   "ready with continuum",
			output: "version=tmux 3.7c\ncontinuum=/home/kit/.tmux/plugins/tmux-continuum/scripts/continuum_save.sh\nstate=ready\n",
			want:   Prepared{Version: "tmux 3.7c", State: StateReady, ContinuumSave: "/home/kit/.tmux/plugins/tmux-continuum/scripts/continuum_save.sh"},
		},
		{
			name:   "restored without continuum",
			output: "version=tmux 3.4\nstate=restored\n",
			want:   Prepared{Version: "tmux 3.4", State: StateRestored},
		},
		{
			name:   "gone",
			output: "version=tmux 3.7c\nstate=gone\n",
			want:   Prepared{Version: "tmux 3.7c", State: StateGone},
		},
		{
			name:   "path with an equals sign",
			output: "version=tmux 3.7c\ncontinuum=/opt/a=b/continuum_save.sh\nstate=created\n",
			want:   Prepared{Version: "tmux 3.7c", State: StateCreated, ContinuumSave: "/opt/a=b/continuum_save.sh"},
		},
		{name: "no tmux", output: "error=no-tmux\n", wantErr: ErrNoTmux},
		{name: "old tmux", output: "version=tmux 3.1c\nstate=ready\n", wantErr: ErrOldTmux},
		{name: "new-session failed", output: "version=tmux 3.7c\nerror=new-session\ndetail=create window failed\n", wantErr: ErrSession},
		{name: "unknown state", output: "version=tmux 3.7c\nstate=weird\n", wantErr: ErrSession},
		{name: "empty output", output: "", wantErr: ErrOldTmux},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePrepared(tc.output)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestErrorDetail(t *testing.T) {
	_, err := parsePrepared("version=tmux 3.1c\nstate=ready\n")
	var detailed *Error
	if !errors.As(err, &detailed) || detailed.Detail != "tmux 3.1c" {
		t.Fatalf("err = %#v", err)
	}
}

func TestLimitedBuffer(t *testing.T) {
	b := &limitedBuffer{limit: 5}
	n, err := b.Write([]byte("abc"))
	if n != 3 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	n, err = b.Write([]byte("defgh"))
	if n != 5 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	_, _ = b.Write([]byte("ijk"))
	if b.String() != "abcde" {
		t.Fatalf("String() = %q", b.String())
	}
}
