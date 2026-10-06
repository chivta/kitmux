# kitmux, split-native variant

kitmux opens one kitty OS window that mirrors one tmux session per target, the local machine and/or SSH hosts. Every tmux window is a kitty tab, and every tmux pane is its own kitty window inside that tab.

This branch is the Go variant. A background daemon keeps one tmux control-mode connection per target (`tmux -C attach -t =SESSION -f ignore-size`, through ssh for remote targets). It streams each pane's raw output into that pane's kitty window and sends keystrokes back with `send-keys -H`. The `split-keys` branch holds the other variant, where tmux draws the panes.

## Install

Building needs Go 1.27. At run time kitmux needs kitty, tested with 0.48.2, and tmux 3.2 or newer on every target, tested with 3.7c.

```sh
go build -o ~/.local/bin/kitmux .
```

Any directory on `PATH` works. kitty runs this binary in every pane window, so leave it in place while kitmux runs.

## Usage

```sh
kitmux [-s SESSION] [TARGET...]
```

- `TARGET` is `local` or an SSH destination: `user@host`, `host` or an ssh_config alias. No target means `local`.
- `-s SESSION` names the tmux session on every target, `main` by default. A session name is 1 to 32 characters of `A-Z a-z 0-9 _ . -`.
- `KITMUX_SSH` replaces the ssh command and is split on whitespace, e.g. `KITMUX_SSH="ssh -F ~/.ssh/other_config"`.

```sh
kitmux                     # the local "main" session
kitmux local build-box     # local tabs first, then build-box's tabs
kitmux -s work me@server
```

`kitmux` checks every target from your terminal first, so ssh can ask for a password or a host key there. Then it starts the daemon, waits until kitty is up and gives your prompt back. If tmux is missing or older than 3.2, or a host cannot be reached, it prints why and exits non-zero without starting kitty.

The daemon logs to `${XDG_STATE_HOME:-~/.local/state}/kitmux/kitmux.log` and exits when its kitty exits. Sockets and the generated kitty config live in `${XDG_RUNTIME_DIR:-$TMPDIR}/kitmux-<uid>/<random>/` and are removed on exit.

Remote commands to one host share an SSH master: `ControlPath ~/.ssh/kitmux-%C`, `ControlPersist 10m`, `ServerAliveInterval 10`, `ServerAliveCountMax 3`.

## Who decides what

tmux decides which windows and panes exist, their names and the tab order. A window created in tmux by any client gets a tab. A renamed, moved, swapped or renumbered window gets its title and position fixed within two seconds. A killed window or pane closes its tab or kitty window. Tab titles are `tmux-<index> <name>` for `local` and `<host>:tmux-<index> <name>` for remote targets. Tabs go in target order as given on the command line, then by window index. Tabs you open yourself in the kitmux kitty keep their positions.

kitty decides geometry. When you resize a kitty window or the OS window, rotate a split or move a window, kitmux rebuilds the tmux layout from kitty's split tree and applies it, reordering tmux panes with `swap-pane` where needed. A layout change made on the tmux side with `select-layout`, `resize-pane` or `swap-pane` gets put back to match kitty.

A pane created in tmux opens a kitty window next to its neighbour in the tmux layout. If you close a kitmux window or tab in kitty while its pane still exists, kitmux opens it again. That is also what happens after a middle-click on a tab: kitty handles that click itself and offers no way to remap it.

Closing the kitty OS window, quitting kitty, logging out or rebooting only detaches. tmux keeps every window and pane.

## Keys

These apply in kitmux windows only. Every binding is guarded with `map --when-focus-on var:kitmux_target`, so plain kitty tabs keep your own bindings.

| Action | macOS | Linux | What happens |
|---|---|---|---|
| New tab | cmd+t | ctrl+shift+t | `new-window` on the focused tab's target; the new tab gets the focus |
| Split side by side | cmd+d | ctrl+shift+d | `split-window -h` of the focused pane; the new kitty window follows |
| Split stacked | cmd+shift+d | ctrl+alt+d | `split-window -v` of the focused pane |
| Next / previous split | cmd+shift+right / left | ctrl+shift+right / left | kitty's own window switching, not remapped |
| Close split | cmd+w | ctrl+shift+w | asks `Kill tmux pane in <tab title>? [y/N]`, then `kill-pane` |
| Close tab | every key still bound to `close_tab` after your kitty.conf | same | asks `Kill tmux window <tab title>? [y/N]`, then `kill-window` |
| Scrollback | kitty_mod+h | ctrl+shift+h | kitty's own scrollback, not remapped |

The confirmation runs in a kitty overlay. Only `y` or `Y` kills. Any other key, Esc or closing the overlay cancels. The kill goes to tmux, and the kitty window or tab closes once tmux reports the pane or window gone.

At startup kitmux loads your kitty.conf with kitty's own config loader to find the keys still bound to `close_tab`. A key that also has one of the bindings above keeps that binding. With the dotfiles kitty.conf this leaves no close-tab key: cmd+w is close split and kitty_mod+q is `goto_tab -1`.

Every pane window has kitty's native scrollback. kitmux paints the pane's tmux history into it when the window opens.

## tmux.conf

kitmux needs nothing in tmux.conf. Reboot survival needs tmux-resurrect and tmux-continuum, e.g. through TPM:

