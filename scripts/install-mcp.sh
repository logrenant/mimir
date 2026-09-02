#!/bin/sh
# Register mimir-mcp with every MCP client on this machine, and install the
# session preflight hook.
#
# Usage:
#   scripts/install-mcp.sh [--no-build] [--no-hooks] [--hooks-only] [--uninstall]
#
# Idempotent: running it twice leaves the same configuration.
#
# Every client here has its own CLI for registration, so this script calls those
# rather than merging their JSON by hand. Their file shapes are theirs to
# change, and four hand-written mergers would be four things to keep in step
# with four release cycles. The hooks are the exception — neither client has a
# command for those — and they are merged with jq, atomically.

set -eu

LABEL="studio.mimir.daemon"
SERVER_NAME="mimir"
SUPPORT_DIR="$HOME/Library/Application Support/mimir"
BIN_DIR="$SUPPORT_DIR/bin"
MCP_BIN="$BIN_DIR/mimir-mcp"
PREFLIGHT="$BIN_DIR/mimir-preflight.sh"
INSTALLER_COPY="$BIN_DIR/install-mcp.sh"
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)

CLAUDE_SETTINGS="$HOME/.claude/settings.json"
AGY_HOOKS="$HOME/.gemini/config/hooks.json"

BUILD=1
HOOKS=1
INSTALL=1

while [ $# -gt 0 ]; do
	case "$1" in
	--no-build) BUILD=0 ;;
	--no-hooks) HOOKS=0 ;;
	--hooks-only) INSTALL=0 ;;
	--uninstall) INSTALL=2 ;;
	*)
		echo "unknown option: $1" >&2
		exit 2
		;;
	esac
	shift
done

say() { printf '%s\n' "$*"; }

# --- uninstall ---------------------------------------------------------------

