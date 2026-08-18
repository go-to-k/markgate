#!/usr/bin/env bash
# End-to-end verification of the markgate CLI surface.
#
# Covers (a) the original primitives — set / verify / clear / run /
# init / version, default key, --hash files + --include, --state-dir
# override, env-var override — (b) the six features added in the
# 2026-05-09 batch: shell completion, config lint, TTL, --explain,
# bare status, and gate dependencies (composes / requires) — and
# (c) the hash: diff strategy (#68): base-branch churn staying fresh,
# same-file churn going stale, the empty-delta and unresolvable-base
# refusals, flag and config validation -- (d) the dead-include refusal
# shared by both scoped modes (#70) -- and (e) clear surviving an
# invalid config (#77).
#
# The assertion COUNT is printed by the summary and deliberately not
# repeated in prose anywhere: it drifted by 14 the last time it was.
#
# This script is invoked manually via the `verify-e2e` skill and
# automatically by the e2e-pre-merge hook (which wraps it in
# `markgate run` so unchanged repos skip the run).
#
# Usage:
#   bash .claude/scripts/e2e.sh           # full run, verbose
#   QUIET=1 bash .claude/scripts/e2e.sh   # only print summary + failures
#
# Exit code: 0 on all-pass, non-zero = number of failed assertions.

set -u

ROOT=${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}
BUILD_DIR=$(mktemp -d /tmp/markgate-e2e-build.XXXXXX)
SANDBOX=$(mktemp -d /tmp/markgate-e2e.XXXXXX)
trap 'rm -rf "$BUILD_DIR" "$SANDBOX"' EXIT

PASS=0
FAIL=0
FAIL_LOG=()

cyan()  { printf "\033[36m%s\033[0m\n" "$*"; }
green() { [ "${QUIET:-0}" = "1" ] || printf "\033[32m%s\033[0m\n" "$*"; }
red()   { printf "\033[31m%s\033[0m\n" "$*"; }

assert_eq() {
  local label="$1" want="$2" got="$3"
  if [[ "$got" == "$want" ]]; then
    PASS=$((PASS+1)); green "  PASS  $label  (got=$got)"
  else
    FAIL=$((FAIL+1)); red "  FAIL  $label  want=$want got=$got"
    FAIL_LOG+=("$label: want=$want got=$got")
  fi
}

assert_contains() {
  local label="$1" needle="$2" hay="$3"
  if [[ "$hay" == *"$needle"* ]]; then
    PASS=$((PASS+1)); green "  PASS  $label  (contains: $needle)"
  else
    FAIL=$((FAIL+1)); red "  FAIL  $label  missing: $needle"
    red "    hay: ${hay:0:200}"
    FAIL_LOG+=("$label: missing $needle")
  fi
}

assert_file() {
  local label="$1" path="$2"
  if [[ -f "$path" ]]; then
    PASS=$((PASS+1)); green "  PASS  $label  (file exists)"
  else
    FAIL=$((FAIL+1)); red "  FAIL  $label  missing file: $path"
    FAIL_LOG+=("$label: missing file $path")
  fi
}

assert_absent() {
  local label="$1" path="$2"
  if [[ ! -e "$path" ]]; then
    PASS=$((PASS+1)); green "  PASS  $label  (absent)"
  else
    FAIL=$((FAIL+1)); red "  FAIL  $label  unexpectedly present: $path"
    FAIL_LOG+=("$label: unexpectedly present $path")
  fi
}

# Build once, reuse for all assertions.
cyan "=== build ==="
cd "$ROOT"
if ! go build -o "$BUILD_DIR/markgate" ./cmd/markgate 2>&1; then
  red "build failed"
  exit 1
fi
MG="$BUILD_DIR/markgate"
green "  built $MG"

# Fresh repo per section; markers from one section never leak into the next.
new_repo() {
  cd /tmp
  rm -rf "$SANDBOX"
  mkdir -p "$SANDBOX"
  cd "$SANDBOX"
  git init -q -b main
  git config user.email t@e
  git config user.name T
  mkdir -p src docs
  echo "package main" > src/a.go
  echo "x" > docs/README.md
  git add . && git commit -qm init
}

# ─────────────────────────────────────────────────────────────────
# Pre-existing CLI surface — verify the basics still work after the
# batch of 6 features. Regression-detection net.
# ─────────────────────────────────────────────────────────────────

cyan "=== core: set / verify / clear cycle ==="
new_repo

$MG verify check >/dev/null 2>&1
assert_eq "verify before set exit=1 (no marker)" "1" "$?"

$MG set check >/dev/null 2>&1
assert_eq "set exit=0" "0" "$?"

$MG verify check >/dev/null 2>&1
assert_eq "verify after set exit=0 (match)" "0" "$?"

