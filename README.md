# markgate

Stop re-running your checks. Skip the commit hook when nothing has
changed since the last time they passed.

## The problem

Coding agents (and humans) do one of two things around commits: they
either **skip** the check pipeline and commit dirty, or they **re-run**
it even though it passed five seconds ago. Existing pre-commit tools
(husky, lefthook, pre-commit) are good at *running* checks; they have
no concept of *remembering* that the checks just ran and nothing
relevant has changed since.

## What markgate does

`markgate` remembers. After your checks pass, record a marker. Before
the next commit (or PR, or image push, ...), verify it. If nothing has
changed, the gate opens instantly. If something has, it closes and your
agent is told to re-run.

It's not a hook manager — it's a small primitive you drop into the hook
manager you already use (Claude Code hooks, husky, lefthook, pre-commit,
or a bare `.git/hooks/pre-commit`).

## Quick start

```sh
# One-shot: verify, run the check on mismatch, record the marker on success.
markgate run -- pnpm run check

# Or the two-step form:
markgate verify || { pnpm run check && markgate set; }
```

Zero config. No key argument needed for the default case.

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

Linux / macOS / Windows archives (amd64, arm64, 386) are published on
[GitHub Releases](https://github.com/go-to-k/markgate/releases).

## How it works

`markgate` maintains one marker per gate. When you call `verify`, it
hashes the current repository state and compares it against the stored
marker.

| exit | meaning                                                   |
| ---- | --------------------------------------------------------- |
| 0    | verified — state matches the marker, safe to skip         |
| 1    | not verified — no marker, or state differs                |
| 2    | error — not in a repo, bad config, bad key, etc.          |

This follows the `grep` / `diff` convention, so it composes cleanly:

```sh
markgate verify || pnpm run check
```

The default hash covers `HEAD` plus every file that differs from `HEAD`
or is untracked-and-not-ignored. Consequences:

- **Committing invalidates the marker** (because HEAD moved).
- **Editing any tracked file invalidates it**.
- **`git add` alone does not change the hash** (staging-agnostic, so
  you can stage and unstage freely).

## `markgate run` — the recommended sugar

`markgate run -- <cmd>` collapses the common pattern into one line:

1. **verify** — if the marker matches, `<cmd>` is not executed; exit 0
   immediately.
2. Otherwise **execute `<cmd>`**. stdio is passed through; `SIGINT` and
   `SIGTERM` are forwarded to the child.
3. On success, **set** the marker. On failure, the marker is **not**
   updated and `<cmd>`'s exit code is returned.

Most hook setups only need this one command.

## Use cases

### Pre-commit: skip duplicate checks

```sh
# In your check command:
pnpm run check && markgate set

# In your Claude Code PreToolUse hook on `git commit*`:
markgate verify
```

When the agent runs `/check` then commits, the commit hook verifies
instantly. When someone commits without running `/check`, the hook
returns 1 and tells the agent to run it.

### Pre-PR: docs consistency

```yaml
# .markgate.yml
gates:
  pre-pr:
    hash: files
    include:
      - "docs/**"
      - "README.md"
```

```sh
./scripts/check-docs && markgate set pre-pr

# Before `gh pr create`:
markgate verify pre-pr || {
  echo "Docs are out of date. Run check-docs." >&2
  exit 1
}
```

Only docs changes invalidate the marker. Code-only commits leave it
alone.

### Pre-image-push: vulnerability scan freshness

```yaml
gates:
  pre-image-push:
    hash: files
    include:
      - "Dockerfile"
      - "package.json"
      - "package-lock.json"
```

```sh
trivy image ... && markgate set pre-image-push

# In your `docker push` wrapper:
markgate verify pre-image-push || exit 1
```

Only re-scan when something the image actually depends on has changed.

### Pre-push: coverage report freshness

```yaml
gates:
  pre-push:
    hash: files
    include:
      - "src/**"
      - "tests/**"
```

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
markgate version                   Print the version.
```

- `[key]` defaults to `default`. Supply one only when you want multiple
  independent gates in the same repo (`pre-commit`, `pre-pr`, ...).
- Keys must match `[a-z0-9][a-z0-9-]*` (kebab-case, ASCII).

## Hash types

- **`git-tree`** (default, zero config): `HEAD` + diff-vs-HEAD ∪
  untracked-not-ignored. Deletion-aware. Staging-agnostic. Commits
  automatically invalidate the marker.
- **`files`**: explicit include/exclude globs (`**` supported).
  `HEAD` is intentionally not part of the hash, so commits outside the
  configured paths don't invalidate. Use this for narrow-scope gates
  (docs, Docker, coverage, ...).

## `.markgate.yml` (optional)

Only needed when you want multiple gates or the `files` hash. Looked up
at `$(git rev-parse --show-toplevel)/.markgate.yml` (no parent-dir
walking).

```yaml
gates:
  pre-commit:
    hash: git-tree

  pre-pr:
    hash: files
    include:
      - "docs/**"
    exclude:
      - "**/*.txt"
```

## Marker storage

Markers live at:

```text
$(git rev-parse --git-dir)/markgate/<key>.json
```

Inside `.git/`, so no gitignore entry is needed and worktrees stay
isolated. The on-disk JSON layout is an implementation detail; don't
parse it.

## Integration snippets

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
pnpm run check && markgate set
```

### husky

`.husky/pre-commit`:

```sh
markgate run -- pnpm run check
```

### lefthook

`lefthook.yml`:

```yaml
pre-commit:
  commands:
    check:
      run: markgate run -- pnpm run check
```

### pre-commit framework

`.pre-commit-hooks.yaml`:

```yaml
repos:
  - repo: local
    hooks:
      - id: markgate-check
        name: markgate check
        entry: markgate run -- pnpm run check
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

## FAQ

- **Does it work in git worktrees?** Yes. Markers live under each
  worktree's own `.git/` dir, so they don't leak across worktrees.
- **Do I need to gitignore anything?** No — markers are under `.git/`.
- **What if I don't want HEAD in the hash?** Use `hash: files` for that
  gate.
- **Does `files` respect `.gitignore`?** No. `files` is explicit scope
  by design. Use `git-tree` when you want `.gitignore`-aware behavior.
- **Can the marker be tampered with?** Locally, yes (it's a JSON file
  under `.git/`). Signed / remote-shared markers are a future
  consideration for CI-shared caches, not part of this release.

## License

MIT. See [LICENSE](LICENSE).