if [ "$INSTALL" = "2" ]; then
	command -v claude >/dev/null 2>&1 && claude mcp remove -s user "$SERVER_NAME" >/dev/null 2>&1 || true
	command -v agy >/dev/null 2>&1 && agy mcp remove "$SERVER_NAME" >/dev/null 2>&1 || true
	command -v gemini >/dev/null 2>&1 && gemini mcp remove -s user "$SERVER_NAME" >/dev/null 2>&1 || true

	if [ -f "$CLAUDE_SETTINGS" ] && command -v jq >/dev/null 2>&1; then
		tmp=$(mktemp)
		jq 'if .hooks.SessionStart then
		      .hooks.SessionStart |= map(.hooks |= map(select((.command // "") | test("mimir-preflight") | not)))
		      | .hooks.SessionStart |= map(select((.hooks | length) > 0))
		    else . end
		    | if (.hooks.SessionStart? | length) == 0 then del(.hooks.SessionStart) else . end' \
			"$CLAUDE_SETTINGS" >"$tmp" && mv "$tmp" "$CLAUDE_SETTINGS"
	fi
	if [ -f "$AGY_HOOKS" ] && command -v jq >/dev/null 2>&1; then
		tmp=$(mktemp)
		jq 'del(."mimir-preflight")' "$AGY_HOOKS" >"$tmp" && mv "$tmp" "$AGY_HOOKS"
	fi

	# VS Code has --add-mcp but no --remove-mcp, so its entry is the one thing
	# here that cannot be undone from a script. Saying so is better than leaving
	# a stale entry that points at a binary this command just deleted.
	vscode_cfg="$HOME/Library/Application Support/Code/User/mcp.json"
	if [ -f "$vscode_cfg" ] && grep -q "\"$SERVER_NAME\"" "$vscode_cfg" 2>/dev/null; then
		say "VS Code has no remove command: delete the \"$SERVER_NAME\" entry from"
		say "  $vscode_cfg"
	fi

	rm -f "$MCP_BIN" "$PREFLIGHT" "$INSTALLER_COPY"
	say "Unregistered $SERVER_NAME. The store, the daemon and its launchd agent are untouched."
	exit 0
fi

# --- 1. the binary -----------------------------------------------------------
#
# Installed into the support directory rather than referenced where it was
# built. A client config naming a path inside a checkout breaks the moment the
# checkout moves, and it would break for four clients at once.

if [ "$INSTALL" = "1" ]; then
	if [ "$BUILD" = "1" ]; then
		say "Building mimir-mcp…"
		(cd "$REPO_ROOT" && go build -o bin/mimir-mcp ./cmd/mimir-mcp)
	fi
	[ -x "$REPO_ROOT/bin/mimir-mcp" ] || {
		echo "bin/mimir-mcp not found — build it first, or drop --no-build" >&2
		exit 1
	}

	mkdir -p "$BIN_DIR"
	# Copy to a temp name and move into place: replacing a binary that a client
	# may be running right now must not expose a half-written file.
	cp "$REPO_ROOT/bin/mimir-mcp" "$MCP_BIN.new"
	chmod 755 "$MCP_BIN.new"
	mv "$MCP_BIN.new" "$MCP_BIN"

	cp "$REPO_ROOT/scripts/mimir-preflight.sh" "$PREFLIGHT.new"
	chmod 755 "$PREFLIGHT.new"
	mv "$PREFLIGHT.new" "$PREFLIGHT"

	# The preflight re-registers when it finds a client config that has lost the
	# entry, so it needs this script somewhere stable too.
	cp "$REPO_ROOT/scripts/install-mcp.sh" "$INSTALLER_COPY.new"
	chmod 755 "$INSTALLER_COPY.new"
	mv "$INSTALLER_COPY.new" "$INSTALLER_COPY"

	say "Installed $MCP_BIN"
fi

# --- 2. registration ---------------------------------------------------------
#
# Each client is registered through its own CLI, at user scope where the notion
# exists. Project scope is what this repository already had, and it is the shape
# that silently stops existing the moment work moves to another directory.

register_claude() {
	command -v claude >/dev/null 2>&1 || return 1
	# `mcp add` refuses a name it already has, so removing first is what makes
	# a second run a no-op rather than an error.
	claude mcp remove -s user "$SERVER_NAME" >/dev/null 2>&1 || true
	claude mcp add -s user "$SERVER_NAME" -- "$MCP_BIN" >/dev/null 2>&1 || return 2
}

register_agy() {
	command -v agy >/dev/null 2>&1 || return 1
	# `agy mcp add` is documented as add-or-update, so it needs no removal.
	# This one registration also covers Antigravity IDE: both read
	# ~/.gemini/config/mcp_config.json.
	agy mcp add "$SERVER_NAME" "$MCP_BIN" >/dev/null 2>&1 || return 2
}

register_gemini() {
	command -v gemini >/dev/null 2>&1 || return 1
	gemini mcp remove -s user "$SERVER_NAME" >/dev/null 2>&1 || true
	gemini mcp add -s user "$SERVER_NAME" "$MCP_BIN" >/dev/null 2>&1 || return 2
}

register_vscode() {
	code_bin=$(command -v code || true)
	if [ -z "$code_bin" ]; then
		code_bin="/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"
	fi
	[ -x "$code_bin" ] || return 1
	"$code_bin" --add-mcp "{\"name\":\"$SERVER_NAME\",\"type\":\"stdio\",\"command\":\"$MCP_BIN\"}" >/dev/null 2>&1 || return 2
}

if [ "$INSTALL" = "1" ]; then
	for client in claude agy gemini vscode; do
		set +e
		"register_$client"
		rc=$?
		set -e
		case "$rc" in
		0) say "  registered with $client" ;;
		1) say "  skipped $client (not installed)" ;;
		*) say "  FAILED to register with $client — run its own 'mcp add' by hand to see why" ;;
		esac
	done
fi

# --- 3. the session preflight ------------------------------------------------

if [ "$HOOKS" = "1" ]; then
	if ! command -v jq >/dev/null 2>&1; then
		say "jq not found — skipping hook installation. Registration is done; sessions simply will not self-check."
		exit 0
	fi

	# claude: a real SessionStart event.
	if [ -d "$HOME/.claude" ]; then
		[ -f "$CLAUDE_SETTINGS" ] || printf '{}\n' >"$CLAUDE_SETTINGS"
		tmp=$(mktemp)
		# Drop any previous mimir entry before adding this one, so the hook list
		# does not grow by one on every install.
		jq --arg cmd "$PREFLIGHT --format claude" '
			.hooks //= {}
			| .hooks.SessionStart //= []
			| .hooks.SessionStart |= map(.hooks |= map(select((.command // "") | test("mimir-preflight") | not)))
			| .hooks.SessionStart |= map(select((.hooks | length) > 0))
			| .hooks.SessionStart += [{matcher: "", hooks: [{type: "command", command: $cmd, timeout: 20}]}]
		' "$CLAUDE_SETTINGS" >"$tmp" && mv "$tmp" "$CLAUDE_SETTINGS"
		say "  hook installed for claude (SessionStart)"
	fi

	# agy: no SessionStart event exists. PreInvocation fires before every model
	# call; the preflight's own invocationNum guard is what makes it behave like
	# a session hook. Named hooks are merged by agy, so writing one key here
	# cannot disturb another tool's.
	if [ -d "$HOME/.gemini/config" ]; then
		[ -f "$AGY_HOOKS" ] || printf '{}\n' >"$AGY_HOOKS"
		tmp=$(mktemp)
		jq --arg cmd "$PREFLIGHT --format agy" '
			."mimir-preflight" = {PreInvocation: [{type: "command", command: $cmd, timeout: 20}]}
		' "$AGY_HOOKS" >"$tmp" && mv "$tmp" "$AGY_HOOKS"
		say "  hook installed for agy (PreInvocation)"
	fi
fi

say ""
say "Done. Open a new session in any client and it will check itself before it starts."