echo "edited" > src/a.go
$MG verify check >/dev/null 2>&1
assert_eq "verify after edit exit=1 (mismatch)" "1" "$?"

$MG clear check >/dev/null 2>&1
assert_eq "clear exit=0" "0" "$?"

$MG verify check >/dev/null 2>&1
assert_eq "verify after clear exit=1 (no marker)" "1" "$?"

# clear is idempotent
$MG clear check >/dev/null 2>&1
assert_eq "clear-of-missing is idempotent exit=0" "0" "$?"

cyan "=== core: default key (no positional arg) ==="
new_repo

$MG set >/dev/null 2>&1
assert_eq "set with no key uses 'default' exit=0" "0" "$?"
$MG verify >/dev/null 2>&1
assert_eq "verify with no key exit=0" "0" "$?"
out=$($MG status default 2>&1)
assert_contains "status default produces match line" "match" "$out"

cyan "=== core: --hash files + --include ==="
new_repo

# files hasher requires --include
$MG set check --hash files >/dev/null 2>&1
assert_eq "files hash without include exit=2" "2" "$?"

$MG set check --hash files --include "src/**" >/dev/null 2>&1
assert_eq "files hash with include exit=0" "0" "$?"

$MG verify check --hash files --include "src/**" >/dev/null 2>&1
assert_eq "files hash verify exit=0" "0" "$?"

# Edit outside scope: still match
echo "outside scope" > docs/README.md
$MG verify check --hash files --include "src/**" >/dev/null 2>&1
assert_eq "files hash ignores out-of-scope edits exit=0" "0" "$?"

# Edit inside scope: mismatch
echo "in scope" > src/a.go
$MG verify check --hash files --include "src/**" >/dev/null 2>&1
assert_eq "files hash detects in-scope edit exit=1" "1" "$?"

cyan "=== core: --state-dir override ==="
new_repo
STATE_DIR=$(mktemp -d /tmp/markgate-e2e-state.XXXXXX)

$MG set check --state-dir "$STATE_DIR" >/dev/null 2>&1
assert_file "marker written to --state-dir" "$STATE_DIR/check.json"

# Default state-dir should NOT have the marker
[ ! -f .git/markgate/check.json ]
assert_eq "marker NOT in default location when --state-dir set" "0" "$?"

$MG verify check --state-dir "$STATE_DIR" >/dev/null 2>&1
assert_eq "verify finds marker in --state-dir" "0" "$?"
rm -rf "$STATE_DIR"

cyan "=== core: env var MARKGATE_STATE_DIR ==="
new_repo
STATE_DIR=$(mktemp -d /tmp/markgate-e2e-state.XXXXXX)

MARKGATE_STATE_DIR="$STATE_DIR" $MG set check >/dev/null 2>&1
assert_file "marker written when MARKGATE_STATE_DIR set" "$STATE_DIR/check.json"
MARKGATE_STATE_DIR="$STATE_DIR" $MG verify check >/dev/null 2>&1
assert_eq "verify reads marker from env-var dir" "0" "$?"
rm -rf "$STATE_DIR"

cyan "=== core: precedence (flag > env > config > default) ==="
new_repo
FLAG_DIR=$(mktemp -d /tmp/markgate-e2e-flag.XXXXXX)
ENV_DIR=$(mktemp -d /tmp/markgate-e2e-env.XXXXXX)
cat > .markgate.yml <<EOF
gates:
  check:
    hash: git-tree
    state_dir: ".cfg-state"
EOF

# flag wins over env wins over config
MARKGATE_STATE_DIR="$ENV_DIR" $MG set check --state-dir "$FLAG_DIR" >/dev/null 2>&1
assert_file "flag wins: marker in --state-dir"  "$FLAG_DIR/check.json"
[ ! -f "$ENV_DIR/check.json" ]
assert_eq "flag wins: marker NOT in env dir" "0" "$?"
[ ! -f .cfg-state/check.json ]
assert_eq "flag wins: marker NOT in config dir" "0" "$?"

# env wins over config
MARKGATE_STATE_DIR="$ENV_DIR" $MG set check >/dev/null 2>&1
assert_file "env wins: marker in env dir" "$ENV_DIR/check.json"

# config wins over default
$MG set check >/dev/null 2>&1
assert_file "config wins: marker in config-relative dir" ".cfg-state/check.json"
rm -rf "$FLAG_DIR" "$ENV_DIR"

cyan "=== core: run (verify + child + set sugar) ==="
new_repo

# First call: no marker → child runs → on success set
out=$($MG run check -- echo hello 2>&1)
assert_eq "run no-marker exit=0" "0" "$?"
assert_contains "run executes child on miss" "hello" "$out"

# Second call: matched → child must NOT run
out=$($MG run check -- echo SHOULD-NOT-PRINT 2>&1)
assert_eq "run match exit=0 (skip)" "0" "$?"
[[ "$out" != *"SHOULD-NOT-PRINT"* ]]
assert_eq "run skips child on match" "0" "$?"

