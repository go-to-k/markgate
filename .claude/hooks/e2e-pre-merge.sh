#!/bin/bash
# PreToolUse hook: run the full E2E verification before `gh pr merge`.
#
# Dogfoods markgate itself: the script is wrapped in `markgate run` so
# unchanged repos skip the run entirely (~0.1s skip), and a real run
# only happens when the source has changed since the last successful
# E2E pass. The marker key is `hook-e2e-pre-merge`, scoped under
# `.git/markgate/` (default state-dir, git-ignored).
#
# Triggers on Bash invocations whose command starts with `gh pr merge`.
# On test failure, prints a summary to stderr and exits 2 so Claude
# Code surfaces it as blocking feedback. On unmatched commands or a
# clean E2E (incl. cached skip), exits 0 silently.
#
# Input (stdin): the tool-use JSON from Claude Code; we need
# .tool_input.command.

set -u
input=$(cat)
command=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')

case "$command" in
  "gh pr merge"*) ;;
  *) exit 0 ;;
esac

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0

if ! go run ./cmd/markgate run hook-e2e-pre-merge -- \
     bash .claude/scripts/e2e.sh 1>&2; then
  printf '\n[claude hook] E2E suite failed before `%s`. ' "$command" >&2
  printf 'Fix the failures above (or rerun the e2e skill) before merging.\n' >&2
  exit 2
fi
exit 0
