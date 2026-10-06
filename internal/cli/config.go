// Package cli turns the kitmux command line and environment into a validated
// Config, and holds the user-facing text for every error code.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"kitmux/internal/tabs"
)

const (
	// DefaultSession is the tmux session mirrored when -s is not given.
	DefaultSession = "main"
	// EnvSSH overrides the ssh command; its value is split on whitespace.
	EnvSSH = "KITMUX_SSH"

	defaultSSH         = "ssh"
	defaultLang        = "en_US.UTF-8"
	stateDirName       = "kitmux"
	logFileName        = "kitmux.log"
	kittyConfDirName   = "kitty"
	kittyConfFileName  = "kitty.conf"
	sessionFlag        = "s"
	fallbackRuntimeDir = "/tmp"
)

var (
	// sessionPattern is what a session name must match. Session names are
	// interpolated into remote shell commands, so nothing else is allowed.
	sessionPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)
	// langPattern is what LANG must match to be passed on to tmux; it too
	// ends up in remote shell commands.
	langPattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,64}$`)
)

// Config is everything kitmux reads from its command line and environment.
// It is built once at startup; a value that fails validation stops the
// process before anything else happens.
type Config struct {
	// Session is the tmux session mirrored on every target.
	Session string
	// Targets are "local" or SSH destinations, in command-line order.
	Targets []string
	// SSH is the ssh command and its leading arguments.
	SSH []string
	// Lang is the LANG value tmux runs with on every target.
	Lang string
	// Home is the user's home directory.
	Home string
	// LogFile is where the background daemon logs.
	LogFile string
	// RuntimeBases are the directories, in order of preference, under which
	// the per-run runtime directory may be created.
	RuntimeBases []string
	// KittyConf is the user's kitty.conf, loaded before kitmux's overlay.
	KittyConf string
}

// Load parses the arguments after the program name and reads the environment.
func Load(args []string) (Config, error) {
	flags := flag.NewFlagSet("kitmux", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	session := flags.String(sessionFlag, DefaultSession, "")
	err := flags.Parse(args)
	if err != nil {
		return Config{}, &Error{Code: CodeUsage, Detail: err.Error()}
	}
	targets := flags.Args()
	if len(targets) == 0 {
		targets = []string{tabs.LocalTarget}
	}
	err = validate(*session, targets)
	if err != nil {
		return Config{}, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, &Error{Code: CodeNoHome, Detail: err.Error()}
	}
	cfg := Config{
		Session:      *session,
		Targets:      targets,
		SSH:          strings.Fields(os.Getenv(EnvSSH)),
		Lang:         os.Getenv("LANG"),
		Home:         home,
		LogFile:      filepath.Join(envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), stateDirName, logFileName),
		RuntimeBases: runtimeBases(),
		KittyConf:    filepath.Join(envOr("KITTY_CONFIG_DIRECTORY", filepath.Join(home, ".config", kittyConfDirName)), kittyConfFileName),
	}
	if len(cfg.SSH) == 0 {
		cfg.SSH = []string{defaultSSH}
	}
	if !langPattern.MatchString(cfg.Lang) {
		cfg.Lang = defaultLang
	}
	return cfg, nil
}

// validate checks the session name and every target.
func validate(session string, targets []string) error {
	if !sessionPattern.MatchString(session) || session == "." || session == ".." {
		return &Error{Code: CodeBadSession, Detail: session}
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if !validTarget(target) {
			return &Error{Code: CodeBadTarget, Detail: target}
		}
		if seen[target] {
			return &Error{Code: CodeDuplicateTarget, Detail: target}
		}
		seen[target] = true
	}
	return nil
}

// validTarget reports whether target can be handed to ssh as a destination:
// not empty, not an option, and made of printable, non-space characters.
func validTarget(target string) bool {
	if target == "" || strings.HasPrefix(target, "-") {
		return false
	}
	for _, r := range target {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// runtimeBases lists where the runtime directory may live: XDG_RUNTIME_DIR,
// then TMPDIR, then /tmp. The caller takes the first one that keeps socket
// paths short enough.
func runtimeBases() []string {
	var bases []string
	for _, name := range []string{"XDG_RUNTIME_DIR", "TMPDIR"} {
		value := os.Getenv(name)
		if value != "" {
			bases = append(bases, value)
		}
	}
	return append(bases, fallbackRuntimeDir)
}

// envOr returns the environment variable name, or fallback when it is unset
// or empty.
func envOr(name, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}

// Usage is the one-line synopsis printed with usage errors.
func Usage() string {
	return fmt.Sprintf("usage: kitmux [-%s SESSION] [TARGET...]", sessionFlag)
}