# Mutation → child runs again
echo "edit" > src/a.go
out=$($MG run check -- echo ran-again 2>&1)
assert_eq "run after edit exit=0" "0" "$?"
assert_contains "run executes child on mismatch" "ran-again" "$out"

# Child failure: exit propagates, marker NOT updated
echo "edit2" > src/a.go
$MG run check -- bash -c "exit 7" >/dev/null 2>&1
assert_eq "run propagates child exit code" "7" "$?"
$MG verify check >/dev/null 2>&1
assert_eq "marker NOT advanced on child failure (verify still mismatch)" "1" "$?"

cyan "=== core: init / version ==="
new_repo

$MG init >/dev/null 2>&1
assert_eq "init exit=0" "0" "$?"
assert_file "init writes .markgate.yml" ".markgate.yml"

# init is non-clobbering
echo "# user customization" >> .markgate.yml
out_before=$(cat .markgate.yml)
$MG init >/dev/null 2>&1
out_after=$(cat .markgate.yml)
assert_eq "init does not clobber existing config" "$out_before" "$out_after"

out=$($MG version 2>&1)
assert_contains "version prints something non-empty" "." "$out"

# ─────────────────────────────────────────────────────────────────
# Features added 2026-05-09 (new batch)
# ─────────────────────────────────────────────────────────────────

cyan "=== #25 shell completion ==="
new_repo

out=$($MG completion bash 2>/dev/null | head -3)
assert_contains "completion bash emits script" "bash completion" "$out"

$MG completion totallybogus >/dev/null 2>&1
assert_eq "completion unknown shell exit=2" "2" "$?"

cat > .markgate.yml <<'EOF'
gates:
  alpha: { hash: git-tree }
  beta:  { hash: git-tree }
EOF
out=$($MG __complete verify "" 2>/dev/null)
assert_contains "completion lists alpha" "alpha" "$out"
assert_contains "completion lists beta"  "beta"  "$out"

cyan "=== #26 config lint ==="
new_repo

cat > .markgate.yml <<'EOF'
gates:
  check:
    hash: files
    include: ["src/**"]
EOF
$MG config lint >/dev/null 2>&1
assert_eq "lint clean exit=0" "0" "$?"

cat > .markgate.yml <<'EOF'
gates:
  docs:
    hash: files
    include: ["README.md", "docss/**"]
    legacy_field: 1
weird_top: 1
EOF
echo x > README.md
out=$($MG config lint 2>&1)
code=$?
assert_eq "lint dirty exit=1" "1" "$code"
assert_contains "lint flags dead glob"      "docss/**"     "$out"
assert_contains "lint flags unknown gate"   "legacy_field" "$out"
assert_contains "lint flags unknown top"    "weird_top"    "$out"

out=$($MG config lint --json 2>&1)
assert_contains "lint --json has path field"     '"path"'     "$out"
assert_contains "lint --json has severity field" '"severity"' "$out"

# Malformed glob (unmatched `[`) is a config error, not a finding —
# must surface as exit 2, not silently pass.
cat > .markgate.yml <<'EOF'
gates:
  bad:
    hash: files
    include: ["src/[unclosed"]
EOF
$MG config lint >/dev/null 2>&1
assert_eq "lint malformed glob exit=2" "2" "$?"

# Lint surfaces every rule that would make `markgate run` exit 2 (the
# config.Validate-shared rules — undeclared refs, both-set, unknown
# hash, ttl parse, cycle), so a clean lint means the config will load.
cat > .markgate.yml <<'EOF'
gates:
  check:
    hash: files
    include: ["src/**"]
  docs:
    hash: files
    include: ["src/**"]
  verify-pr:
    requires: [check, doaaaaaaacs]
EOF
mkdir -p src && echo x > src/a.go
out=$($MG config lint 2>&1)
code=$?
assert_eq "lint undeclared ref exit=1" "1" "$code"
assert_contains "lint flags undeclared ref" "doaaaaaaacs" "$out"

cat > .markgate.yml <<'EOF'
gates:
  cache:
    hash: files
    include: ["src/**"]
    ttl: 5xs
EOF
out=$($MG config lint 2>&1)
code=$?
assert_eq "lint ttl parse exit=1" "1" "$code"
assert_contains "lint flags ttl parse error" "gates.cache.ttl" "$out"

cyan "=== #34 TTL ==="
new_repo

cat > .markgate.yml <<'EOF'
gates:
  integ:
    hash: git-tree
    ttl: 2s
EOF
$MG set integ >/dev/null 2>&1
$MG verify integ >/dev/null 2>&1
assert_eq "fresh marker verify exit=0" "0" "$?"

