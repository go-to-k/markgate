#!/usr/bin/env bash
# End-to-end verification of the markgate CLI surface.
#
# Covers (a) the original primitives — set / verify / clear / run /
# init / version, default key, --hash files + --include, --state-dir
# override, env-var override — and (b) the six features added in the
# 2026-05-09 batch: shell completion, config lint, TTL, --explain,
# bare status, and gate dependencies (composes / requires).
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
