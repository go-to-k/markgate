# markgate

> Stop re-running your checks. Skip the commit hook when nothing has
> changed since the last time they passed.

`markgate` is a verification-state cache for hook managers (Claude
Code hooks, husky, lefthook, pre-commit, bare `.git/hooks/*`). It
records that a check passed at the current repo state so the next hook
can skip re-running it.

## 20-second tour

```sh
# First run — no marker yet, so `make check` runs and the marker is saved.
$ markgate run -- make check
linting...
tests passed in 4.2s

# Second run — nothing changed since the last success: instant skip.
$ markgate run -- make check

# After you edit a file — marker is stale, `make check` runs again.
$ echo '// fix typo' >> src/foo.go
$ markgate run -- make check
linting...
tests passed in 4.1s
```

Zero config. No key argument needed. That is the intended daily usage.
(`make check` is a placeholder — substitute your project's verification
command.)

## Why markgate?

Your agent (or you) just ran `make check`. The commit hook runs it
again. `gh pr create` runs it again. CI runs it again — four passes,
one change. `markgate` lets the second / third / fourth of those exit
instantly when the repo state hasn't moved.

Concrete gates you can build (see [Use cases](#use-cases) for full
configs):

- **Pre-commit: skip duplicate checks** — skip lint / test / build when
  nothing changed since the last `/check`.
- **Pre-PR: docs consistency** — re-verify docs only when `docs/**` or
  `README.md` actually changed. Code-only commits are free.
- **Pre-image-push: vulnerability scan freshness** — re-run `trivy`
  only when `Dockerfile` or lockfiles changed.
- **Pre-push: coverage report freshness** — re-run the test suite only
  when `src/**` or `tests/**` changed.

Existing hook managers (husky / lefthook / pre-commit / Claude Code
hooks) are great at *running* checks. None of them *remember* that the
checks just passed and nothing relevant has changed since. `markgate`
is that memory layer — exit 0 means "verified, skip", exit 1 means
"stale, re-run". It's not a hook manager itself; it slots into the one
you already use — one line to adopt, one line to remove.

## Usage

### `markgate run -- <cmd>` (main)

`markgate run -- <cmd>` is the idiomatic form. It collapses the common
verify → run → set cycle into one invocation:

1. **verify** — if the marker matches, `<cmd>` is not executed; exit 0
   immediately.
2. Otherwise **execute `<cmd>`**. stdio is passed through;
   `SIGINT` / `SIGTERM` are forwarded to the child.
3. On success, **set** the marker. On failure, the marker is **not**
   updated and `<cmd>`'s exit code is returned as-is.

```sh
markgate run -- make check
```

Most hook setups only need this one command.

### `markgate set` / `markgate verify` (building blocks)

Reach for the two halves directly when `run` doesn't fit:

- **Multi-step check pipelines** — `run -- <cmd>` wraps a single
  command. If your check is several steps (typecheck, lint, build,
  tests) spread across a script or skill, run them normally and call
  `markgate set` once at the end, after all pass.
- **Check and gate in different places** — e.g. a Claude Code skill
  runs the check and records the marker, while a PreToolUse hook on
  `git commit*` only calls `markgate verify` to gate.

```sh
# Wherever the check runs — record state on success:
typecheck && lint && build && test && markgate set

# Wherever the gate runs — short-circuit on a fresh marker, else re-run:
markgate verify || make check
```

Exit codes follow the `grep` / `diff` convention, so `||` composes
naturally:

| exit | meaning                                                   |
| ---- | --------------------------------------------------------- |
| 0    | verified — state matches the marker, safe to skip         |
| 1    | not verified — no marker, or state differs                |
| 2    | error — not in a repo, bad config, bad key, etc.          |

## Drop into your hook

If the hook can wrap your check command, use `markgate run -- <cmd>`
(husky / lefthook / pre-commit). If the hook only sits *in front of*
the command (Claude Code PreToolUse, bare `.git/hooks/*`), use
`markgate verify` (exit 0 = skip, exit 1 = re-run) and pair it with
`markgate set` in the check command itself.

### Claude Code (PreToolUse hook)

`.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "if": "Bash(git commit*)",
        "hooks": [
          {
            "type": "command",
            "command": "markgate verify"
          }
        ]
      }
    ]
  }
}
```

In your check skill:

```sh
make check && markgate set
```

See [Use cases § Pre-commit](#1-pre-commit-skip-duplicate-checks) for
the full flow.

### husky

`.husky/pre-commit`:

```sh
markgate run -- make check
```

### lefthook

`lefthook.yml`:

```yaml
pre-commit:
  commands:
    check:
      run: markgate run -- make check
```

### pre-commit framework

`.pre-commit-hooks.yaml`:

```yaml
repos:
  - repo: local
    hooks:
      - id: markgate-check
        name: markgate check
        entry: markgate run -- make check
        language: system
        pass_filenames: false
```

### Bare `.git/hooks/pre-commit`

```sh
#!/bin/sh
markgate verify || {
  echo "Run your check command first, then commit." >&2
  exit 1
}
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

## Use cases

Each section below follows the same shape: **Scope** (config) →
**Wire** (shell).

### 1. Pre-commit: skip duplicate checks

**Scope**: anything tracked by git. No config needed (default `git-tree`).

**Wire**:

```sh
# In your check command:
make check && markgate set

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

**Wire**:

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

**Wire**:

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

**Wire**:

```sh
go test -cover && markgate set pre-push

# In .git/hooks/pre-push:
markgate verify pre-push || exit 1
```

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
```

Flag syntax is identical across hash types. With `--hash files`,
`--include` is required. Example — exclude `vendor/` without any
config file:

```sh
markgate run --exclude 'vendor/**' -- make check
```

## `.markgate.yml` (optional)

Only needed for multiple gates, or for `files` hash, or to persist
include/exclude. Looked up at
`$(git rev-parse --show-toplevel)/.markgate.yml` (no parent-dir
walking).

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

## Marker storage

Markers live at:

```text
$(git rev-parse --git-dir)/markgate/<key>.json
```

Inside `.git/`, so no gitignore entry is needed and worktrees stay
isolated. The on-disk JSON layout is an implementation detail — the
fields (including `version`, which is an internal schema marker) exist
only for debugging and may change between releases without notice.
Don't parse it.

## FAQ

- **Does it work in git worktrees?** Yes. Markers live under each
  worktree's own `.git/` dir, so they don't leak across worktrees.
- **Do I need to gitignore anything?** No — markers are under `.git/`.
- **What if I don't want HEAD in the hash?** Use `hash: files` for
  that gate.
- **Does `files` respect `.gitignore`?** No. `files` is explicit scope
  by design. Use `git-tree` when you want `.gitignore`-aware behavior.
- **Can the marker be tampered with?** Locally, yes (it's a JSON file
  under `.git/`). Signed / remote-shared markers are a future
  consideration for CI-shared caches, not part of this release.

## License

MIT. See [LICENSE](LICENSE).