sleep 3
out=$($MG verify integ 2>&1)
code=$?
assert_eq "expired marker verify exit=1" "1" "$code"
assert_contains "expired marker stderr message" "expired by ttl" "$out"

$MG set integ >/dev/null 2>&1
$MG verify integ >/dev/null 2>&1
assert_eq "set resets countdown verify exit=0" "0" "$?"

cat > .markgate.yml <<'EOF'
gates:
  bad: { ttl: 1mo }
EOF
$MG verify bad >/dev/null 2>&1
assert_eq "1mo rejected at config load exit=2" "2" "$?"

# TTL propagates through composes chain: child's expired TTL must
# fail the parent even when parent's own marker is fresh.
new_repo

cat > .markgate.yml <<'EOF'
gates:
  child:
    hash: git-tree
    ttl: 2s
  parent:
    composes: [child]
EOF
$MG set child >/dev/null 2>&1
$MG set parent >/dev/null 2>&1
$MG verify parent >/dev/null 2>&1
assert_eq "parent fresh while child fresh exit=0" "0" "$?"
sleep 3
$MG verify parent >/dev/null 2>&1
assert_eq "parent inherits child TTL expiry exit=1" "1" "$?"

cyan "=== #31 verify --explain ==="
new_repo

cat > .markgate.yml <<'EOF'
gates:
  check:
    hash: files
    include: ["src/**"]
    exclude: ["**/*_test.go"]
EOF

stderr_file=$(mktemp)
$MG verify check -e 2>"$stderr_file" >/dev/null
code=$?
err=$(cat "$stderr_file")
rm -f "$stderr_file"
assert_eq "explain exit code unchanged on no-marker" "1" "$code"
assert_contains "explain prints scope: header" "scope:"   "$err"
assert_contains "explain lists src/a.go"       "src/a.go" "$err"
assert_contains "explain prints state line"    "state:"   "$err"

$MG set check >/dev/null 2>&1
out=$($MG verify check --explain --json 2>/dev/null)
# JSON is pretty-printed; match including the space after `:`.
assert_contains "explain --json has key field"    '"key": "check"'    "$out"
assert_contains "explain --json has hasher field" '"hasher": "files"' "$out"
assert_contains "explain --json has scope array"  '"scope"'           "$out"
assert_contains "explain --json reports match"    '"state": "match"'  "$out"

cyan "=== #33 bare status ==="
new_repo

cat > .markgate.yml <<'EOF'
gates:
  alpha: { hash: git-tree }
  beta:  { hash: git-tree }
EOF
$MG set alpha >/dev/null 2>&1
$MG set stray >/dev/null 2>&1   # marker for an unconfigured key

out=$($MG status 2>&1)
code=$?
assert_eq "bare status exit=1 (beta missing)" "1" "$code"
assert_contains "bare status header KEY"        "KEY"            "$out"
assert_contains "bare status header STATE"      "STATE"          "$out"
assert_contains "bare status lists alpha"       "alpha"          "$out"
assert_contains "bare status lists beta"        "beta"           "$out"
assert_contains "bare status lists stray"       "stray"          "$out"
assert_contains "beta has (configured) note"    "(configured)"   "$out"
assert_contains "stray has (unconfigured) note" "(unconfigured)" "$out"

out=$($MG status --json 2>/dev/null)
assert_contains "bare status --json is array"          '['          "$out"
assert_contains "bare status --json snake_case key"    '"key"'      "$out"
assert_contains "bare status --json snake_case marker" '"marker"'   "$out"
assert_contains "bare status --json hash_type"         '"hash_type"' "$out"

# Single-key path still works (backwards compat).
$MG status alpha >/dev/null 2>&1
assert_eq "status alpha (single-key) exit=0" "0" "$?"

cyan "=== #28 composes (loose) ==="
new_repo

cat > .markgate.yml <<'EOF'
gates:
  child-a: { hash: files, include: ["src/**"] }
  child-b: { hash: files, include: ["docs/**"] }
  pr:
    composes: [child-a, child-b]
EOF
$MG set child-a >/dev/null 2>&1
$MG set child-b >/dev/null 2>&1
$MG set pr >/dev/null 2>&1
assert_eq "composes parent set is unconditional" "0" "$?"

$MG verify pr >/dev/null 2>&1
assert_eq "composes parent verify all-match exit=0" "0" "$?"

echo y >> docs/README.md
$MG verify pr >/dev/null 2>&1
assert_eq "composes parent verify child-stale exit=1" "1" "$?"

cyan "=== #28 requires (strict) ==="
new_repo

cat > .markgate.yml <<'EOF'
gates:
  migration: { hash: files, include: ["src/**"] }
  deploy:
    requires: [migration]
EOF
$MG set migration >/dev/null 2>&1
$MG set deploy >/dev/null 2>&1
assert_eq "requires set passes when child fresh" "0" "$?"

