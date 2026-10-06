package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadValidation(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		session string
		targets []string
		code    Code
	}{
		{name: "defaults", args: nil, session: "main", targets: []string{"local"}},
		{name: "session and targets", args: []string{"-s", "work.1", "local", "me@box"}, session: "work.1", targets: []string{"local", "me@box"}},
		{name: "alias target", args: []string{"build-host"}, session: "main", targets: []string{"build-host"}},
		{name: "session too long", args: []string{"-s", strings.Repeat("a", 33)}, code: CodeBadSession},
		{name: "session with a space", args: []string{"-s", "a b"}, code: CodeBadSession},
		{name: "session with a quote", args: []string{"-s", "a'b"}, code: CodeBadSession},
		{name: "session dot", args: []string{"-s", "."}, code: CodeBadSession},
		{name: "session dotdot", args: []string{"-s", ".."}, code: CodeBadSession},
		{name: "empty session", args: []string{"-s", ""}, code: CodeBadSession},
		{name: "unknown flag", args: []string{"-x"}, code: CodeUsage},
		{name: "target like an option after a target", args: []string{"local", "-oProxyCommand=x"}, code: CodeBadTarget},
		{name: "target like an option after --", args: []string{"--", "-oProxyCommand=x"}, code: CodeBadTarget},
		{name: "target with a space", args: []string{"bad host"}, code: CodeBadTarget},
		{name: "target with a control character", args: []string{"bad\x1bhost"}, code: CodeBadTarget},
		{name: "empty target", args: []string{""}, code: CodeBadTarget},
		{name: "duplicate target", args: []string{"local", "local"}, code: CodeDuplicateTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(tc.args)
			if tc.code != "" {
				var coded *Error
				if !errors.As(err, &coded) || coded.Code != tc.code {
					t.Fatalf("err = %v, want code %s", err, tc.code)
				}
				if !IsUsage(err) {
					t.Fatalf("IsUsage(%v) = false", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Session != tc.session || strings.Join(cfg.Targets, ",") != strings.Join(tc.targets, ",") {
				t.Fatalf("cfg = %+v", cfg)
			}
		})
	}
}

func TestLoadEnvironment(t *testing.T) {
	t.Setenv(EnvSSH, "  ssh   -F /tmp/ssh_config ")
	t.Setenv("LANG", "C; rm -rf /")
	t.Setenv("XDG_STATE_HOME", "/state")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", "/tmpdir")
	t.Setenv("KITTY_CONFIG_DIRECTORY", "/kittyconf")
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Join(cfg.SSH, "|") != "ssh|-F|/tmp/ssh_config" {
		t.Errorf("SSH = %q", cfg.SSH)
	}
	if cfg.Lang != defaultLang {
		t.Errorf("Lang = %q, want the fallback", cfg.Lang)
	}
	if cfg.LogFile != "/state/kitmux/kitmux.log" {
		t.Errorf("LogFile = %q", cfg.LogFile)
	}
	if strings.Join(cfg.RuntimeBases, "|") != "/tmpdir|/tmp" {
		t.Errorf("RuntimeBases = %q", cfg.RuntimeBases)
	}
	if cfg.KittyConf != "/kittyconf/kitty.conf" {
		t.Errorf("KittyConf = %q", cfg.KittyConf)
	}

	t.Setenv(EnvSSH, "")
	t.Setenv("LANG", "uk_UA.UTF-8")
	cfg, err = Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Join(cfg.SSH, "|") != "ssh" || cfg.Lang != "uk_UA.UTF-8" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestReadyRoundTrip(t *testing.T) {
	if DecodeReady(EncodeReady(nil)) != nil {
		t.Fatal("ok must decode to nil")
	}
	err := DecodeReady(EncodeReady(&Error{Code: CodeOldTmux, Target: "box", Detail: "tmux 3.1c\nsecond line"}))
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != CodeOldTmux || coded.Target != "box" || coded.Detail != "tmux 3.1c second line" {
		t.Fatalf("decoded %#v", err)
	}
	if err.Error() != "box runs tmux 3.1c second line; kitmux needs tmux 3.2 or newer" {
		t.Fatalf("message %q", err.Error())
	}
	err = DecodeReady(EncodeReady(errors.New("plain")))
	if !errors.As(err, &coded) || coded.Code != CodeInternal || err.Error() != "plain" {
		t.Fatalf("decoded %#v", err)
	}
	err = DecodeReady("garbage")
	if !errors.As(err, &coded) || coded.Code != CodeDaemonFailed {
		t.Fatalf("decoded %#v", err)
	}
}
