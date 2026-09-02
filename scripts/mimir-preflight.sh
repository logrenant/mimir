#!/bin/sh
# Session preflight: make sure Mimir is there before a session starts spending
# tokens rediscovering what it already knows.
#
# Wired to two events, because the two clients do not have the same one:
#   claude  → hooks.SessionStart in ~/.claude/settings.json
#   agy     → PreInvocation in ~/.gemini/config/hooks.json, guarded on
#             invocationNum so it does real work once per conversation
#
# It does four things and then gets out of the way: check the installed binary,
# re-register if the client's config lost the entry, health-check the daemon and
# restart it if it is down, and inject one short line telling the model that a
# memory exists.
#
# Two rules govern everything below.
#
# There is deliberately no `set -e`. A hook that aborts before printing its
# envelope is worse than one that reports a problem — the client gets no output
# at all, and on the agy side an empty stdout is not a valid response. Every
# path here ends in an envelope and exit 0.
#
# Nothing but the envelope goes to stdout. Diagnostics go to stderr, which the
# clients log and neither of them parses.

set -u

FORMAT=""
while [ $# -gt 0 ]; do
	case "$1" in
	--format)
		shift
		FORMAT="${1:-}"
		;;
	*) ;;
	esac
	shift || true
done

SUPPORT_DIR="$HOME/Library/Application Support/mimir"
BIN_DIR="$SUPPORT_DIR/bin"
MCP_BIN="$BIN_DIR/mimir-mcp"
ENDPOINT_FILE="$SUPPORT_DIR/endpoint.json"
LABEL="studio.mimir.daemon"

# The payload is read whether or not it is used: leaving it in the pipe risks
# the caller seeing a write error instead of our answer.
PAYLOAD=$(cat 2>/dev/null || true)

log() { printf '%s\n' "mimir-preflight: $*" >&2; }

# emit prints the envelope this client understands and exits.
#
# jq builds it because the note carries a path and a daemon's own error text,
# and hand-escaping those into JSON is exactly the kind of thing that works
# until a directory has a quote in it. Without jq we still answer — with the
# empty envelope, which is valid for both clients — rather than emitting
# something malformed.
emit() {
	note="$1"
	if ! command -v jq >/dev/null 2>&1; then
		[ "$FORMAT" = "agy" ] && printf '{}\n'
		exit 0
	fi
	case "$FORMAT" in
	agy)
		jq -nc --arg m "$note" '{injectSteps: [{ephemeralMessage: $m}]}'
		;;
	claude)
		jq -nc --arg m "$note" \
			'{hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: $m}}'
		;;
	*)
		printf '{}\n'
		;;
	esac
	exit 0
}

# agy has no SessionStart event, so this rides PreInvocation — which fires
# before every model call, not once per conversation. invocationNum is what
# turns one into the other; without this guard the checks below would run on
# every turn.
if [ "$FORMAT" = "agy" ]; then
	invocation=$(printf '%s' "$PAYLOAD" | sed -n 's/.*"invocationNum"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p')
	if [ -n "$invocation" ] && [ "$invocation" -gt 1 ] 2>/dev/null; then
		printf '{}\n'
		exit 0
	fi
fi

# 1. The binary the clients were pointed at.
if [ ! -x "$MCP_BIN" ]; then
	log "$MCP_BIN is missing"
	emit "Mimir is not installed on this machine: $MCP_BIN is missing. Run \`make install-mcp\` in the mimir checkout. Its memory and knowledge-base tools are unavailable for this session."
fi

# 2. The registration, read from the client's own config rather than by asking
#    the client — spawning it to interrogate itself would cost more than the
#    check is worth, on every session.
registration_present() {
	case "$FORMAT" in
	claude) grep -q '"mimir"' "$HOME/.claude.json" 2>/dev/null ;;
	agy) grep -q '"mimir"' "$HOME/.gemini/config/mcp_config.json" 2>/dev/null ;;
	*) return 0 ;;
	esac
}

if ! registration_present; then
	log "no mimir entry in this client's config; re-registering"
	installer="$BIN_DIR/install-mcp.sh"
	if [ -x "$installer" ]; then
		"$installer" --no-build --no-hooks >/dev/null 2>&1 || log "re-registration failed"
	else
		log "$installer is missing; cannot re-register"
	fi
fi

# 3. The daemon. mimir-mcp opens the store itself and does not need it, so a
#    dead daemon costs the desktop app and coding runs, not this session's
#    memory — which is why nothing below is fatal.
daemon_note=""
if [ -r "$ENDPOINT_FILE" ]; then
	base_url=$(sed -n 's/.*"base_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$ENDPOINT_FILE")
	token=$(sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$ENDPOINT_FILE")

	healthy() {
		[ -n "$base_url" ] || return 1
		curl -fsS -m 2 -o /dev/null -H "Authorization: Bearer $token" "$base_url/healthz" 2>/dev/null
	}

	if ! healthy; then
		log "daemon not answering at $base_url; kickstarting $LABEL"
		launchctl kickstart -k "gui/$(id -u)/$LABEL" >/dev/null 2>&1 || true

		# Ten half-second looks. Long enough for a cold start, short enough that
		# a session never waits on a daemon that is not coming back.
		i=0
		while [ "$i" -lt 10 ]; do
			if healthy; then
				break
			fi
			sleep 0.5
			i=$((i + 1))
		done

		if healthy; then
			log "daemon recovered"
		else
			daemon_note=" The Mimir daemon is not answering and could not be restarted; run \`make agent-status\` and \`make agent-logs\` in the mimir checkout. Memory and knowledge-base tools still work — they read the store directly — but coding runs and the desktop app do not."
		fi
	fi
else
	daemon_note=" The Mimir daemon is not installed (no endpoint.json); run \`make install-agent\` in the mimir checkout if you want coding runs and the desktop app."
fi

emit "Mimir MCP is available in this session. Before exploring a repository you have worked in before, call \`project_context\` — it returns what earlier sessions established and replaces re-reading the repo to rediscover its shape. Use \`brain_query_nodes\` for what is already known about a library, a decision or a topic, across projects.${daemon_note}"
