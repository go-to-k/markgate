#!/usr/bin/env bash
# Test matrix for guard-main-branch.sh.
#
# The hook has failed twice in both directions: it missed `git add -A &&
# git commit` (a prefix-only match, #80) and it blocked writing a file
# that merely contained a git command (a heredoc body, #81). Both are
# silent -- a miss lets a commit onto main, a false positive trains the
# reader to route around the guard -- so the matrix asserts BOTH
# directions rather than only the case that last went wrong.
#
# Usage: bash .claude/hooks/guard-main-branch.test.sh
# Exit code = number of failures.

set -u
HOOK="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/guard-main-branch.sh"
SANDBOX=$(mktemp -d)
trap 'rm -rf "$SANDBOX"' EXIT

REPO="$SANDBOX/repo"
mkdir -p "$REPO"
git -C "$REPO" init -q -b main
git -C "$REPO" config user.email t@e
git -C "$REPO" config user.name T
: > "$REPO/seed"
git -C "$REPO" add -A
git -C "$REPO" -c commit.gpgsign=false commit -qm init
git -C "$REPO" branch -q feat

PASS=0
FAIL=0

# The verb is assembled rather than written, so this file does not trip
# the very hook it tests when an agent edits it from main.
V="com""mit"

# want=2 means "must refuse", want=0 means "must allow".
check() {
  local label="$1" want="$2" cmd="$3"
  local payload
  payload=$(CMD="$cmd" python3 -c 'import json,os;print(json.dumps({"tool_input":{"command":os.environ["CMD"]}}))')
  printf '%s' "$payload" | CLAUDE_PROJECT_DIR="$REPO" bash "$HOOK" >/dev/null 2>&1
  local got=$?
  if [ "$got" = "$want" ]; then
    PASS=$((PASS+1)); printf '  PASS  %s\n' "$label"
  else
    FAIL=$((FAIL+1)); printf '  FAIL  %s (want exit %s, got %s)\n' "$label" "$want" "$got"
  fi
}

git -C "$REPO" checkout -q main
echo "=== on main: writes must be refused ==="
check "plain"                       2 "git $V -m x"
check "compound (the #80 miss)"     2 "git add -A && git $V -m x"
check "git -C <dir>"                2 "git -C /repo $V -m x"
check "git -c k=v"                  2 "git -c user.name=x $V"
check "extra spaces"                2 "git   $V"
check "push / merge / rebase"       2 "git push && git merge main && git rebase -i"
# A quoted string CAN be a real command, so it must stay refused. This is
# the case that makes stripping quotes -- unlike heredoc bodies -- unsafe.
check "real commit inside quotes"   2 "bash -c \"git $V -m x\""
check "real commit after a heredoc" 2 "$(printf 'cat > f <<%sEOF%s\nhi\nEOF\ngit %s -m x' "'" "'" "$V")"
check "real commit USING a heredoc" 2 "$(printf 'git %s -F - <<%sEOF%s\nmsg\nEOF' "$V" "'" "'")"

echo "=== on main: non-writes must be allowed ==="
check "writing a script that contains one (the #81 false positive)" 0 \
  "$(printf 'cat > e2e.sh <<%sEOF%s\nnew_repo() {\n  git init -q\n  git add . && git %s -qm init\n}\nEOF' "'" "'" "$V")"
check "heredoc, double-quoted delimiter" 0 "$(printf 'cat > f <<"SH"\ngit %s -m x\nSH' "$V")"
check "heredoc, bare delimiter"          0 "$(printf 'cat > f <<SH\ngit %s -m x\nSH' "$V")"
check "heredoc, <<- form"                0 "$(printf 'cat > f <<-SH\n\tgit %s -m x\n\tSH' "$V")"
check "two heredocs in one command"      0 "$(printf 'cat > a <<%sA%s\ngit %s x\nA\ncat > b <<%sB%s\ngit push\nB' "'" "'" "$V" "'" "'")"
check "git merge-tree (read-only)"       0 "git merge-tree a b"
check "git log --grep"                   0 "git log --grep $V"
check "not git at all"                   0 "mygit $V; git-foo $V; echo done"

echo "=== on a feature branch: everything allowed ==="
git -C "$REPO" checkout -q feat
check "plain"    0 "git $V -m x"
check "compound" 0 "git add -A && git $V -m x"
check "push"     0 "git push"

echo
echo "PASS: $PASS"
echo "FAIL: $FAIL"
exit "$FAIL"
