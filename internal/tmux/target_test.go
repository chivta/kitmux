package tmux

import (
	"errors"
	"testing"
)

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
		{name: "old tmux", output: "version=tmux 3.1c\nerror=old-tmux\n", wantErr: ErrOldTmux},
		{name: "new-session failed", output: "version=tmux 3.7c\nerror=new-session\ndetail=create window failed\n", wantErr: ErrSession},
		{name: "unknown state", output: "version=tmux 3.7c\nstate=weird\n", wantErr: ErrSession},
		{name: "empty output", output: "", wantErr: ErrSession},
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
	_, err := parsePrepared("version=tmux 3.1c\nerror=old-tmux\n")
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
