#!/bin/bash
# PostToolUse hook: run `go vet ./...` after Claude edits a .go file.
#
# Dogfoods markgate itself to skip the vet run when repo state has not
# changed since the last successful vet. Runs markgate via `go run
# ./cmd/markgate` so the hook always reflects current source — no
# global install needed, no stale-binary problem. Go's content-based
# build cache keeps steady-state invocations well under 0.1s; only
# the first compile after `go clean -cache` is slow (~2-3s).
#
# On vet failure, prints the report to stderr and exits 2 so Claude
# Code surfaces it as blocking feedback (and Claude sees the
# diagnostic). On non-.go edits or clean vet, exits 0 silently.
#
# Input (stdin): the tool-use JSON from Claude Code; we only need
# .tool_input.file_path.
set -u
input=$(cat)
file=$(printf '%s' "$input" | jq -r '.tool_input.file_path // empty')
case "$file" in
  *.go) ;;
  *) exit 0 ;;
esac

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0

if ! go run ./cmd/markgate run hook-vet -- go vet ./... 1>&2; then
  printf '[claude hook] go vet or markgate failed after editing %s\n' "$file" >&2
  exit 2
fi
exit 0