echo "package main // edit" > src/a.go
out=$($MG set deploy 2>&1)
code=$?
assert_eq "requires set refuses stale-child exit=2" "2" "$code"
assert_contains "requires error names offending child" "migration" "$out"

cyan "=== #28 composes + parent include ==="
new_repo
cat > .markgate.yml <<'EOF'
gates:
  child: { hash: files, include: ["src/**"] }
  pr:
    hash: files
    include: ["docs/**"]
    composes: [child]
EOF
$MG set child >/dev/null 2>&1
$MG set pr >/dev/null 2>&1
$MG verify pr >/dev/null 2>&1
assert_eq "composes+include verify all-match exit=0" "0" "$?"

# Parent's own scope changes → verify mismatch (child still fresh).
echo z >> docs/README.md
$MG verify pr >/dev/null 2>&1
assert_eq "composes+include verify parent-scope-stale exit=1" "1" "$?"

cyan "=== #28 requires + parent include ==="
new_repo
cat > .markgate.yml <<'EOF'
gates:
  migration: { hash: files, include: ["src/**"] }
  deploy:
    hash: files
    include: ["docs/**"]
    requires: [migration]
EOF
$MG set migration >/dev/null 2>&1
$MG set deploy >/dev/null 2>&1
$MG verify deploy >/dev/null 2>&1
assert_eq "requires+include verify all-match exit=0" "0" "$?"

# Parent's own scope changes → verify mismatch.
echo z >> docs/README.md
$MG verify deploy >/dev/null 2>&1
assert_eq "requires+include verify parent-scope-stale exit=1" "1" "$?"

# set parent ignores parent's own digest — succeeds with fresh child.
$MG set deploy >/dev/null 2>&1
assert_eq "requires+include set parent succeeds with fresh child" "0" "$?"

# Stale child → set parent refused (regardless of parent's own include).
echo w >> src/a.go
out=$($MG set deploy 2>&1)
code=$?
assert_eq "requires+include set refuses with stale child exit=2" "2" "$code"
assert_contains "requires+include set error names child" "migration" "$out"

cyan "=== #28 config-load errors ==="

cat > .markgate.yml <<'EOF'
gates:
  a: { composes: [b] }
  b: { composes: [a] }
EOF
out=$($MG verify a 2>&1)
code=$?
assert_eq "cycle rejected at config load exit=2" "2" "$code"
assert_contains "cycle error message" "cycle" "$out"

cat > .markgate.yml <<'EOF'
gates:
  a: { composes: [missing] }
EOF
$MG verify a >/dev/null 2>&1
assert_eq "missing-child rejected exit=2" "2" "$?"

cat > .markgate.yml <<'EOF'
gates:
  a:
    composes: [b]
    requires: [c]
  b: {}
  c: {}
EOF
$MG verify a >/dev/null 2>&1
assert_eq "composes+requires rejected exit=2" "2" "$?"

cyan "=== #68 hash: diff (base-branch churn) ==="
new_repo

printf 'l1\nl2\nl3\nl4\nl5\n' > src/a.go
printf 'b1\nb2\nb3\nb4\nb5\n' > src/b.go
cat > .markgate.yml <<'EOF'
gates:
  integ:
    hash: diff
    base: main
    include:
      - "src/**"
EOF
git add -A && git commit -qm "diff fixture" >/dev/null
git checkout -q -b feat
printf 'MINE\nl2\nl3\nl4\nl5\n' > src/a.go
git commit -qam "my work" >/dev/null

$MG set integ >/dev/null 2>&1
assert_eq "diff: set on a branch ahead of base exit=0" "0" "$?"

$MG verify integ >/dev/null 2>&1
assert_eq "diff: verify after set exit=0" "0" "$?"

out=$($MG status integ 2>&1)
assert_contains "diff: status records the base ref" "base:       main" "$out"
assert_contains "diff: status records the merge base" "merge base:" "$out"

# An unrelated in-scope file changes on the base branch and is merged in.
git checkout -q main
printf 'b1\nb2\nb3\nb4\nBASE\n' > src/b.go
git commit -qam "unrelated base work" >/dev/null
git checkout -q feat
git merge -q --no-edit main >/dev/null
$MG verify integ >/dev/null 2>&1
assert_eq "diff: unrelated base-branch change stays fresh exit=0" "0" "$?"

# The base branch touches a file this branch also changed.
git checkout -q main
printf 'l1\nl2\nl3\nl4\nBASE\n' > src/a.go
git commit -qam "base touches my file" >/dev/null
git checkout -q feat
git merge -q --no-edit main >/dev/null
$MG verify integ >/dev/null 2>&1
assert_eq "diff: same-file base-branch change goes stale exit=1" "1" "$?"

$MG set integ >/dev/null 2>&1
printf 'l1\nl2\nEDITED\nl4\nl5\n' > src/a.go
$MG verify integ >/dev/null 2>&1
assert_eq "diff: uncommitted in-scope edit exit=1" "1" "$?"

