#!/bin/bash
# PreToolUse hook: refuse mutating git commands on main.
#
# After a PR merges, local state can drift — the remote deleted the
# feature branch, but the old branch is still checked out, or a
# checkout to main slipped in unnoticed. Claude then commits to main
# locally. The project's branch-protection rule rejects the push, so
# the commit has to be unwound (cherry-pick to a new branch, reset
# main). This hook catches the mistake at commit time.
#
# Blocks `git commit`, `git push`, `git merge`, `git rebase` when the
# current branch is `main`. Exits 2 so Claude Code surfaces it as a
# blocking error and Claude sees the diagnostic. Exits 0 silently on
# non-git or off-main commands.
#
# Input (stdin): the tool-use JSON from Claude Code; we need
# .tool_input.command.

set -u
input=$(cat)
command=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')

case "$command" in
  "git commit"*|"git push"*|"git merge"*|"git rebase"*) ;;
  *) exit 0 ;;
esac

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0

branch=$(git branch --show-current 2>/dev/null || echo "")
if [ "$branch" = "main" ]; then
  printf '[claude hook] Refusing `%s` on main.\n' "$command" >&2
  printf 'Create or switch to a feature branch first (e.g. git checkout -b docs/short-name).\n' >&2
  exit 2
fi

exit 0
