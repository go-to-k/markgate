---
name: verify-e2e
description: Run the markgate end-to-end CLI verification script (`.claude/scripts/e2e.sh`). It exercises the full CLI surface — original primitives (set / verify / clear / run / init / version, default key, --hash files + --include, --state-dir, env-var, precedence) plus every feature added in the 2026-05-09 batch (completion, config lint, TTL, --explain, bare status, composes / requires). Wraps the script in `markgate run` so unchanged repos skip the run. Use whenever you want to confirm "all features still work" before declaring done, opening a PR, or merging — or when your changes touch the CLI surface and you want a quick smoke before pushing.
---

# verify-e2e

End-to-end smoke of the whole markgate CLI. The companion to
`make lint` (static) and `go test ./...` (unit / integration); this
one is the **black-box** layer that drives the built binary against
real `git init` sandboxes the way a user would.

## When to invoke

- After a non-trivial change anywhere under `cmd/` or
  `internal/cli/` — the unit tests cover most of this, but the
  black-box harness catches wiring regressions (cobra command
  registration, flag exposure, exit-code mapping in `Execute`).
- Before opening a PR for a CLI-touching change. Faster than
  waiting on CI.
- Before declaring a multi-PR merge wave done.
- When the user asks "is everything still working?".

The PreToolUse hook `e2e-pre-merge.sh` runs the same script
automatically before any `gh pr merge ...`, so you usually do not
need to invoke this skill *immediately* before merging — but it is
the manual entry point and a good rehearsal step before pushing.

## What it covers

84 assertions across two layers:

**Pre-existing primitives (33 assertions)**

- `set` / `verify` / `clear` cycle including `clear` idempotency
- Default key (no positional arg) → `default`
- `--hash files` requires `--include`; respects scope
- `--state-dir` flag override; marker NOT in default location
- `MARKGATE_STATE_DIR` env override
- Precedence chain: flag > env > `state_dir:` > default
- `run`: child runs on miss, skips on match, exit propagates,
  marker NOT advanced on child failure
- `init` writes `.markgate.yml` and is non-clobbering
- `version` prints something

**2026-05-09 batch features (51 assertions)**

- `completion`: bash script emit, unknown-shell exit=2, dynamic
  gate-key completion via `__complete`
- `config lint`: clean exit=0, dirty exit=1, dead glob /
  unknown-field detection, `--json` shape
- TTL: fresh→0, expired→1 with stderr message, set resets,
  `1mo` rejected at config load
- `--explain` / `-e`: scope to stderr, exit code unchanged,
  pretty-printed JSON shape `{key, scope, hasher, state}`
- Bare `status`: header, all rows, `(configured)` /
  `(unconfigured)` notes, single-key backward compat,
  snake_case `--json`
- `composes` (loose): unconditional set, verify-time propagation
- `requires` (strict): set refused exit=2 naming offending child
- Config-load errors: cycle / missing child / both fields set
  all reject at exit=2

## How it caches

The hook wraps the script in `go run ./cmd/markgate run
hook-e2e-pre-merge -- bash .claude/scripts/e2e.sh`, so:

- **First run after a source change**: full ~10–15s execution
  (build + 84 assertions, the script does its own `go build` to
  the temp dir).
- **Repeat run with no source change**: ~0.1s skip, marker hit.

The marker lives at `.git/markgate/hook-e2e-pre-merge.json`
(default state-dir, git-ignored). Default `git-tree` hash means
any source change invalidates it.

## Running it

```sh
bash .claude/scripts/e2e.sh             # full run, 84 PASS lines + summary
QUIET=1 bash .claude/scripts/e2e.sh     # only print section headers + failures
```

Exit code = number of failed assertions (0 on all-pass).

## When this skill is NOT enough

- The script does not exercise marker-file *atomicity* under
  concurrent writes — that's covered by `internal/state` unit
  tests, and re-testing it from a black-box harness is unreliable.
- It does not run `make lint` or `go test ./...`. Those remain
  the static / unit gates and are independent. Run them separately
  when introducing new code (the `audit-before-done` skill is the
  canonical pre-push checklist).
- It uses a temp sandbox under `/tmp` and does not exercise the
  user's actual `.markgate.yml` or shared state-dir. If you
  changed sharing / multi-host behavior, smoke that path manually
  too.

## Adding a new assertion

When a new feature lands in the markgate CLI:

1. Add a section to `.claude/scripts/e2e.sh` (mirror the existing
   `cyan "=== feature ==="` + `new_repo` + `assert_*` pattern).
2. Run `bash .claude/scripts/e2e.sh` once and confirm the new
   assertions pass.
3. Update the "what it covers" list in this SKILL.md so the
   description stays honest.