$MG set integ >/dev/null 2>&1
echo "out of scope" > docs/README.md
$MG verify integ >/dev/null 2>&1
assert_eq "diff: out-of-scope edit stays fresh exit=0" "0" "$?"

echo "new file" > src/untracked.go
$MG verify integ >/dev/null 2>&1
assert_eq "diff: untracked in-scope file exit=1" "1" "$?"
rm src/untracked.go
git checkout -q -- docs/README.md src/a.go

# Clean base-branch checkout: empty delta, constant digest -> refuse.
git checkout -q main
out=$($MG verify integ 2>&1)
code=$?
assert_eq "diff: clean base branch errors exit=2" "2" "$code"
assert_contains "diff: base-branch error names hash=diff" "hash=diff" "$out"
out=$($MG set integ 2>&1)
assert_eq "diff: set on the base branch errors exit=2" "2" "$?"

# Bare status keeps listing instead of aborting on the unusable gate:
# the healthy gate must still appear alongside the refusing one.
$MG set healthy --hash files --include "src/**" >/dev/null 2>&1
out=$($MG status 2>&1)
assert_contains "diff: bare status still renders the diff row" "integ" "$out"
assert_contains "diff: bare status keeps rendering other gates" "healthy" "$out"

git checkout -q feat
out=$($MG verify integ --base origin/never-fetched 2>&1)
assert_eq "diff: unresolvable base errors exit=2" "2" "$?"
assert_contains "diff: unresolvable base names the ref" "never-fetched" "$out"

$MG set flagged --hash diff --base main >/dev/null 2>&1
assert_eq "diff: --hash diff --base flags exit=0" "0" "$?"
out=$($MG set flagless --hash diff 2>&1)
assert_eq "diff: --hash diff without --base exit=2" "2" "$?"
# Names the flag, so the assertion still fails if the eager validation
# is removed and the error arrives later from merge-base resolution.
assert_contains "diff: missing --base names the flag" "requires --base" "$out"
$MG set nondiff --base main >/dev/null 2>&1
assert_eq "diff: --base without hash=diff exit=2" "2" "$?"

cat > .markgate.yml <<'EOF'
gates:
  integ:
    hash: diff
    include:
      - "src/**"
EOF
$MG verify integ >/dev/null 2>&1
assert_eq "diff: config without base rejected exit=2" "2" "$?"

cat > .markgate.yml <<'EOF'
gates:
  scan:
    hash: files
    base: main
    include:
      - "src/**"
EOF
out=$($MG config lint 2>&1)
assert_eq "diff: base on a files gate lints dirty exit=1" "1" "$?"
assert_contains "diff: lint explains base misuse" "base is only valid with hash=diff" "$out"

# The mirror case: a deps-only gate discards hash/base instead, so it is
# the same config error seen from the other side (#71). The unresolvable
# base is deliberate — nothing may resolve it, which is the point.
cat > .markgate.yml <<'EOF'
gates:
  child:
    hash: files
    include:
      - "src/**"
  parent:
    hash: diff
    base: origin/never-resolves
    composes: [child]
EOF
out=$($MG set parent 2>&1)
assert_eq "diff: deps-only gate rejects hash=diff exit=2" "2" "$?"
assert_contains "diff: deps-only rejection names the scope" "requires its own scope" "$out"

# Adding include gives the same gate its own scope back, so the rule is
# about scope rather than about having children at all.
cat > .markgate.yml <<'EOF'
gates:
  child:
    hash: files
    include:
      - "src/**"
  parent:
    hash: diff
    base: main
    include:
      - "src/**"
    composes: [child]
EOF
$MG config lint >/dev/null 2>&1
assert_eq "diff: scoped gate with composes lints clean exit=0" "0" "$?"
cyan "=== #70 dead include glob (both scoped modes) ==="
new_repo

printf 'a\n' > src/a.go
git add -A && git commit -qm "dead-glob fixture" >/dev/null
git checkout -q -b feat
printf 'mine\n' > src/a.go
git commit -qam "my work" >/dev/null

# A scope that cannot exist would digest to the constant SHA-256 of the
# empty set, so the gate would report match forever.
out=$($MG set typo --hash files --include "scr/**" 2>&1)
assert_eq "dead glob: files set exit=2" "2" "$?"
assert_contains "dead glob: error names the pattern" "scr/**" "$out"
$MG set typo --hash diff --base main --include "scr/**" >/dev/null 2>&1
assert_eq "dead glob: diff set exit=2" "2" "$?"
assert_absent "dead glob: no marker recorded" ".git/markgate/typo.json"

