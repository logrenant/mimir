#!/bin/sh
#
# Installs goat-daemon as a per-user launchd agent, so GOAT runs whether or not
# the desktop app is open.
#
# The daemon itself is unchanged by this: it still learns where to listen and
# what secret to accept from exactly two environment variables
# (GOAT_DAEMON_PORT / GOAT_DAEMON_TOKEN, internal/config/config.go). All this
# script changes is *who the parent is* — launchd instead of the Tauri shell.
#
# Idempotent. Re-running it rebuilds the binary and restarts the agent while
# keeping the existing port and token, so a running desktop app stays connected.
#
# Usage:
#   scripts/install-agent.sh [--rotate-token] [--port N] [--no-build]

set -eu

LABEL="com.goat.daemon"
SUPPORT_DIR="$HOME/Library/Application Support/goat-mcp"
BIN_DIR="$SUPPORT_DIR/bin"
ENDPOINT_FILE="$SUPPORT_DIR/endpoint.json"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG_FILE="$HOME/Library/Logs/goat-daemon.log"
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
DEFAULT_PORT=41999

ROTATE=0
BUILD=1
FORCED_PORT=""

while [ $# -gt 0 ]; do
	case "$1" in
	--rotate-token) ROTATE=1 ;;
	--no-build) BUILD=0 ;;
	--port)
		shift
		FORCED_PORT="${1:-}"
		;;
	-h | --help)
		sed -n '2,20p' "$0"
		exit 0
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
	shift
done

# --- 1. the binary ----------------------------------------------------------

if [ "$BUILD" -eq 1 ]; then
	echo "==> building goat-daemon"
	(cd "$REPO_ROOT" && make build >/dev/null)
fi

if [ ! -x "$REPO_ROOT/bin/goat-daemon" ]; then
	echo "bin/goat-daemon is missing — run 'make build' first (or drop --no-build)" >&2
	exit 1
fi

mkdir -p "$BIN_DIR"
# Copy to a temp name and move it into place: replacing a running binary
# in-place is what makes launchd's restart pick up a half-written file.
cp "$REPO_ROOT/bin/goat-daemon" "$BIN_DIR/goat-daemon.new"
chmod 0755 "$BIN_DIR/goat-daemon.new"
mv "$BIN_DIR/goat-daemon.new" "$BIN_DIR/goat-daemon"

# --- 2. port and token ------------------------------------------------------
#
# Both are reused across installs unless asked otherwise: rotating the token on
# every re-install would log out a desktop app that is holding the old one.

read_endpoint_field() {
	[ -f "$ENDPOINT_FILE" ] || return 1
	/usr/bin/sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p" "$ENDPOINT_FILE" | head -1
}

port_is_free() {
	! /usr/bin/nc -z 127.0.0.1 "$1" >/dev/null 2>&1
}

PORT=""
if [ -n "$FORCED_PORT" ]; then
	PORT="$FORCED_PORT"
else
	PORT=$(read_endpoint_field port 2>/dev/null || true)
fi

if [ -z "$PORT" ]; then
	CANDIDATE="$DEFAULT_PORT"
	while [ "$CANDIDATE" -lt $((DEFAULT_PORT + 20)) ]; do
		if port_is_free "$CANDIDATE"; then
			PORT="$CANDIDATE"
			break
		fi
		CANDIDATE=$((CANDIDATE + 1))
	done
fi

if [ -z "$PORT" ]; then
	echo "could not find a free loopback port in ${DEFAULT_PORT}-$((DEFAULT_PORT + 19)) — pass --port N" >&2
	exit 1
fi

TOKEN=""
if [ "$ROTATE" -eq 0 ]; then
	TOKEN=$(read_endpoint_field token 2>/dev/null || true)
fi
if [ -z "$TOKEN" ]; then
	TOKEN=$(/usr/bin/openssl rand -hex 32)
	echo "==> minted a new daemon token"
fi

# --- 3. the endpoint file (what the desktop app reads) ----------------------

umask 077
cat >"$ENDPOINT_FILE" <<JSON
{
  "base_url": "http://127.0.0.1:$PORT",
  "port": $PORT,
  "token": "$TOKEN"
}
JSON
chmod 0600 "$ENDPOINT_FILE"

