# kitmux

Mirrors tmux sessions (local and over SSH) as native kitty tabs in one kitty window.

Two variants live on separate branches:

- `split-keys`: a zsh script. Each kitty tab is a tmux client showing one tmux window, and kitty's split and close keys drive tmux panes.
- `split-native`: a Go binary. Each tmux pane is its own kitty window, fed by a tmux control-mode connection.
