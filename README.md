# kitmux (split-keys)

kitmux opens one kitty OS window with one tab per window of a tmux session, on your machine and on SSH hosts. This branch is the `split-keys` variant, a zsh script. Each kitty tab runs a tmux client that shows one tmux window. tmux draws the panes, and kitty's split, close and new-tab keys send tmux commands. The `split-native` branch is the other variant, a Go binary that turns every tmux pane into its own kitty window.

## Install

You need kitty (tested with 0.48.2), `jq`, and zsh (macOS's stock zsh 5.9 works) on the machine that runs kitty. Every target needs tmux 3.2 or newer and a POSIX `sh`. kitmux checks `tmux -V` on each target and refuses older versions.

```sh
git clone -b split-keys <repo> ~/Projects/kitmux-split-keys
ln -s ~/Projects/kitmux-split-keys/kitmux ~/.local/bin/kitmux
```

kitmux finds `kitmux.conf` next to the real path of the script, so a symlink on `PATH` is enough. Copy `kitmux-resurrect-hook` to every target that uses tmux-resurrect, see [tmux.conf](#tmuxconf).

## Usage

```sh
kitmux [-s SESSION] [TARGET...]
kitmux                          # session "main" on this machine
kitmux local devbox             # local tabs first, then devbox's
kitmux -s work me@build.example # session "work" on build.example
```

A TARGET is `local` or an SSH destination (`user@host`, `host`, or an ssh_config alias). SESSION defaults to `main` and applies to every target. kitmux creates SESSION on a target where it doesn't exist.

The launcher starts a dedicated kitty instance and a background bridge, then gives your prompt back. The bridge logs to `${XDG_STATE_HOME:-$HOME/.local/state}/kitmux/kitmux.log` and stops when that kitty exits.

`KITMUX_SSH` replaces the ssh command, split on whitespace, for example `KITMUX_SSH="ssh -F ~/test/ssh_config" kitmux devbox`. All commands for one host share an SSH master (`ControlPath=~/.ssh/kitmux-%C`, kept 10 minutes after the last use).

Tabs are titled `tmux-<index> <name>` for local windows and `<host>:tmux-<index> <name>` for remote ones, `user@` dropped. They follow the targets in command line order, then the tmux window index. tmux is the source of truth. A new, renamed, moved, swapped, renumbered or killed window shows up in kitty within about 2 seconds. When SESSION ends on a target, its tabs close. When no target has SESSION left, kitty closes.

Closing the kitty window or quitting kitty only detaches. tmux keeps every window and pane. kitty may ask before closing, because each tab runs a program (the tmux client).

## Keys

The keys work only in kitmux tabs. Plain kitty tabs in the same instance keep your normal bindings, because every map in `kitmux.conf` is guarded with `--when-focus-on var:kitmux_target`.

| Action | macOS | Linux | What kitmux does |
|---|---|---|---|
| New tab | cmd+t | ctrl+shift+t | `new-window -c "$HOME"` on the focused tab's host (the remote user's home for an SSH target), then focuses the new tab |
| Split side by side | cmd+d | ctrl+shift+d | `split-window -h`, the new pane starting in the focused pane's directory |
| Split stacked | cmd+shift+d | ctrl+alt+d | `split-window -v`, same directory |
| Next / previous split | cmd+shift+right / cmd+shift+left | ctrl+shift+right / ctrl+shift+left | `select-pane -t :.+` / `:.-` |
| Close split | cmd+w | ctrl+shift+w | asks `Kill tmux pane in <tab>? [y/N]`, then `kill-pane` on the active pane |
| Close tab | not bound, see below | not bound | asks `Kill tmux window <tab>? [y/N]`, then `kill-window` |
| Scrollback | cmd+h | ctrl+shift+h | the active pane's full history (`capture-pane -p -e -J -S -`) in `less -R +G` |

The questions open in a kitty overlay over the tab. Only `y` or `Y` kills. Any other key, Esc, or closing the overlay (cmd+w inside it) cancels. kitmux sends the kill to tmux and never closes a kitty tab itself. The tab closes when tmux reports the window gone. Closing the last pane of a window with the close split key therefore closes the tab too.

kitty 0.48 binds `close_tab` to kitty_mod+q, plus cmd+w on macOS. Your kitty.conf rebinds both (kitty_mod+q to `goto_tab -1`, cmd+w to `close_window_with_confirmation`), so no close tab key is left for kitmux to take over. To get one, uncomment the template line in `kitmux.conf` and pick a chord.

## tmux.conf

kitmux leaves server-wide tmux options alone. Two lines help, both in the global config of each target:

```tmux
# Let programs in a pane set kitty user vars through tmux (see "Bells").
set -g allow-passthrough on

# Keep kitmux helper sessions out of tmux-resurrect saves. List every session
# you mirror with kitmux after the path; with none, the hook assumes main.
set -g @resurrect-hook-post-save-layout '~/.local/bin/kitmux-resurrect-hook main'
```

Your own tmux.conf already has `allow-passthrough on`. It needs only the hook line.

## How a tab works

For each tmux window, kitmux creates a helper session grouped with SESSION, named `SESSION__<window id>` (window `@7` gets `main__7`). The helper has `status off` (kitty's tab bar replaces the status line), `mouse off`, `bell-action current`, and the tab's window selected. Grouped sessions share their windows, so a window or pane you create from any client shows up in SESSION and in every tab. One tmux control-mode client per target (`tmux -C attach -f no-output,ignore-size`) reports changes, and kitmux re-lists windows on every notification and every 2 seconds.

The tab's client reconnects when the network drops, showing `kitmux: reconnecting to <host>`, and exits for good once its helper or window is gone. kitmux deletes its helpers when it stops and also removes leftovers from an earlier run when it starts.

## Bells

A bell in a tmux window marks only that window's kitty tab. Each helper holds every window of the group, and tmux's default `bell-action any` would ring every helper's client, so every kitmux tab would get the bell. kitmux sets `bell-action current` on each helper, so only the client showing the window rings. SESSION's own `bell-action` stays as you set it.

Programs can also set kitty user vars from inside tmux with an OSC 1337 `SetUserVar` sequence wrapped in tmux passthrough. `~/.claude/hooks/bell.sh` does this to set `bell_pending`. With `allow-passthrough on`, tmux delivers the sequence only to clients showing that pane's window, so only that tab's kitty window gets the var.

## Reboot behaviour

kitmux works with tmux-resurrect and tmux-continuum.

- continuum saves from the status line, and kitmux tabs have none. kitmux runs continuum's `continuum_save.sh` with `tmux run-shell -b` every 60 seconds per target, and continuum keeps its own `@continuum-save-interval`.
- resurrect writes empty save files without a UTF-8 locale. kitmux runs every tmux command with your `LANG`, or `en_US.UTF-8` when `LANG` is unset.
- After a reboot, run `kitmux` again. On a target with no tmux server it runs `tmux new-session -d -s SESSION` (continuum may already restore SESSION while the server loads its config) and then resurrect's `restore.sh` through `tmux run-shell`. It never runs `restore.sh` against a server that was already running.
- resurrect brings back windows with their index, name and working directory, pane layouts, and programs on its process list (vim, less, man and so on). The tabs come back with the same titles and order.
- Without the hook, saves taken while kitmux runs list the helpers, and a later restore recreates them as extra sessions. kitmux deletes such leftovers when it starts, but a plain `tmux` start with `@continuum-restore on` would show them. The hook drops the `grouped_session` and `state` lines of `SESSION__<number>` sessions.

The plugin dir is the tmux global environment's `TMUX_PLUGIN_MANAGER_PATH`, which TPM sets, or `~/.tmux/plugins` when it's unset.

## Known limitations

- Session-scoped tmux commands typed inside a tab act on that tab's helper session. `move-window -r` is the visible case: tmux 3.7c renumbers only the helper and leaves SESSION and the tab titles as they were. Use `move-window -r -t main:` instead.
- kitty has close paths kitmux cannot remap: middle click on a tab and the macOS menu item Close Tab. kitmux reopens a kitmux tab closed this way within about 2 seconds, at the same position. Focus stays on whatever tab kitty picked after the close.
- kitty's own scrollback in a kitmux tab holds nothing useful, because the tmux client redraws the screen. Use the scrollback key.
- Tabs are sorted only in kitty's active OS window, because kitty's `move_tab_*` actions act on that window. Tabs dragged out into another OS window keep working but are not re-sorted.
- A remote host that reboots while kitmux runs counts as the session ending there, so its tabs close. Run kitmux again once the host is back.
- After a network drop, ssh notices after about 30 seconds (`ServerAliveInterval=10`, `ServerAliveCountMax=3`). Until then the remote tabs look frozen.
- If a target has no terminfo entry for kitty's `TERM`, the tab's client falls back to `xterm-256color`.
- The Linux keys come from your linux.conf but were only tested on macOS.

## Tests

`zsh kitmux_test.zsh` runs the unit tests: name validation and cleaning, titles, sort keys and tab moves, the tmux version check, shell quoting, and the resurrect hook. Everything else was checked by running kitmux against a local tmux server and an SSH container.
