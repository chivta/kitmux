# kitmux target preparation. Runs on the target as
#   sh -s -- SESSION LANG MODE MIN_MAJOR MIN_MINOR
# and prints key=value lines for kitmux to read. Only sh and tmux are needed.
#
# MODE decides what happens when the session is not there:
#   start  first connect: create the session; when no tmux server was running,
#          also restore the tmux-resurrect save.
#   lost   reconnect after the connection dropped: restore when no server is
#          running (the machine rebooted), but report a missing session on a
#          running server as gone.
#   exit   reconnect after tmux detached the client itself: never create.
session=$1
LANG=$2
export LANG
mode=$3
min_major=$4
min_minor=$5
# kitmux may itself be started from inside tmux.
unset TMUX TMUX_PANE

version=$(tmux -V 2>/dev/null)
if [ -z "$version" ]; then
	echo "error=no-tmux"
	exit 0
fi
echo "version=$version"

# Refuse an old tmux before touching anything. A version without numbers is a
# development build ("tmux master") and passes.
numbers=$(printf '%s\n' "$version" | sed -n 's/^[^0-9]*\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2/p')
if [ -n "$numbers" ]; then
	major=${numbers% *}
	minor=${numbers#* }
	if [ "$major" -lt "$min_major" ] || { [ "$major" -eq "$min_major" ] && [ "$minor" -lt "$min_minor" ]; }; then
		echo "error=old-tmux"
		exit 0
	fi
fi

# Plugin scripts are looked up where TPM says they are, else in ~/.tmux/plugins.
plugin_script() {
	plugins=$(tmux show-environment -g TMUX_PLUGIN_MANAGER_PATH 2>/dev/null | sed -n 's/^TMUX_PLUGIN_MANAGER_PATH=//p')
	[ -n "$plugins" ] || plugins="$HOME/.tmux/plugins/"
	printf '%s' "${plugins%/}/$1"
}

# Quotes $1 for the shell that tmux run-shell hands its argument to.
shell_quote() {
	printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

fail() {
	echo "error=$1"
	printf '%s\n' "$2" | sed -n '1s/^/detail=/p'
	exit 0
}

server=up
out=$(tmux list-sessions -F '#{session_name}' 2>&1)
if [ $? -ne 0 ]; then
	case $out in
	*"no server running"* | *"error connecting"* | *"No such file or directory"*) server=down ;;
	*"no sessions"*) ;;
	*) fail list-sessions "$out" ;;
	esac
fi

state=ready
if [ "$server" = down ]; then
	case $mode in
	start | lost)
		# The server reads the tmux config while it starts, and continuum
		# may restore the session there already; that is not an error.
		out=$(tmux new-session -d -s "$session" 2>&1)
		if [ $? -ne 0 ]; then
			case $out in
			*"duplicate session"*) ;;
			*) fail new-session "$out" ;;
			esac
		fi
		state=created
		restore=$(plugin_script tmux-resurrect/scripts/restore.sh)
		if [ -f "$restore" ]; then
			# Only through run-shell: outside tmux the script would
			# start a server of its own.
			tmux run-shell "$(shell_quote "$restore")" >/dev/null 2>&1
			state=restored
		fi
		;;
	*) state=gone ;;
	esac
elif ! tmux has-session -t "=$session" 2>/dev/null; then
	if [ "$mode" = start ]; then
		out=$(tmux new-session -d -s "$session" 2>&1) || fail new-session "$out"
		state=created
	else
		state=gone
	fi
fi

if [ "$state" != gone ]; then
	save=$(plugin_script tmux-continuum/scripts/continuum_save.sh)
	[ -f "$save" ] && echo "continuum=$save"
fi
echo "state=$state"
