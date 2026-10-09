# kitmux (split-keys)

kitmux opens one kitty OS window with one tab per window of a tmux session, on your machine and on SSH hosts. Every run opens its OS window in one shared kitty process, so macOS treats all kitmux windows as one app and cmd+` cycles through them. This branch is the `split-keys` variant, a zsh script. Each kitty tab runs a tmux client that shows one tmux window with your normal tmux status bar. tmux draws the panes, and kitty's split, close and new-tab keys send tmux commands. Your tmux prefix keys work in every tab too. The `split-native` branch is the other variant, a Go binary that turns every tmux pane into its own kitty window.

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

The launcher starts a background bridge, which opens a new OS window in the shared kitty process, then gives your prompt back. The first run starts that process (`kitty --single-instance --instance-group kitmux-<n> --listen-on ...`, with your kitty.conf and `kitmux.conf`), and later runs only add an OS window to it. The process is dedicated to kitmux and separate from any kitty you started yourself. Its socket lives in `${XDG_RUNTIME_DIR:-${TMPDIR:-/tmp}}/kitmux-<uid>/kitty.sock`, a fixed location that no single run owns. The bridge logs to `${XDG_STATE_HOME:-$HOME/.local/state}/kitmux/kitmux.log`.

Each run's bridge manages only the tabs of its own OS window and stops when that window closes. Closing one OS window detaches only that run, and the other windows keep working. Run `kitmux` again for a session that already has a window and kitmux focuses that window and exits 0 instead of opening a duplicate (two windows would fight over the same helper sessions). Mixing is allowed across sessions: `kitmux` and `kitmux -s other` give two OS windows in one process. A run that asks for several targets where only some already have a window for that session fails with a message.

`KITMUX_SSH` replaces the ssh command, split on whitespace, for example `KITMUX_SSH="ssh -F ~/test/ssh_config" kitmux devbox`. All commands for one host share an SSH master (`ControlPath=~/.ssh/kitmux-%C`, kept 10 minutes after the last use).

Tabs are titled `tmux-<index> <name>` for local windows and `<host>:tmux-<index> <name>` for remote ones, `user@` dropped. They follow the targets in command line order, then the tmux window index. tmux is the source of truth. A new, renamed, moved, swapped, renumbered or killed window shows up in kitty within about 2 seconds. When SESSION ends on a target, its tabs close. When no target has SESSION left, the run's OS window closes without asking, and the other windows stay.

Closing an OS window only detaches that run, and quitting kitty detaches every run. tmux keeps every window and pane. kitty may ask before closing, because each tab runs a program (the tmux client). Whether kitty itself quits after its last window closes follows your kitty.conf (`macos_quit_when_last_window_closed`). If it stays running, the next run reuses it.

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

### tmux prefix keys

Each tab is a real tmux client, so your prefix reaches tmux and everything bound after it works, with the status bar showing the windows of the mirrored session. The window-switching keys behave like kitty tabs. When a tab's client changes window, for example with `<prefix> n`, `<prefix> p`, `<prefix> <digit>`, `<prefix> w` or `<prefix> c` (which creates a window and selects it), kitmux focuses the kitty tab of the window that was selected, creating the tab first for a new window, and puts the helper back on the window of its own tab. Every tab keeps showing its own window. Prompt-based keys such as `<prefix> ,` (rename window) work in the status line, and the tab title follows the new name.

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

For each tmux window, kitmux creates a helper session grouped with SESSION, named `SESSION__<window id>` (window `@7` gets `main__7`). The helper has `mouse off`, `bell-action current`, and the tab's window selected. Its status bar is your normal one, and its `status-left` is `[#{session_group}] ` so it names the mirrored session (`main`) instead of the helper (`main__3`). The helper gets that `status-left` only when the session's `status-left` is tmux's default, so a `status-left` you set yourself is left alone (use `#{session_group}` in it for the session name). Grouped sessions share their windows, so a window or pane you create from any client shows up in SESSION and in every tab. One tmux control-mode client per target (`tmux -C attach -f no-output,ignore-size`) reports changes, and kitmux re-lists windows on every notification and every 2 seconds. When a helper's current window changes, tmux sends `%session-window-changed` to that client, and kitmux treats it as a request to focus the tab of the new window. A helper that tmux moved by itself (swap, renumber) is put back without moving focus.

The tab's client reconnects when the network drops, showing `kitmux: reconnecting to <host>`, and exits for good once its helper or window is gone. kitmux deletes its helpers when it stops and also removes leftovers from an earlier run when it starts.

## Bells

A bell in a tmux window marks only that window's kitty tab. Each helper holds every window of the group, and tmux's default `bell-action any` would ring every helper's client, so every kitmux tab would get the bell. kitmux sets `bell-action current` on each helper, so only the client showing the window rings. SESSION's own `bell-action` stays as you set it.

Programs can also set kitty user vars from inside tmux with an OSC 1337 `SetUserVar` sequence wrapped in tmux passthrough. `~/.claude/hooks/bell.sh` does this to set `bell_pending`. With `allow-passthrough on`, tmux delivers the sequence only to clients showing that pane's window, so only that tab's kitty window gets the var.

## Reboot behaviour

kitmux works with tmux-resurrect and tmux-continuum.

- continuum saves from the status line, and kitmux tabs now have one, so continuum's own auto-save runs from them. kitmux still runs continuum's `continuum_save.sh` with `tmux run-shell -b` every 60 seconds per target, and continuum keeps its own `@continuum-save-interval`. Both can save, so a save may run twice in a short time.
- resurrect writes empty save files without a UTF-8 locale. kitmux runs every tmux command with your `LANG`, or `en_US.UTF-8` when `LANG` is unset.
- After a reboot, run `kitmux` again. On a target with no tmux server it runs `tmux new-session -d -s SESSION` (continuum may already restore SESSION while the server loads its config) and then resurrect's `restore.sh` through `tmux run-shell`. It never runs `restore.sh` against a server that was already running.
- resurrect brings back windows with their index, name and working directory, pane layouts, and programs on its process list (vim, less, man and so on). The tabs come back with the same titles and order.
- Without the hook, saves taken while kitmux runs list the helpers, and a later restore recreates them as extra sessions. kitmux deletes such leftovers when it starts, but a plain `tmux` start with `@continuum-restore on` would show them. The hook drops the `grouped_session` and `state` lines of `SESSION__<number>` sessions.

The plugin dir is the tmux global environment's `TMUX_PLUGIN_MANAGER_PATH`, which TPM sets, or `~/.tmux/plugins` when it's unset.

## Known limitations

- Session-scoped tmux commands typed inside a tab act on that tab's helper session. `move-window -r` is the visible case: tmux 3.7c renumbers only the helper and leaves SESSION and the tab titles as they were. Use `move-window -r -t main:` instead.
- kitty has close paths kitmux cannot remap: middle click on a tab and the macOS menu item Close Tab. kitmux reopens a kitmux tab closed this way within about 2 seconds, at the same position. Focus stays on whatever tab kitty picked after the close.
- Each tab's status bar takes one row. A tab's client that switches to another session (`<prefix> s`, or a window listed under another session in `<prefix> w`) stays there, and kitmux does not move it back. The trees also list kitmux's helper sessions; pick windows under the tab's own session.
- kitty's own scrollback in a kitmux tab holds nothing useful, because the tmux client redraws the screen. Use the scrollback key.
- kitty's `move_tab_*` and `goto_tab` actions act only on its active OS window. A run sorts its tabs only while its OS window is the active one and catches up when it becomes so. A tab opened in a window that is not the active one stays that window's active tab, instead of switching back to the tab that was. Tabs dragged out into another OS window keep working but are not re-sorted.
- All runs share one kitty process, so its config (`kitty.conf`, `kitmux.conf`, the key aliases) comes from the run that started it. Quit that kitty to pick up changes.
- Two runs for the same session on one target are refused, and so is a run that overlaps another run's session on some but not all of its targets. A bridge killed with SIGKILL leaves an OS window nothing manages. kitmux ignores its tabs when it looks for an open window.
- A remote host that reboots while kitmux runs counts as the session ending there, so its tabs close. Run kitmux again once the host is back.
- After a network drop, ssh notices after about 30 seconds (`ServerAliveInterval=10`, `ServerAliveCountMax=3`). Until then the remote tabs look frozen.
- If a target has no terminfo entry for kitty's `TERM`, the tab's client falls back to `xterm-256color`.
- The Linux keys come from your linux.conf but were only tested on macOS.

## Tests

`zsh kitmux_test.zsh` runs the unit tests: name validation and cleaning, titles, sort keys and tab moves, the tmux version check, shell quoting, and the resurrect hook. Everything else was checked by running kitmux against a local tmux server and an SSH container.
