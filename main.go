// Command kitmux opens one kitty OS window that mirrors a tmux session per
// target (the local machine and/or SSH hosts): a kitty tab per tmux window and
// a kitty window per tmux pane.
//
// `kitmux [-s SESSION] [TARGET...]` checks the targets, starts the background
// daemon (`kitmux _daemon`), which starts kitty, and returns. The other
// underscore subcommands are run by kitty: `_pane` in every kitmux window,
// `_newtab`, `_split` and `_confirm` from kitmux's key bindings.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"kitmux/internal/cli"
	"kitmux/internal/daemon"
	"kitmux/internal/ipc"
	"kitmux/internal/proxy"
	"kitmux/internal/tmux"
)

const (
	subcommandDaemon  = "_daemon"
	subcommandNewTab  = "_newtab"
	subcommandSplit   = "_split"
	subcommandConfirm = "_confirm"

	splitStacked = "stacked"

	// readyFD is the descriptor on which the launcher passes the daemon its
	// end of the startup pipe.
	readyFD = 3
	// readyTimeout bounds how long the launcher waits for the daemon.
	readyTimeout   = 90 * time.Second
	preflightLimit = 60 * time.Second

	logDirMode  = 0o700
	logFileMode = 0o600

	exitUsage = 2
	exitError = 1
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case daemon.SubcommandPane:
			os.Exit(runPane(args[1:]))
		case subcommandDaemon:
			os.Exit(runDaemon(args[1:]))
		case subcommandNewTab, subcommandSplit, subcommandConfirm:
			os.Exit(runKeyAction(args[0], args[1:]))
		}
	}
	os.Exit(launch(args))
}

// launch is the user-facing command: validate, check every target from the
// user's terminal (so ssh can ask for passwords and host keys, and errors are
// shown where they can be read), then start the daemon and wait until it
// reports that kitty is up.
func launch(args []string) int {
	cfg, err := cli.Load(args)
	if err != nil {
		return fail(err)
	}
	err = tmux.EnsureControlDir(cfg.Home)
	if err != nil {
		return fail(&cli.Error{Code: cli.CodeInternal, Detail: err.Error()})
	}
	for i, name := range cfg.Targets {
		target := tmux.Target{Index: i, Name: name, Session: cfg.Session, Lang: cfg.Lang, SSH: cfg.SSH, Home: cfg.Home}
		ctx, cancel := context.WithTimeout(context.Background(), preflightLimit)
		_, err = target.Prepare(ctx, tmux.ModeStart)
		cancel()
		if err != nil {
			return fail(cli.TargetError(name, err))
		}
	}

	self, err := executable()
	if err != nil {
		return fail(&cli.Error{Code: cli.CodeDaemonFailed, Detail: err.Error()})
	}
	logFile, err := openLog(cfg.LogFile)
	if err != nil {
		return fail(&cli.Error{Code: cli.CodeDaemonFailed, Detail: err.Error()})
	}
	defer logFile.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return fail(&cli.Error{Code: cli.CodeDaemonFailed, Detail: err.Error()})
	}
	cmd := exec.Command(self, append([]string{subcommandDaemon}, args...)...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.ExtraFiles = []*os.File{readyWrite} // becomes readyFD
	// A session of its own: the daemon outlives the shell that started it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	readyWrite.Close()
	if err != nil {
		return fail(&cli.Error{Code: cli.CodeDaemonFailed, Detail: err.Error()})
	}
	go func() { _ = cmd.Wait() }()

	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(readyRead).ReadString('\n')
		lines <- line
	}()
	select {
	case line := <-lines:
		if line == "" {
			return fail(&cli.Error{Code: cli.CodeDaemonFailed, Detail: "it exited early; see " + cfg.LogFile})
		}
		err = cli.DecodeReady(line[:len(line)-1])
		if err != nil {
			return fail(err)
		}
		return 0
	case <-time.After(readyTimeout):
		return fail(&cli.Error{Code: cli.CodeDaemonFailed, Detail: "no answer; see " + cfg.LogFile})
	}
}

// runDaemon is `kitmux _daemon [-s SESSION] [TARGET...]`, started by launch.
func runDaemon(args []string) int {
	ready := os.NewFile(readyFD, "ready")
	cfg, err := cli.Load(args)
	if err != nil {
		_, _ = fmt.Fprintln(ready, cli.EncodeReady(err))
		return exitUsage
	}
	logFile, err := openLog(cfg.LogFile)
	if err != nil {
		_, _ = fmt.Fprintln(ready, cli.EncodeReady(&cli.Error{Code: cli.CodeDaemonFailed, Detail: err.Error()}))
		return exitError
	}
	defer logFile.Close()
	zerolog.TimeFieldFormat = time.RFC3339Nano
	log.Logger = zerolog.New(logFile).With().Timestamp().Caller().Int("pid", os.Getpid()).Logger()

	self, err := executable()
	if err != nil {
		_, _ = fmt.Fprintln(ready, cli.EncodeReady(&cli.Error{Code: cli.CodeDaemonFailed, Detail: err.Error()}))
		return exitError
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
	log.Info().Str("session", cfg.Session).Strs("targets", cfg.Targets).Msg("daemon starting")
	err = daemon.Run(ctx, daemon.Options{Config: cfg, Bin: self, Ready: ready})
	if err != nil {
		log.Error().Err(err).Msg("daemon failed")
		return exitError
	}
	log.Info().Msg("daemon stopped")
	return 0
}

// runPane is `kitmux _pane SOCKET TARGET PANE`.
func runPane(args []string) int {
	if len(args) != 3 {
		return exitUsage
	}
	target, err := strconv.Atoi(args[1])
	if err != nil {
		return exitUsage
	}
	err = proxy.RunPane(args[0], target, args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "kitmux:", err)
		return exitError
	}
	return 0
}

// runKeyAction is `kitmux _newtab SOCKET WINDOW`, `kitmux _split SOCKET
// side|stacked WINDOW` and `kitmux _confirm SOCKET pane|window WINDOW`.
func runKeyAction(name string, args []string) int {
	if len(args) < 2 {
		return exitUsage
	}
	window, err := strconv.Atoi(args[len(args)-1])
	if err != nil {
		return exitUsage
	}
	socket := args[0]
	switch {
	case name == subcommandNewTab && len(args) == 2:
		err = proxy.RunAction(socket, ipc.Request{Op: ipc.OpNewTab, Window: window})
	case name == subcommandSplit && len(args) == 3:
		err = proxy.RunAction(socket, ipc.Request{Op: ipc.OpSplit, Window: window, Stacked: args[1] == splitStacked})
	case name == subcommandConfirm && len(args) == 3:
		err = proxy.RunConfirm(socket, ipc.Scope(args[1]), window)
	default:
		return exitUsage
	}
	if err != nil {
		return exitError
	}
	return 0
}

// fail prints a coded error for the user and returns the exit status.
func fail(err error) int {
	fmt.Fprintln(os.Stderr, "kitmux:", err)
	if cli.IsUsage(err) {
		fmt.Fprintln(os.Stderr, cli.Usage())
		return exitUsage
	}
	return exitError
}

// executable returns the path of the running kitmux binary with symlinks
// resolved, for kitty to run.
func executable() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(self)
}

// openLog opens the daemon log for appending, creating its directory.
func openLog(path string) (*os.File, error) {
	err := os.MkdirAll(filepath.Dir(path), logDirMode)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
}
