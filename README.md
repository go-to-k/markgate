# markgate

`markgate` is a zero-config verification-state cache for hook
managers (Claude Code hooks, husky, lefthook, pre-commit, bare
`.git/hooks/*`). Your hooks can:

- **Skip the checks that already passed** — instant exit when the
  repo state hasn't changed since the last successful run.
- **Catch the ones that never ran** — block the commit when the
  check hasn't been recorded yet.

**Especially useful in the AI-coding-agent era.** You tell your
agent to run the check; sometimes it forgets (context loss, tokens,
speed pressure) and commits anyway. You add a pre-commit hook to
enforce it; now every commit runs the check twice — once by the
agent, once by the hook. `markgate` breaks this dilemma: duplicate
runs exit instantly, and commits without a fresh check get blocked.

## 20-second tour

```sh
# First run — nothing cached yet, so `pnpm test` runs and the pass is cached.
$ markgate run -- pnpm test
tests passed in 4.2s

# Second run — nothing changed since the last success: instant skip.
$ markgate run -- pnpm test

# After you edit a file — cache is stale, `pnpm test` runs again.
$ echo '// fix typo' >> src/foo.ts
$ markgate run -- pnpm test
tests passed in 4.1s
```

Under the hood, when a check passes, `markgate` writes a small JSON
**marker** recording the current repo state. The next hook run exits
in milliseconds if the state matches, or re-runs the check if it's
moved.

## Two shapes: `run` vs `set` + `verify`

Pick by where your hook sits relative to the check.

**`markgate run -- <cmd>`** — one-shot. Use where the hook runs the
check itself (husky, lefthook, pre-commit framework, bare
`pre-commit`): just prefix your check command with `markgate run --`.

```sh
# .husky/pre-commit (or lefthook.yml, .pre-commit-hooks.yaml, ...):
markgate run -- pnpm test
# First hook: pnpm test runs. Next hook with no changes: instant skip.
```

**`markgate set` + `markgate verify`** — split. Use when the check
and the gate live in different places. Concrete scenarios:

- **Claude Code gating `git commit`** — the `/check` skill runs the
  check and calls `markgate set` on success; a PreToolUse hook on
  `git commit` calls `markgate verify` to block un-verified commits.
  The hook sits *in front of* `git commit`, so it can't run the
  check itself. This is the canonical case.
- **Multi-step checks** — `run -- <cmd>` takes a single command;
  split lets the check stay a plain script (typecheck → lint → build
  → test → `markgate set`) and stops forcing you to collapse
  everything into one command.
- **Commit-then-push** — the commit hook runs the check (`... &&
  markgate set`); the push hook only calls `markgate verify`,
  skipping a second run when nothing has changed since the commit.

```sh
# /check skill body (or build script, CI job, Make target):
pnpm run typecheck
pnpm run lint:fix
pnpm run build
pnpm test
markgate set

# .claude/settings.json PreToolUse on `git commit`:
markgate verify
```

