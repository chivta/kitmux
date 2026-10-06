package kitty

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ErrRemoteControl wraps every failed remote-control command.
var ErrRemoteControl = errors.New("kitty remote control failed")

const (
	locationSideBySide = "vsplit"
	locationStacked    = "hsplit"
	locationLastTab    = "last"
	typeTab            = "tab"
	typeWindow         = "window"

	actionMoveTabBackward = "move_tab_backward"
)

// Client sends remote-control commands to one kitty instance through its
// unix socket, by running `kitty @`.
type Client struct {
	bin    string
	socket string
}

// NewClient returns a client for the kitty listening on socket. bin is the
// kitty executable.
func NewClient(bin, socket string) *Client {
	return &Client{bin: bin, socket: socket}
}

// LaunchSpec describes a window to open with `kitty @ launch`.
type LaunchSpec struct {
	// Tab opens the window in a new tab, placed last, in the OS window that
	// holds NextTo. Otherwise the window splits NextTo.
	Tab bool
	// NextTo is a kitty window id.
	NextTo int
	// Stacked puts the new window below NextTo instead of beside it.
	Stacked bool
	// Bias is the percentage of NextTo's space the new window takes; 0
	// leaves kitty's even split.
	Bias int
	// KeepFocus leaves the focus where it is.
	KeepFocus bool
	// TabTitle names the new tab.
	TabTitle string
	// Vars are user variables to set on the window, as NAME=VALUE.
	Vars []string
	// Command is the program to run and its arguments.
	Command []string
}

// List returns every OS window with its tabs and windows.
func (c *Client) List(ctx context.Context) ([]OSWindow, error) {
	out, err := c.run(ctx, "ls")
	if err != nil {
		return nil, err
	}
	var windows []OSWindow
	err = json.Unmarshal(out, &windows)
	if err != nil {
		return nil, fmt.Errorf("%w: decode ls: %v", ErrRemoteControl, err)
	}
	return windows, nil
}

// Launch opens a window and returns its kitty id.
func (c *Client) Launch(ctx context.Context, spec LaunchSpec) (int, error) {
	// Without --match, a remote launch goes to the active tab and ignores
	// --next-to when that window sits in another tab; matching the tab of
	// NextTo puts the window (or new tab) where it belongs.
	args := []string{"launch", "--match", matchWindowTab(spec.NextTo), "--next-to", matchID(spec.NextTo)}
	if spec.Tab {
		args = append(args, "--type", typeTab, "--location", locationLastTab)
		if spec.TabTitle != "" {
			args = append(args, "--tab-title", spec.TabTitle)
		}
	} else {
		location := locationSideBySide
		if spec.Stacked {
			location = locationStacked
		}
		args = append(args, "--type", typeWindow, "--location", location)
		if spec.Bias > 0 {
			args = append(args, "--bias", strconv.Itoa(spec.Bias))
		}
	}
	if spec.KeepFocus {
		args = append(args, "--keep-focus")
	}
	for _, v := range spec.Vars {
		args = append(args, "--var", v)
	}
	args = append(args, "--")
	args = append(args, spec.Command...)
	out, err := c.run(ctx, args...)
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("%w: launch printed %q", ErrRemoteControl, out)
	}
	return id, nil
}

// SetTabTitle sets the title of a tab.
func (c *Client) SetTabTitle(ctx context.Context, tabID int, title string) error {
	_, err := c.run(ctx, "set-tab-title", "--match", matchID(tabID), "--", title)
	return err
}

// FocusTab makes a tab the active one in its OS window.
func (c *Client) FocusTab(ctx context.Context, tabID int) error {
	_, err := c.run(ctx, "focus-tab", "--match", matchID(tabID))
	return err
}

// FocusWindow gives a window the focus.
func (c *Client) FocusWindow(ctx context.Context, windowID int) error {
	_, err := c.run(ctx, "focus-window", "--match", matchID(windowID))
	return err
}

// MoveActiveTabBackward moves the active tab of the OS window that holds
// windowID one position toward the front. kitty can only move the active tab,
// so callers focus the tab first.
func (c *Client) MoveActiveTabBackward(ctx context.Context, windowID int) error {
	_, err := c.run(ctx, "action", "--match", matchID(windowID), actionMoveTabBackward)
	return err
}

// CloseWindow closes a window without asking.
func (c *Client) CloseWindow(ctx context.Context, windowID int) error {
	_, err := c.run(ctx, "close-window", "--match", matchID(windowID))
	return err
}

// GotoLayout switches a tab to the named layout, which must be one of the
// user's enabled_layouts.
func (c *Client) GotoLayout(ctx context.Context, tabID int, layout string) error {
	_, err := c.run(ctx, "goto-layout", "--match", matchID(tabID), layout)
	return err
}

// run executes one remote-control command and returns its output.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"@", "--to", "unix:" + c.socket}, args...)
	cmd := exec.CommandContext(ctx, c.bin, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v: %s", ErrRemoteControl, args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// matchID builds the match expression for a window or tab id.
func matchID(id int) string {
	return "id:" + strconv.Itoa(id)
}

// matchWindowTab builds the tab match expression for the tab that holds a
// window.
func matchWindowTab(windowID int) string {
	return "window_id:" + strconv.Itoa(windowID)
}
