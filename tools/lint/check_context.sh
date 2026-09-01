#!/usr/bin/env bash
set -e

FAILURES=$(grep -rnE 'context\.Background\(\)|context\.TODO\(\)' internal/ --include=\*.go | grep -v '_test\.go' || true)

if [ -n "$FAILURES" ]; then
  echo "ERROR: Found context.Background() or context.TODO() in non-test code inside internal/:"
  echo "$FAILURES"
  exit 1
fi

exit 0
