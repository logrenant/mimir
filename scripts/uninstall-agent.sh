#!/bin/sh
#
# Removes the mimir-daemon launchd agent.
#
# The store (~/Library/Application Support/mimir/mimir.db) is never touched:
# it holds registered projects, run history and the project memory, and losing
# it to an uninstall would be a data loss the operator did not ask for.
#
# Usage:
#   scripts/uninstall-agent.sh [--purge]
#     --purge  also remove the installed binary and the endpoint file

set -eu

LABEL="studio.mimir.daemon"
SUPPORT_DIR="$HOME/Library/Application Support/mimir"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
DOMAIN="gui/$(id -u)"
PURGE=0

while [ $# -gt 0 ]; do
	case "$1" in
	--purge) PURGE=1 ;;
	-h | --help)
		sed -n '2,13p' "$0"
		exit 0
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
	shift
done

if /bin/launchctl bootout "$DOMAIN/$LABEL" >/dev/null 2>&1; then
	echo "==> agent stopped"
else
	echo "==> agent was not loaded"
fi

if [ -f "$PLIST" ]; then
	rm -f "$PLIST"
	echo "==> removed $PLIST"
fi

if [ "$PURGE" -eq 1 ]; then
	rm -f "$SUPPORT_DIR/endpoint.json" "$SUPPORT_DIR/bin/mimir-daemon"
	echo "==> removed the endpoint file and the installed binary"
	echo "    the store at $SUPPORT_DIR/mimir.db was left alone"
fi