Full semantics and exit codes are in [Command model](#command-model).
Both shapes appear throughout the use cases below.

## Why markgate?

Two failure modes in an AI-agent-driven workflow, one cache layer.
Hook managers are great at *running* checks; none remember when one
just passed. `markgate` is that memory layer — exit 0 = verified,
exit 1 = run it. One line to adopt, one line to remove.

**Skip what already passed.** Your agent (or you) just ran `pnpm test`.
The commit hook runs it again. `gh pr create` runs it again. CI
runs it again — four passes, one change. `markgate` lets the second
/ third / fourth of those exit instantly when the repo state hasn't
moved. (The CI pass needs a bit of extra wiring — see
[Sharing markers](#sharing-markers-across-machines-ci--teammates).)

**Catch what never ran.** Your agent decided to run `/check`, then
ran out of tool budget / context and committed anyway. Or a tool
call silently failed. Or it simply forgot. Wire `markgate verify`
into your pre-commit or PreToolUse hook and there's no bypass by
"forgetting" — a fresh marker exists only after a check actually
passed against the current state. Exit 1 with "no marker" is a
loud, debuggable failure; a silent skip is not. **No marker, no
commit.**

## Use cases

Each section below follows the same shape: **Scope** (what triggers
re-verify) → **Commands** (what goes in your shell / hook). The
first use case works with zero config; the rest define scoped gates
in [`.markgate.yml`](#markgateyml-optional) at the repo root.

### 1. Pre-commit: skip duplicates, catch forgotten checks

**Scope**: anything tracked by git. No config needed (default `git-tree`).

**Commands**:

```sh
# In your check command:
pnpm test && markgate set

# In your Claude Code PreToolUse hook on `git commit*`:
markgate verify
```

The agent runs `/check`, commits immediately after, and the commit
hook verifies instantly. Commit without a prior `/check` → hook returns
1, agent re-runs the check.

### 2. Pre-PR: docs consistency

**Scope**: only `docs/` and `README.md`. Code-only commits don't
invalidate the marker.

```yaml
# .markgate.yml
gates:
  pre-pr:
    hash: files
    include:
      - "docs/**"
      - "README.md"
```

**Commands**:

```sh
./scripts/check-docs && markgate set pre-pr

# Before `gh pr create`:
markgate verify pre-pr || {
  echo "Docs are out of date. Run check-docs." >&2
  exit 1
}
```

### 3. Pre-image-push: vulnerability scan freshness

**Scope**: only files that actually affect the image (Dockerfile +
lockfiles).

```yaml
gates:
  pre-image-push:
    hash: files
    include:
      - "Dockerfile"
      - "package.json"
      - "package-lock.json"
```

**Commands**:

```sh
trivy image ... && markgate set pre-image-push

# In your `docker push` wrapper:
markgate verify pre-image-push || exit 1
```

### 4. Pre-push: coverage report freshness

**Scope**: just source and tests.

```yaml
gates:
  pre-push:
    hash: files
    include:
      - "src/**"
      - "tests/**"
```

**Commands**:

```sh
go test -cover && markgate set pre-push

# In .git/hooks/pre-push:
markgate verify pre-push || exit 1
```

## Install

### Homebrew (macOS / Linux)

```sh
brew install go-to-k/tap/markgate
```

### Shell script (macOS / Linux / Windows with Git Bash)

```sh
# Latest
curl -fsSL https://raw.githubusercontent.com/go-to-k/markgate/main/install.sh | bash

# Pin a version
curl -fsSL https://raw.githubusercontent.com/go-to-k/markgate/main/install.sh | bash -s -- v0.1.0
```

### `go install`

```sh
go install github.com/go-to-k/markgate/cmd/markgate@latest
```

### Prebuilt binaries

Linux / macOS / Windows archives (amd64 / arm64 / 386) — see
[GitHub Releases](https://github.com/go-to-k/markgate/releases).

## Drop into your hook manager

Substitute `pnpm test` with your verification command. When the
hook runs the check itself, use `run`; when it sits *in front of* a
separate command, use `verify` and pair it with `set` in the check
itself (see [Two shapes](#two-shapes-run-vs-set--verify)).

**husky** — `.husky/pre-commit`:

```sh
markgate run -- pnpm test
```

**lefthook** — `lefthook.yml`:

```yaml
pre-commit:
  commands:
    check:
      run: markgate run -- pnpm test
```

**pre-commit framework** — `.pre-commit-hooks.yaml`:

```yaml
repos:
  - repo: local
    hooks:
      - id: markgate-check
        name: markgate check
        entry: markgate run -- pnpm test
        language: system
        pass_filenames: false
```

**Claude Code (PreToolUse)** — `.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "if": "Bash(git commit*)",
        "hooks": [
          { "type": "command", "command": "markgate verify" }
        ]
      }
    ]
  }
}
```

In your `/check` skill: `pnpm test && markgate set`. See
[Use case 1](#1-pre-commit-skip-duplicates-catch-forgotten-checks) for the full flow.

## Command model

### `markgate run -- <cmd>` (one-shot)

Collapses verify → run → set into one invocation:

1. **verify** — if the marker matches, `<cmd>` is not executed; exit 0
   immediately.
2. Otherwise **execute `<cmd>`**. stdio is passed through;
   `SIGINT` / `SIGTERM` are forwarded to the child.
3. On success, **set** the marker. On failure, the marker is **not**
   updated and `<cmd>`'s exit code is returned as-is.

### `markgate set` / `markgate verify` (split)

Reach for these two halves when the check and the gate run in
different places, or when the check is several steps (typecheck,
lint, build, tests) that don't fit into a single `<cmd>`:

```sh
# Wherever the check runs — record state on success:
pnpm test && markgate set

# Wherever the gate runs — short-circuit on a fresh marker, else re-run:
markgate verify || pnpm test
```

### Exit codes

Exit codes follow the `grep` / `diff` convention, so `||` composes
naturally:

| exit | meaning                                                   |
| ---- | --------------------------------------------------------- |
| 0    | verified — state matches the marker, safe to skip         |
| 1    | not verified — no marker, or state differs                |
| 2    | error — not in a repo, bad config, bad key, etc.          |

## Core concepts

### Key (optional)

Every marker is keyed. You only need to think about keys when you run
**multiple independent gates** in the same repo (e.g. both `pre-commit`
and `pre-pr`). Omitted key = `default`, which covers the single-gate
case.

```sh
markgate set               # same as `markgate set default`
markgate set pre-pr        # a second, independent gate
```

Keys must match `[a-z0-9][a-z0-9-]*` (kebab-case ASCII).

### Hashing strategy: `git-tree` vs `files`

`markgate` ships two hashing strategies:

| | `git-tree` (default) | `files` |
|---|---|---|
| What it hashes | `HEAD` + diff-vs-HEAD ∪ untracked-not-ignored | whatever matches your `include` globs |
| `HEAD` in the hash? | **Yes** | **No** |
| Commits invalidate the marker? | Yes | Only if they touch in-scope files |
| `.gitignore` respected? | Yes (automatic) | No — scope is explicit |
| Needs config? | No | Yes (`include` required) |

They serve different purposes:

- **`git-tree`** = "re-verify on *any* repo change". Broad gates
  (pre-commit running lint/test/build). Add `exclude` patterns to skip
  `vendor/`, `node_modules/`, etc. — HEAD-aware invalidation is kept.
- **`files`** = "re-verify *only* when these paths change, ignore other
  commits". Narrow gates (docs consistency, vuln scan rooted on a
  lockfile, coverage for one sub-tree).

Rule of thumb: start with `git-tree` (add `exclude` if needed). Reach
for `files` only when you specifically want the "ignore commits that
don't touch these paths" semantics.

## CLI reference

```text
markgate set    [key]              Record the current state hash.
markgate verify [key]              Exit 0 match, 1 mismatch, 2 error.
markgate status [key]              Show marker + match status.
markgate clear  [key]              Delete the marker (idempotent).
markgate run    [key] -- <cmd>...  Sugar for verify + <cmd> + set.
markgate init                      Write a starter .markgate.yml.
markgate version                   Print the version.
```

### Per-invocation overrides

`set` / `verify` / `status` / `clear` / `run` each accept these flags,
so one-off scopes don't need a `.markgate.yml`:

```text
--hash git-tree|files    Override hash type for this call.
--include <glob>         Repeatable. Override the gate's include list.
--exclude <glob>         Repeatable. Override the gate's exclude list.
--state-dir <path>       Directory to store marker files. Takes
                         precedence over MARKGATE_STATE_DIR env and
                         state_dir: in .markgate.yml. Default:
                         <git-dir>/markgate. See "Sharing markers".
```

Flag syntax is identical across hash types. With `--hash files`,
`--include` is required. Example — exclude `vendor/` without any
config file:

```sh
markgate run --exclude 'vendor/**' -- pnpm test
```

### Environment variables

```text
MARKGATE_STATE_DIR       Marker storage directory. Same effect as
                         --state-dir and state_dir: in config.
                         Precedence: --state-dir > this env >
                         state_dir: in .markgate.yml > default.
```

## `.markgate.yml` (optional)

Only needed for multiple gates, or for `files` hash, or to persist
include / exclude / state_dir. Looked up at
`$(git rev-parse --show-toplevel)/.markgate.yml` (no parent-dir
walking).

Per-gate fields:

| field | purpose |
|---|---|
| `hash` | `git-tree` (default) or `files` |
| `include` | glob list; required for `hash: files` |
| `exclude` | glob list |
| `state_dir` | marker storage directory (override per gate). Prefer a **relative** path — it resolves against the repo top-level so it's identical on every machine. An absolute path committed here will point to nonexistent locations on other machines. CLI flag and `MARKGATE_STATE_DIR` still take precedence. |

### Generate a starter — `markgate init`

```sh
markgate init          # writes .markgate.yml at the repo root
markgate init --force  # overwrite an existing one
```

The generated file enables the default `git-tree` gate with
commented-out examples (an `exclude` list on `git-tree`, plus a
`files`-type gate) — uncomment what you need.

### Full example

```yaml
gates:
  default:
    hash: git-tree
    exclude:
      - "vendor/**"
      - "node_modules/**"

  pre-pr:
    hash: files
    include:
      - "docs/**"
      - "README.md"
    exclude:
      - "**/*.txt"
```

## Sharing markers across machines (CI / teammates)

By default, markers live under `.git/markgate/` — strictly local. If
that's all you need, skip this section; the [use cases above](#use-cases)
all work with the default.

Read on if you want a check to **skip in CI (or on a teammate's
machine) based on a run that already happened elsewhere**. Typical
wins: coverage, vulnerability scan, e2e, image build — expensive and
deterministic, redundant to re-run. Don't use it for security
boundaries (supply-chain audit, permission scan); those should stay
fresh in CI.

### Specifying a non-default location

Three sources, in precedence order (flag beats env beats config):

```text
--state-dir <dir>           # per-invocation flag
MARKGATE_STATE_DIR=<dir>    # environment variable
state_dir: <dir>            # in .markgate.yml, per gate
```

The marker is written at `<dir>/<key>.json` (no extra `markgate/`
subdirectory). Relative paths resolve against the repo top-level, so
the location is stable regardless of cwd — identical on every machine
that checks out the repo.

### Two patterns at a glance

Both use `--state-dir` / `state_dir`; the difference is whether the
marker is **committed** to the repo.

| aspect | **A. Not committed** (CI cache / artifact) | **B. Committed** |
|---|---|---|
| Marker in the repo? | No (typically gitignored, or outside the repo) | Yes, tracked in git |
| Works with hash type | `git-tree` or `files` | **`files` only** — committing with `git-tree` breaks: the commit changes HEAD → digest is instantly stale |
| Local → CI sharing | Needs CI cache / artifact / shared volume | Just `git push` |
| Tamper surface | Whoever can write to the cache | Whoever has commit access |
| Extra infra | CI cache provider (e.g. `actions/cache`, `actions/upload-artifact`) | None — git is enough |
| Best for | CI-internal reuse across runs; teams already on remote cache infra | Zero-infra local→CI sharing for `files`-hash gates (coverage, scans) |

### A. Not committed (CI cache / artifact)

Store the marker somewhere CI can pick it up, but keep it out of git.
`.markgate-cache/` at the repo root is a conventional choice; any
path outside `.git/` works. (If you'd rather commit the marker into
git so CI sees it without any cache layer, skip to
[Pattern B](#b-committed-files-hash) — that's a different shape, not
a variant of this one.)

#### Step 1. Add the state dir to `.gitignore`

**This is a required setup step on `hash: git-tree`, not optional
hygiene.** Do this *before* your first `markgate run`:

```gitignore
# .gitignore — add the state dir you chose
/.markgate-cache/
```

You can skip this only if:

- the state dir is **outside the repo** (e.g. `$RUNNER_TEMP/mg`,
  `/tmp/mg`, `$HOME/.cache/markgate`), **or**
- you're on `hash: files` (gitignore then becomes hygiene, not
  required — see why below).

<details>
<summary>Why it's required on <code>hash: git-tree</code> (click to expand)</summary>

The `git-tree` digest hashes `HEAD + diff-vs-HEAD ∪
untracked-not-ignored`. The saved marker file is itself an untracked
file, so without gitignore:

1. `markgate run` computes **digest_1** (before the marker exists)
   and saves the marker with digest_1.
2. The saved marker file now exists as untracked-not-ignored.
3. The next `markgate verify` computes **digest_2**, which *includes*
   the marker file. digest_2 ≠ digest_1 → mismatch → the check
   re-runs every time.

The feature is defeated on the first verify, before any commit.
Gitignoring the state dir keeps the marker out of the digest.

`hash: files` sidesteps this: the marker is only in the digest if an
`include` glob matches it, which it normally won't. That's why
gitignore is optional on `files`.

</details>

#### Step 2. Wire up CI

**Across runs of the same workflow** — `actions/cache`, extending the
`pre-image-push` gate from
[Use case 3](#3-pre-image-push-vulnerability-scan-freshness):

```yaml
# .github/workflows/scan.yml
jobs:
  scan:
    steps:
      - uses: actions/checkout@v4
      - uses: actions/cache@v4
        with:
          path: .markgate-cache
          key: markgate-scan-${{ github.sha }}
          restore-keys: |
            markgate-scan-
      - run: markgate run pre-image-push --state-dir .markgate-cache -- trivy fs .
```

**Across jobs within one workflow** — `actions/upload-artifact` →
`actions/download-artifact`. A setup job runs the expensive check
once; matrix jobs on the same commit download the marker and skip.
(`expensive` below is a placeholder key — define it in your
`.markgate.yml` using the [Use cases](#use-cases) as templates, or
pass `--include` / `--hash` via CLI flags.)

```yaml
jobs:
  verify:
    steps:
      - uses: actions/checkout@v4
      - run: markgate run expensive --state-dir .markgate-cache -- make expensive-check
      - uses: actions/upload-artifact@v4
        with:
          name: markgate-state
          path: .markgate-cache

  fan-out:
    needs: verify
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/download-artifact@v4
        with:
          name: markgate-state
          path: .markgate-cache
      - run: markgate verify expensive --state-dir .markgate-cache || make expensive-check
```

### B. Committed (files hash)

Keep the state directory **tracked in git** and commit the marker with
the code. Works only with `hash: files`: `git-tree` would change HEAD
on the commit and invalidate the marker it just wrote.

Typical fit: coverage reports, image vulnerability scans — expensive,
deterministic, and already re-running them on every push is waste
when nothing in scope changed.

Coverage example (extending the pre-push gate from
[Use case 4](#4-pre-push-coverage-report-freshness)):

```yaml
# .markgate.yml
gates:
  coverage:
    hash: files
    include:
      - "src/**"
      - "tests/**"
    state_dir: .markgate-state
```

```sh
# Locally, after a successful coverage run:
markgate run coverage -- go test -cover ./...
git add .markgate-state/coverage.json
git commit -m "bump coverage marker"
git push

# In CI (already sees the committed marker):
markgate verify coverage || go test -cover ./...
```

Trust model: anyone with commit access can forge a skip. Use committed
markers where commit-access already implies trust in the signal.

### Notes

- **Worktree isolation is lost** when the dir is shared across
  worktrees pointing at the same location. The default `.git/`-based
  layout preserves isolation; `--state-dir` does not.
- **Relative paths** resolve from the repo top-level, not cwd, so
  hook-invoked commands land in the same place regardless of where
  they run from.
- **Signing is not yet implemented** — markers are unsigned JSON.
  Tamper resistance depends on who can write to the directory (cache /
  repo).

## Marker storage

Markers live at:

```text
$(git rev-parse --git-dir)/markgate/<key>.json
```

Inside `.git/`, so no gitignore entry is needed and worktrees stay
isolated. With `--state-dir <dir>`, `MARKGATE_STATE_DIR=<dir>`, or
`state_dir:` in `.markgate.yml`, the location becomes `<dir>/<key>.json`
instead — see
[Sharing markers](#sharing-markers-across-machines-ci--teammates). The on-disk
JSON layout is an implementation detail — the fields (including
`version`, which is an internal schema marker) exist only for
debugging and may change between releases without notice. Don't parse
it.

## FAQ

- **Does it work in git worktrees?** Yes. Markers live under each
  worktree's own `.git/` dir, so they don't leak across worktrees.
  (This isolation is lost if you point `--state-dir` at a shared
  location.)
- **Do I need to gitignore anything?** No for the default layout —
  markers are under `.git/`. If you use `--state-dir` pointing inside
  the repo, gitignore that directory.
- **What if I don't want HEAD in the hash?** Use `hash: files` for
  that gate.
- **Does `files` respect `.gitignore`?** No. `files` is explicit scope
  by design. Use `git-tree` when you want `.gitignore`-aware behavior.
- **Can markers be shared across machines / CI?** Yes, via
  `--state-dir`, `MARKGATE_STATE_DIR`, or `state_dir:` in
  `.markgate.yml`. See
  [Sharing markers](#sharing-markers-across-machines-ci--teammates) for patterns
  and trust considerations.
- **Can the marker be tampered with?** Yes — it's a JSON file under
  `.git/` (or wherever `--state-dir` points). Trust whoever can write
  to that location. Signed markers are still a future consideration.

## License

MIT. See [LICENSE](LICENSE).
