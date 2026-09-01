#!/usr/bin/env bash
set -e

FAILURES=$(grep -rnE 'fmt\.Print[fln]*\(|\bprintln\(|os\.Stdout' . --include=\*.go | grep -v 'internal/mcp/' | grep -v '_test.go' || true)

if [ -n "$FAILURES" ]; then
  echo "ERROR: Found direct stdout writes outside internal/mcp:"
  echo "$FAILURES"
  exit 1
fi

exit 0