# --- 4. the launchd agent ---------------------------------------------------
#
# PATH is the one non-obvious entry. launchd hands a process
# /usr/bin:/bin:/usr/sbin:/sbin, and both internal/refine and
# internal/coderunner resolve the `claude` CLI off PATH — without this the
# daemon starts fine and then every refine and every coding run fails.

CLAUDE_BIN=$(command -v claude 2>/dev/null || true)
CLAUDE_DIR=""
if [ -n "$CLAUDE_BIN" ]; then
	CLAUDE_DIR=$(dirname "$CLAUDE_BIN")
fi

AGENT_PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
case ":$AGENT_PATH:" in
*":$CLAUDE_DIR:"*) ;;
*) [ -n "$CLAUDE_DIR" ] && AGENT_PATH="$CLAUDE_DIR:$AGENT_PATH" ;;
esac

mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"

PLACES_ENTRY=""
if [ -n "${GOAT_GOOGLE_PLACES_API_KEY:-}" ]; then
	PLACES_ENTRY="		<key>GOAT_GOOGLE_PLACES_API_KEY</key>
		<string>$GOAT_GOOGLE_PLACES_API_KEY</string>"
	echo "==> Places key found in the environment; writing it into the agent"
fi

cat >"$PLIST" <<PLISTEOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>$LABEL</string>
	<key>ProgramArguments</key>
	<array>
		<string>$BIN_DIR/goat-daemon</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>GOAT_DAEMON_PORT</key>
		<string>$PORT</string>
		<key>GOAT_DAEMON_TOKEN</key>
		<string>$TOKEN</string>
		<key>PATH</key>
		<string>$AGENT_PATH</string>
		<key>HOME</key>
		<string>$HOME</string>
$PLACES_ENTRY
	</dict>
	<key>WorkingDirectory</key>
	<string>$HOME</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>ProcessType</key>
	<string>Adaptive</string>
	<key>StandardErrorPath</key>
	<string>$LOG_FILE</string>
	<key>StandardOutPath</key>
	<string>/dev/null</string>
</dict>
</plist>
PLISTEOF
chmod 0600 "$PLIST"

# --- 5. (re)load ------------------------------------------------------------

DOMAIN="gui/$(id -u)"

# bootout returns before the job is actually gone, and bootstrapping a label
# that is still unloading fails with "Bootstrap failed: 5: Input/output error".
# So: ask, then wait for the domain to stop knowing about it.
/bin/launchctl bootout "$DOMAIN/$LABEL" >/dev/null 2>&1 || true
WAITED=0
while /bin/launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1; do
	if [ "$WAITED" -ge 20 ]; then
		echo "$LABEL is still loaded after 10s — try 'launchctl bootout $DOMAIN/$LABEL' by hand" >&2
		exit 1
	fi
	WAITED=$((WAITED + 1))
	/bin/sleep 0.5
done

/bin/launchctl bootstrap "$DOMAIN" "$PLIST"
/bin/launchctl kickstart -k "$DOMAIN/$LABEL"

# --- 6. prove it answers ----------------------------------------------------

echo "==> waiting for http://127.0.0.1:$PORT/healthz"
ATTEMPT=0
while [ "$ATTEMPT" -lt 40 ]; do
	BODY=$(/usr/bin/curl -sf -m 2 -H "Authorization: Bearer $TOKEN" \
		"http://127.0.0.1:$PORT/healthz" 2>/dev/null || true)
	case "$BODY" in
	*'"ok":true'*)
		VERSION=$(printf '%s' "$BODY" | /usr/bin/sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
		echo "==> goat-daemon is up on port $PORT (version ${VERSION:-unknown})"
		echo "    endpoint: $ENDPOINT_FILE"
		echo "    logs:     $LOG_FILE"
		exit 0
		;;
	esac
	ATTEMPT=$((ATTEMPT + 1))
	/bin/sleep 0.5
done

echo "goat-daemon did not answer /healthz within 20s. Last 20 log lines:" >&2
/usr/bin/tail -20 "$LOG_FILE" >&2 || true
exit 1
