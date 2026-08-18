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

# Matched anywhere in the command, not only at the start: these arrive
# inside a compound far more often than alone
# (`git add -A && git commit ...`), and a prefix-only match let exactly
# that through once. The global-option run covers `git -C <dir> commit`
# and `git -c k=v commit`, which is how the mistake reaches a repo the
# shell is not sitting in -- the situation that caused it. Requiring a
# word boundary after the verb keeps read-only lookalikes out
# (`git merge-tree`), and anchoring the left side keeps `mygit commit`
# and `git log --grep commit` out.
git_write_verb='(^|[^[:alnum:]_./-])git(( +-[cC] +[^ ]+)|( +--[a-z][a-z-]*(=[^ ]+)?)|( +-[a-zA-Z]))* +(commit|push|merge|rebase)([[:space:]]|$)'
if ! printf '%s' "$command" | grep -Eq "$git_write_verb"; then
  exit 0
fi

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0

branch=$(git branch --show-current 2>/dev/null || echo "")
if [ "$branch" = "main" ]; then
  printf '[claude hook] Refusing `%s` on main.\n' "$command" >&2
  printf 'Create or switch to a feature branch first (e.g. git checkout -b docs/short-name).\n' >&2
  exit 2
fi

exit 0