# The other empty scope: patterns are live, this branch just changed
# nothing under them. Refusing here would break the gate on exactly the
# branches it should trivially pass.
$MG set quiet --hash diff --base main --include "docs/**" >/dev/null 2>&1
assert_eq "dead glob: quiet branch with a live include exit=0" "0" "$?"
$MG set excluded --hash files --include "src/**" --exclude "src/**" >/dev/null 2>&1
assert_eq "dead glob: exclude emptying the scope exit=0" "0" "$?"

# Must not fire before a gated command can create its scope, or
# `markgate run build -- make dist` breaks on a clean checkout.
$MG verify dist --hash files --include "dist/**" >/dev/null 2>&1
assert_eq "dead glob: verify with no marker is a plain mismatch exit=1" "1" "$?"
$MG run dist --hash files --include "dist/**" -- sh -c 'mkdir -p dist && echo built > dist/x' >/dev/null 2>&1
assert_eq "dead glob: run bootstraps a scope its child creates exit=0" "0" "$?"
$MG verify dist --hash files --include "dist/**" >/dev/null 2>&1
assert_eq "dead glob: verify after the child built the scope exit=0" "0" "$?"

# The child runs, produces nothing matching, and the marker is refused.
$MG run never --hash files --include "nope/**" -- touch child-ran.txt >/dev/null 2>&1
assert_eq "dead glob: run refuses the marker after the child runs exit=2" "2" "$?"
assert_file "dead glob: the child still ran" "child-ran.txt"
assert_absent "dead glob: run recorded no marker" ".git/markgate/never.json"

# --explain is a diagnostic: it renders the (empty) scope rather than
# refusing, so adding the flag never changes what a command does.
rm -rf dist
$MG run dist2 --hash files --include "dist/**" --explain -- sh -c 'mkdir -p dist && echo built > dist/x' >/dev/null 2>&1
assert_eq "dead glob: run --explain still bootstraps exit=0" "0" "$?"
assert_file "dead glob: --explain did not suppress the child" "dist/x"
# The harder case: a glob that cannot be resolved at all. Without a
# marker nothing else touches it, so --explain is the only caller.
$MG verify badglob --hash files --include "a[b" >/dev/null 2>&1
plain_code=$?
$MG verify badglob --hash files --include "a[b" --explain >/dev/null 2>&1
assert_eq "dead glob: --explain does not change verify on a bad glob" "$plain_code" "$?"
$MG run badglob --hash files --include "a[b" --explain -- touch explain-ran.txt >/dev/null 2>&1
assert_file "dead glob: --explain did not suppress the child on a bad glob" "explain-ran.txt"

# A scope cleaned away between builds must rebuild, not lock the gate
# out. This is the regression that refusing on the read path caused.
rm -rf dist
$MG run dist --hash files --include "dist/**" -- sh -c 'mkdir -p dist && echo built > dist/x' >/dev/null 2>&1
assert_eq "dead glob: second run cycle rebuilds exit=0" "0" "$?"
assert_file "dead glob: the cleaned scope was rebuilt" "dist/x"

# The missing-/** typo: the pattern matches the directory but no file.
$MG set dirgate --hash files --include "src" >/dev/null 2>&1
assert_eq "dead glob: include naming a directory exit=2" "2" "$?"
$MG set filegate --hash files --include "src/**" >/dev/null 2>&1
assert_eq "dead glob: the same pattern with /** is live exit=0" "0" "$?"

# The candidate universe differs per mode: files hashes gitignored paths,
# a diff delta can never contain them.
echo "ignored/" > .gitignore
mkdir -p ignored && echo bin > ignored/out.bin
git add .gitignore && git commit -qm "ignore" >/dev/null
$MG set fign --hash files --include "ignored/**" >/dev/null 2>&1
assert_eq "dead glob: files may scope gitignored paths exit=0" "0" "$?"
out=$($MG set dign --hash diff --base main --include "ignored/**" 2>&1)
assert_eq "dead glob: diff refuses a gitignore-only include exit=2" "2" "$?"
assert_contains "dead glob: diff refusal names the pattern" "dead scope" "$out"
# The mirror, on an EMPTY scope so the check is actually reached: if
# files asked git's candidate set instead of the disk, this would refuse.
$MG set fexc --hash files --include "ignored/**" --exclude "ignored/**" >/dev/null 2>&1
assert_eq "dead glob: files asks the working tree, not git exit=0" "0" "$?"

# A rename is the realistic way a live scope dies under an existing marker.
# Only scan is narrowed, so healthy proves the listing still renders rows
# that are fine — the whole point of degrading rather than aborting.
cat > .markgate.yml <<'EOF'
gates:
  scan:
    hash: files
    include:
      - "src/**"
  healthy:
    hash: files
    include:
      - "docs/**"
