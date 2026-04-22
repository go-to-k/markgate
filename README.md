# markgate

State-cached gate primitive for hook managers.

`markgate` records a hash of the repository state after your verification
steps (`typecheck`, `lint`, `build`, `test`, ...) pass. The next time a
hook — for example `pre-commit` — needs to decide whether those steps are
still fresh, it asks `markgate verify` which is a near-instant comparison
instead of re-running everything.

`markgate` is **not** a hook manager. It's a small CLI you compose into
the hook manager you already use (Claude Code hooks, husky, lefthook,
pre-commit, or a bare `.git/hooks/pre-commit`).

## Why

Coding agents and humans both tend to skip or duplicate verification
around commits. Existing pre-commit tooling (husky, lefthook,
pre-commit) is good at *running* checks but has no concept of
*remembering that they just ran and nothing relevant has changed since*.

`markgate` adds that memory:

- after `/check` (or whatever command) succeeds, call `markgate set <key>`
- in your commit hook, call `markgate verify <key>` — exit 0 means
  "state hasn't changed, skip re-running", exit 1 means "re-run"
- the marker is invalidated automatically when the working tree changes,
  so there's no TTL to tune

## Install

```sh
go install github.com/go-to-k/markgate/cmd/markgate@latest
```

Release binaries (darwin/linux, amd64/arm64) are published on
[GitHub Releases](https://github.com/go-to-k/markgate/releases).

## Quick start

```sh
# After your verification command succeeds, record a marker:
pnpm run check && markgate set check

# Later, decide whether to re-run:
markgate verify check || pnpm run check && markgate set check

# Or use the `run` sugar:
markgate run check -- pnpm run check
```

## Core concepts

### Exit codes

The public contract is exit codes. Every subcommand returns:

| code | meaning                                                    |
| ---- | ---------------------------------------------------------- |
| 0    | verified — state matches the marker, safe to skip          |
| 1    | not verified — no marker, or state differs                 |
| 2    | error — bad arguments, I/O failure, not in a git repo, ... |

This follows the `grep` / `diff` convention and composes cleanly with
shell `||`.

### Keys

Every marker is keyed. A key is a positional argument to every
subcommand and must match `[a-z0-9][a-z0-9-]*` (kebab-case ASCII).

Recommended names line up with git hook names:

- `pre-commit`
- `pre-push`
- `pre-pr`
- `check`

You can use any key you like; use as many as you want in one repo
(e.g. fast `pre-commit` gate plus a slower `pre-pr` gate).

### Hash types

Two strategies ship out of the box:

- **`git-tree`** (default, zero config) — hashes `HEAD` plus every file
  that differs from `HEAD` or is untracked-and-not-ignored. Deleted
  files are accounted for. Staging (`git add`) does not affect the
  hash. A new commit automatically invalidates the marker.

- **`files`** — hashes a set of paths defined by include/exclude globs
  (`**` supported). HEAD is intentionally not part of the hash, so
  commits that don't touch the configured paths do not invalidate the
  marker. Use this when you want narrow invalidation (e.g. docs-only
  checks).

### Marker storage

Markers live at:

```text
$(git rev-parse --git-dir)/markgate/<key>.json
```

- No gitignore entry needed; it's inside `.git/`.
- Worktrees get isolated markers automatically.
- `git clean -xdf` does **not** reach into `.git/`, so markers survive.
- But `rm -rf .git/markgate` wipes them cleanly if you ever want to.

The on-disk JSON layout is an implementation detail. Don't parse it.

## `.markgate.yml` (optional)

Without a config, every key uses `hash: git-tree`. To customize, drop a
`.markgate.yml` at the repo top level (the file is only looked up at
`$(git rev-parse --show-toplevel)/.markgate.yml`, no parent-dir walking):

```yaml
gates:
  pre-commit:
    hash: git-tree

  pre-pr:
    hash: files
    include:
      - "src/**/*.ts"
      - "tests/**/*.ts"
    exclude:
      - "**/*.md"
```

Notes:

- `hash: files` requires at least one `include` entry.
- Globs are evaluated by
  [`doublestar`](https://github.com/bmatcuk/doublestar). `**` matches
  any number of path segments; `.gitignore` is **not** respected here —
  if you want narrow scope, be explicit with include/exclude.
- Unknown keys and unknown hash types are hard errors (exit 2).

## CLI reference

```text
markgate set <key>                         Record the current state as the marker for <key>.
markgate verify <key>                      Exit 0 if the current state matches the marker, 1 otherwise.
markgate status <key>                      Show the marker plus a human-readable match/mismatch summary.
markgate clear <key>                       Delete the marker (idempotent; exit 0 even if nothing to delete).
markgate run <key> -- <cmd> [args...]      Sugar for verify + set: verify; on mismatch run <cmd>; on success set the marker.
markgate version                           Print the version.
```

### `markgate run`

`run` combines `markgate verify` and `markgate set` into a single
invocation, wedging `<cmd>` in between. It is sugar for:

```sh
markgate verify <key> || { <cmd> && markgate set <key>; }
```

- If the marker matches, `<cmd>` is not executed and `markgate` exits 0.
- Otherwise `<cmd>` is executed with stdio passed through. `SIGINT` and
  `SIGTERM` are forwarded to the child process.
- On success, the marker is written and `markgate` exits 0.
- On failure, the marker is **not** written and `markgate` exits with
  the child's exit code.

`run` does not provide timeouts, retries, parallel execution, or shell
interpretation — arguments after `--` are `execve`'d verbatim. If you
need any of that, use the plain `verify` / `set` pair with your own
runner.

## Integration snippets

### Claude Code (`PreToolUse` hook)

In `.claude/settings.json`:

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
            "command": "markgate verify pre-commit"
          }
        ]
      }
    ]
  }
}
```

Then, at the end of your check skill:

```sh
pnpm run check && markgate set pre-commit
```

When the agent attempts `git commit` without a fresh marker, the hook
exits 1 and the agent is told to re-run `/check`.

### husky

`.husky/pre-commit`:

```sh
markgate verify pre-commit || {
  echo "Run your /check command first." >&2
  exit 1
}
```

Or, if you want husky itself to run the check on miss:

```sh
markgate run pre-commit -- pnpm run check
```

### lefthook

`lefthook.yml`:

```yaml
pre-commit:
  commands:
    check:
      run: markgate run pre-commit -- pnpm run check
