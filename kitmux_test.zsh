#!/bin/zsh
# Unit tests for kitmux's pure logic. Run: zsh kitmux_test.zsh
# Sources kitmux for its functions (it does not start when sourced) and the
# resurrect hook is exercised as a text filter on sample save files.

source ${0:A:h}/kitmux
typeset -gi failures=0 checks=0

# Compares a value with the expected one and records a failure. Args: label,
# expected, actual.
check() {
  checks+=1
  [[ $2 == $3 ]] && return 0
  failures+=1
  print -r -- "FAIL $1"
  print -r -- "  expected: ${(qqq)2}"
  print -r -- "  actual:   ${(qqq)3}"
}

# Runs a predicate and compares its outcome with yes or no. Args: label,
# expected, command...
check_status() {
  local label=$1 expected=$2 actual=no
  shift 2
  "$@" && actual=yes
  check "$label" $expected $actual
}

# Succeeds when string $1 sorts before string $2.
sorts_before() {
  [[ $1 < $2 ]]
}

# Applies the moves in reply to the tab keys given as args, the way kitty's
# move_tab_backward does, and sets REPLY to the resulting order.
apply_moves() {
  local -a tabs
  local move
  local -i from to
  tabs=("$@")
  for move in "${reply[@]}"; do
    from=${move% *} to=${move#* }
    (( from > to )) || { REPLY="bad move $move"; return }
    tabs=($tabs[1,to-1] $tabs[from] $tabs[to,from-1] $tabs[from+1,-1])
  done
  REPLY="$tabs"
}

# Checks that sort_plan orders the kitmux tabs and leaves "-" tabs in place.
# Args: expected final order as one string, then the tabs.
check_sort() {
  local expected=$1
  shift
  sort_plan "$@"
  apply_moves "$@"
  check "sort_plan $*" "$expected" "$REPLY"
}

# --- session and target validation ---
check_status "session main" yes valid_session main
check_status "session with dots and dashes" yes valid_session my_work-2.x
check_status "session of 32 chars" yes valid_session ${(l:32::a:):-}
check_status "session of 33 chars" no valid_session ${(l:33::a:):-}
check_status "empty session" no valid_session ''
check_status "session ." no valid_session .
check_status "session .." no valid_session ..
check_status "session with space" no valid_session 'a b'
check_status "session with colon" no valid_session a:b
check_status "session with quote" no valid_session "a'b"
check_status "session with newline" no valid_session $'a\nb'

check_status "target local" yes valid_target local
check_status "target user@host" yes valid_target user@host.example
check_status "target non-ascii alias" yes valid_target сервер
check_status "target starting with dash" no valid_target -oProxyCommand=x
check_status "empty target" no valid_target ''
check_status "target with space" no valid_target 'a b'
check_status "target with escape" no valid_target $'a\eb'
check_status "target with newline" no valid_target $'a\nb'
check_status "target with zero-width space" no valid_target $'a​b'

# --- names and titles ---
clean_name $'a\tb\ec\x7fd'
check "clean_name strips C0 and DEL" abcd "$REPLY"
clean_name $'x\u0085y'
check "clean_name strips C1" xy "$REPLY"
clean_name $'a​b‌c‍d⁠e﻿f'
check "clean_name strips zero-width" abcdef "$REPLY"
clean_name $'a‮b‪c⁦d⁩e‎f‏g؜h'
check "clean_name strips bidi" abcdefgh "$REPLY"
clean_name $'вікно ✓ 中'
check "clean_name keeps other unicode" $'вікно ✓ 中' "$REPLY"

tab_title local 3 vim
check "local title" "tmux-3 vim" "$REPLY"
tab_title local 0 ''
check "empty name" "tmux-0" "$REPLY"
tab_title local 2 $'\e​'
check "name that is only stripped characters" "tmux-2" "$REPLY"
tab_title user@box.example 12 'my shell'
check "remote title drops user@" "box.example:tmux-12 my shell" "$REPLY"
tab_title box 1 $'ev‮il'
check "remote title, bidi stripped" "box:tmux-1 evil" "$REPLY"

order_key 0 7
check "order key" 00000000000007 "$REPLY"
order_key 12 345
check "order key, wide values" 00120000000345 "$REPLY"
order_key 0 9; first=$REPLY
order_key 0 10; second=$REPLY
check_status "index 9 sorts before 10" yes sorts_before $first $second
order_key 0 999; first=$REPLY
order_key 1 0; second=$REPLY
check_status "first target sorts before second" yes sorts_before $first $second

# --- tmux version ---
check_status "tmux 3.7c" yes tmux_version_ok "tmux 3.7c"
check_status "tmux 3.2" yes tmux_version_ok "tmux 3.2"
check_status "tmux 3.2a" yes tmux_version_ok "tmux 3.2a"
check_status "tmux 4.0" yes tmux_version_ok "tmux 4.0"
check_status "tmux next-3.5" yes tmux_version_ok "tmux next-3.5"
check_status "tmux 3.1c" no tmux_version_ok "tmux 3.1c"
check_status "tmux 2.9" no tmux_version_ok "tmux 2.9"
check_status "tmux 1.8" no tmux_version_ok "tmux 1.8"
check_status "tmux master" yes tmux_version_ok "tmux master"

# --- tab sorting ---
sort_plan 1 2 3
check "sorted tabs need no moves" 0 $#reply
sort_plan - - -
check "foreign tabs need no moves" 0 $#reply
sort_plan
check "no tabs need no moves" 0 $#reply
sort_plan 1 2 4 5 3
check "appended tab goes back in one move" "5 3" "$reply"
check_sort "1 2 3 4 5" 1 2 4 5 3
check_sort "1 2 3" 3 2 1
check_sort "1 2 3 4" 2 1 4 3
check_sort "- 1 2 3" - 3 1 2
check_sort "1 - 2 - 3" 3 - 1 - 2
check_sort "- 1 - - 2 3 -" - 3 - - 1 2 -
check_sort "1 - 2" 2 - 1
check_sort "a1 a2 b1 b2 -" b1 a2 b2 a1 -
check_sort "1 1 2" 2 1 1

# --- command lines for a target ---
tmux_lang=en_US.UTF-8
shell_line tmux -C attach-session -t '=main'
check "shell_line quotes every word" "env LANG=en_US.UTF-8 'tmux' '-C' 'attach-session' '-t' '=main'" "$REPLY"
shell_line tmux display "it's"
check "shell_line escapes a quote" "env LANG=en_US.UTF-8 'tmux' 'display' 'it'\\''s'" "$REPLY"
check "quoted line survives sh" "it's #{x} \$HOME" "$(shell_line printf %s "it's #{x} \$HOME"; sh -c "$REPLY")"

# --- resurrect hook ---
typeset -gr HOOK=${0:A:h}/kitmux-resurrect-hook

# Runs the resurrect hook with args $@ on a save file holding $SAVE_LINES
# (tab-separated fields written with spaces here) and sets REPLY to the file
# afterwards, lines joined with " | ".
run_hook() {
  local file
  file=$(mktemp) || return 1
  print -rl -- ${SAVE_LINES// /$'\t'} > $file
  sh $HOOK "$@" $file
  REPLY=${(j: | :)${(f)"$(<$file)"}//$'\t'/ }
  rm -f -- $file
}

SAVE_LINES=(
  "grouped_session main__0 main :1 :0"
  "grouped_session main__12 main :0 :2"
  "grouped_session work__3 work : :0"
  "grouped_session other main : :1"
  "pane main 0 1 :* 0 host :/tmp 1 zsh :"
  "window main 0 :main__0 1 :* layout off"
  "window main__x 0 :x 1 :* layout off"
  "state main__12 main"
)
kept_main="grouped_session work__3 work : :0 | grouped_session other main : :1 | pane main 0 1 :* 0 host :/tmp 1 zsh : | window main 0 :main__0 1 :* layout off | window main__x 0 :x 1 :* layout off"
run_hook
check "hook drops main helpers by default" "$kept_main" "$REPLY"
run_hook main
check "hook drops helpers of the named session" "$kept_main" "$REPLY"
run_hook main work
check "hook drops helpers of several sessions" "${kept_main#grouped_session work__3 work : :0 | }" "$REPLY"
run_hook dev
check "hook keeps helpers of other sessions" "${(j: | :)SAVE_LINES}" "$REPLY"
SAVE_LINES=("grouped_session a.b__1 a.b : :0" "grouped_session axb__1 a.b : :0")
run_hook a.b
check "hook matches a dot in a session name literally" "grouped_session axb__1 a.b : :0" "$REPLY"

print -r -- "$checks checks, $failures failed"
(( failures == 0 ))