EOF
$MG set scan >/dev/null 2>&1
$MG set healthy >/dev/null 2>&1
git mv src source >/dev/null 2>&1
out=$($MG verify scan 2>&1)
assert_eq "dead glob: verify after a rename is a mismatch exit=1" "1" "$?"
assert_contains "dead glob: rename mismatch names the pattern" "src/**" "$out"
out=$($MG set scan 2>&1)
assert_eq "dead glob: set after a rename exit=2" "2" "$?"
assert_contains "dead glob: set refusal names the pattern" "src/**" "$out"

# Bare status degrades the one bad row and keeps listing the rest.
out=$($MG status 2>&1)
assert_contains "dead glob: bare status reports the dead row" "dead scope" "$out"
assert_contains "dead glob: bare status still lists the healthy gate" "healthy" "$out"
# Column padding varies with the widest key in the sandbox, so assert the
# healthy gate's verdict through its own status rather than by scraping
# the table.
$MG status healthy >/dev/null 2>&1
assert_eq "dead glob: the healthy gate is unaffected exit=0" "0" "$?"
out=$($MG status scan 2>&1)
assert_eq "dead glob: single-key status is a mismatch exit=1" "1" "$?"
assert_contains "dead glob: single-key status explains why" "dead scope" "$out"

cyan "=== #77 clear survives an invalid config ==="
new_repo

cat > .markgate.yml <<'EOF'
gates:
  good:
    hash: files
    include:
      - "src/**"
    state_dir: .mg-store
EOF
$MG set good >/dev/null 2>&1
assert_file "clear: seed marker written to state_dir" ".mg-store/good.json"

# A different gate goes invalid. Removal must not become impossible.
cat > .markgate.yml <<'EOF'
gates:
  good:
    hash: files
    include:
      - "src/**"
    state_dir: .mg-store
  broken:
    hash: bogus
EOF
out=$($MG clear good 2>&1)
assert_eq "clear: invalid config still clears exit=0" "0" "$?"
assert_absent "clear: the marker really went" ".mg-store/good.json"
assert_absent "clear: state_dir honored, no default dir made" ".git/markgate"
assert_contains "clear: the config error is still reported" "bogus" "$out"

# Skipped for clear, not weakened.
$MG set good >/dev/null 2>&1
assert_eq "clear: set still refuses an invalid config exit=2" "2" "$?"
$MG verify good >/dev/null 2>&1
assert_eq "clear: verify still refuses an invalid config exit=2" "2" "$?"
$MG status >/dev/null 2>&1
assert_eq "clear: status still refuses an invalid config exit=2" "2" "$?"

# Unparseable: state_dir is unknowable, so say which path was used.
# Seeded while the config is still valid -- seeding afterwards would make
# `set` fail, leaving nothing to clear and an assertion that passes no
# matter what clear does.
cat > .markgate.yml <<'CFGEOF'
gates:
  good:
    hash: files
    include:
      - "src/**"
CFGEOF
$MG set k --hash files --include "src/**" --state-dir .mg2 >/dev/null 2>&1
assert_file "clear: override marker seeded before the config breaks" ".mg2/k.json"
printf 'gates:\n  good: {hash: files\n' > .markgate.yml
out=$($MG clear k --state-dir .mg2 2>&1)
assert_eq "clear: unparseable config still clears exit=0" "0" "$?"
assert_contains "clear: says the config could not be read" "could not be read" "$out"
assert_contains "clear: names the path it looked at" ".mg2/k.json" "$out"
assert_absent "clear: --state-dir still resolves exactly" ".mg2/k.json"

# With no override the fallback may look somewhere the marker is not, so
# it must say so rather than let "cleared" imply the marker is gone.
out=$($MG clear neverset 2>&1)
assert_eq "clear: fallback with nothing there exit=0" "0" "$?"
assert_contains "clear: warns the marker may survive elsewhere" "still exists" "$out"

# Leniency is about the config, not the command line.
cat > .markgate.yml <<'CFGEOF'
gates:
  good:
    hash: files
    include:
      - "src/**"
CFGEOF
$MG clear good --hash bogus >/dev/null 2>&1
assert_eq "clear: flag typo still refused on a valid config exit=2" "2" "$?"

# A clean config must stay silent, or the warnings above mean nothing.
cat > .markgate.yml <<'EOF'
gates:
  good:
    hash: files
    include:
      - "src/**"
EOF
$MG set good >/dev/null 2>&1
out=$($MG clear good 2>&1)
assert_eq "clear: valid config clears exit=0" "0" "$?"
assert_eq "clear: valid config warns about nothing" "cleared: good" "$out"

# ─────────────────────────────────────────────────────────────────
echo
cyan "=== summary ==="
green "PASS: $PASS"
if (( FAIL > 0 )); then
  red "FAIL: $FAIL"
  printf '\nFailures:\n'
  for f in "${FAIL_LOG[@]}"; do red "  - $f"; done
else
  green "FAIL: 0"
fi
exit "$FAIL"
