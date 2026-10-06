package kitty

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// envListenOn is set by a parent kitty that listens for remote control; the
// kitty kitmux starts must not inherit another instance's socket.
const envListenOn = "KITTY_LISTEN_ON"

// StartOptions describe the kitty instance to start.
type StartOptions struct {
	// Bin is the kitty executable.
	Bin string
	// Configs are the configuration files to load, in order.
	Configs []string
	// Socket is the unix socket kitty listens on for remote control.
	Socket string
	// Session is the session file that creates the first tab.
	Session string
	// Log receives kitty's own stdout and stderr.
	Log io.Writer
}

// Start launches a kitty instance with remote control on a unix socket only.
// The caller waits on the returned command to learn when kitty exits.
func Start(opts StartOptions) (*exec.Cmd, error) {
	var args []string
	for _, conf := range opts.Configs {
		args = append(args, "--config", conf)
	}
	args = append(args,
		"-o", "allow_remote_control=socket-only",
		"--listen-on", "unix:"+opts.Socket,
		"--session", opts.Session,
	)
	cmd := exec.Command(opts.Bin, args...)
	cmd.Stdout = opts.Log
	cmd.Stderr = opts.Log
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, envListenOn+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	err := cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("start kitty: %w", err)
	}
	return cmd, nil
}