```tmux
set -g @plugin 'tmux-plugins/tmux-resurrect'
set -g @plugin 'tmux-plugins/tmux-continuum'
set -g @continuum-restore 'on'
```

`allow-passthrough` makes no difference to kitmux. Control mode hands kitmux the raw pane output, and kitmux unwraps tmux passthrough (`ESC P tmux; ... ESC \`) for kitty whatever the option is set to. User variables and bells that a hook sends this way, like the Claude Code bell hook, land on the kitty window of the pane that sent them.

kitmux leaves tmux's server options alone: `terminal-overrides`, `terminal-features`, `default-terminal`.

## Reboot behaviour

- continuum saves only while some client draws a status line, and kitmux's control client draws none. So once a minute kitmux runs `tmux run-shell -b <plugins>/tmux-continuum/scripts/continuum_save.sh` on every target, and continuum still saves no more often than `@continuum-save-interval`. A target without that script gets no tick. `<plugins>` is the global `TMUX_PLUGIN_MANAGER_PATH`, falling back to `~/.tmux/plugins/` .
- Every tmux command kitmux runs gets your `LANG`, or `en_US.UTF-8` when yours is unset. Without a UTF-8 locale resurrect writes an empty save file.
- When kitmux starts and a target has no tmux server, kitmux runs `tmux new-session -d -s SESSION`, treating "duplicate session" as success because continuum may already have restored the session while the server read its config. Then it runs resurrect's `restore.sh` through `tmux run-shell`. It never runs `restore.sh` against a server that was already running.
- When a remote host reboots while kitmux runs, kitmux loses the connection, shows `kitmux: reconnecting to <host>` at the bottom of that host's panes and retries with backoff. Once ssh works again it finds no tmux server, restores the same way, reattaches and repaints every pane. After a dropped connection to a host that did not reboot, kitmux reattaches and repaints without restoring anything.
- A session that ends while kitmux runs, because its last window was killed or the server was, drops that target's tabs. When no target has a session left, kitmux closes its kitty and exits. The next `kitmux` restores.
- kitmux creates no helper sessions, so a resurrect save holds only your own sessions.

## Known limitations

Found while testing against the local and Docker SSH fixtures from the plan.

- **Panes can be a cell or two smaller than their kitty window.** tmux needs the panes of a row or column to tile their parent exactly, while kitty lays out windows in pixels. When the two disagree, e.g. a column of two panes beside a single pane, kitmux gives tmux the smaller size and the kitty window shows a blank row or column at its edge.
- **Some tmux-side arrangements are approximated.** kitty can only split one window at a time. A `split-window -f` pane opens next to the nearest pane instead of across the whole window, and tmux then follows kitty's arrangement. A reopened window of the first pane in a row or column lands after its neighbour, and tmux swaps the two panes to match.
- **Another attached tmux client decides window sizes.** kitmux attaches with `-f ignore-size`. If a normal terminal is also attached to the session, tmux sizes the windows for that terminal and kitmux panes stop matching their kitty windows.
- **A repaint cannot restore every mode.** kitmux paints a window from tmux's `capture-pane` and pane state when the window opens, after a resize and after a reconnect. tmux does not report the kitty keyboard protocol flags, so an app that turned the protocol on before such a paint, for example in a window created from outside kitmux, gets legacy keys until it turns the protocol on again. Apps started inside a kitmux window get the protocol end to end. The visible screen is painted row by row, so lines that were soft-wrapped at that moment stay broken in kitty.
- **tmux answers terminal queries.** kitmux removes queries such as device attributes, cursor position, colour queries and XTVERSION before kitty sees them. tmux has already answered them, and kitty's second answer would arrive in the app as typed input. Queries inside tmux passthrough go to kitty, and kitty's answers reach the app.
- **Reconnecting needs non-interactive ssh auth.** The daemon has no terminal, so it runs ssh with `BatchMode=yes`. A password or passphrase prompt only works at launch. After a dropped connection the SSH master has to come back with keys or an agent. ssh notices a dead connection after about 30 seconds of silence.
- **tmux has to be on the PATH of non-interactive ssh commands** on remote hosts.
- **kitty settings.** kitmux tabs need `splits` in `enabled_layouts`; kitmux switches its tabs to that layout. kitmux's kitty instance sets `confirm_os_window_close 0`, because kitty cannot tell that a kitmux window is idle and closing only detaches, and `macos_quit_when_last_window_closed yes`.
- **cmd+h hides kitty on macOS.** kitty's macOS defaults bind cmd+h to `hide_macos_app` after `kitty_mod+h show_scrollback`, so with `kitty_mod cmd` the scrollback key is taken. kitmux leaves scrollback keys to kitty.
- **Focus stays in kitty.** Moving between kitty windows does not change tmux's active pane. kitmux windows are titled `kitmux` until the app in the pane sets a title.
- **A dropped connection can leave an idle `tmux -C` process on the remote host.** After a network drop that outlasts ssh's keepalive, the old control client may stay behind on the host, detached from tmux. kitmux attaches a new client and the old one does no harm, but it stays until you kill it or the host reboots.
- **resurrect restores only programs started from the pane's shell.** A program that is the pane's own root process, e.g. `tmux new-window 'vim file'`, is saved without its command line and comes back as a shell. This is tmux-resurrect's behaviour, not kitmux's.