```

### pre-commit framework

`.pre-commit-hooks.yaml` (local repo):

```yaml
repos:
  - repo: local
    hooks:
      - id: markgate-check
        name: markgate check
        entry: markgate run pre-commit -- pnpm run check
        language: system
        pass_filenames: false
```

### Bare `.git/hooks/pre-commit`

```sh
#!/bin/sh
markgate verify pre-commit || {
  echo "Run the verification command first, then commit." >&2
  exit 1
}
```

## FAQ

**Does `markgate` work in a git worktree?**
Yes. `markgate` resolves its storage directory via
`git rev-parse --absolute-git-dir`, which points at the worktree's own
git dir, so markers don't leak across worktrees.

**Do I need to gitignore anything?**
No. Markers live under `.git/`, which is never tracked.

**What happens if the config is missing or the key isn't configured?**
`markgate` uses `hash: git-tree` — the safe default — for any key that
isn't explicitly configured.

**Why doesn't `files` respect `.gitignore`?**
`files` is an explicit-scope hash: you tell `markgate` which paths
matter. If `.gitignore` filtering is what you want, either use the
default `git-tree` hash or exclude the ignored paths yourself.

**Can I use this in CI?**
Yes, for local-style caching inside a single job. Cross-job / cross-
machine sharing (signed markers, remote cache) is not in scope for this
release.

## License

MIT. See [LICENSE](LICENSE).
