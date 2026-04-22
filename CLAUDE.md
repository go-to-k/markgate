# markgate development notes

markgate is a verification-state cache for hook managers. Users run
`markgate run -- <cmd>` (or the `verify` / `set` / `clear` building
blocks) to skip re-running a check when nothing relevant has changed
since the last success. This file captures conventions for working on
the tool itself; user-facing docs live in [README.md](README.md) and
it is the spec — see "README as a spec" below.

## Layout

- `cmd/markgate/` — entrypoint; wires cobra into a binary.
- `internal/cli/` — cobra commands (`set`, `verify`, `status`, `clear`,
  `run`, `init`, `version`). Shared wiring is in `helper.go`:
  - `newGateCtx` is the single reconciliation point for flags + env +
    config. Every command goes through it.
  - `addGateFlags` registers the override flags each command accepts.
- `internal/config/` — `.markgate.yml` parsing (`Gate` struct,
  `Config.Gate(key)`).
- `internal/state/` — marker read / write. Atomic save via temp-file +
  fsync + rename. Callers never touch files directly.
- `internal/hasher/` — `GitTree` and `Files` strategies implement
  `Hasher`.
- `internal/gitutil/` — `git rev-parse` wrappers (top-level, git-dir,
  HEAD SHA).
- `internal/key/` — key syntax validation (`[a-z0-9][a-z0-9-]*`).

## Design principles

- **Zero-config default must keep working.** Adding a feature MUST NOT
  require editing `.markgate.yml` to preserve existing behavior. Any
  new override follows the chain:
  **CLI flag > env var > `.markgate.yml` > default**.
  See `resolveMarkerPath` in [internal/cli/helper.go](internal/cli/helper.go)
  for the canonical implementation.
- **Exit codes follow `grep` / `diff`**: 0 match, 1 mismatch, 2 error.
  Errors surface as `&ExitError{Code: 2, Err: err}` — never panic on
  user-facing failures.
- **Atomic writes** via temp-file + fsync + rename (see `state.Save`).
  A crash mid-write leaves either the old marker or nothing — never a
  truncated file.
- **Relative paths resolve against the repo top-level**, never cwd.
  This keeps hook-invoked commands deterministic regardless of where
  they run from.
- **No implicit nesting of `markgate/`** when the user gives an
  explicit path (`--state-dir` / `state_dir:`). The user owns the
  layout they asked for.

## Testing

- End-to-end CLI tests live in
  [internal/cli/integration_test.go](internal/cli/integration_test.go).
  They drive the root command via `newRootCmd` + `root.Execute`.
  **Prefer adding tests here** for new CLI behavior — internal unit
  tests tend to re-verify what integration already covers.
- Helpers:
  - `initRepo(t)` — creates a fresh git repo in a temp dir and
    `t.Chdir`s into it (auto-restored).
  - `runCmd(t, args...)` — invokes the CLI, returns
    `(exitCode, stdout)`.
  - `writeRepoFile(t, dir, rel, body)` — writes a file under the repo.
- Use `t.Setenv` for env-var coverage (auto-restored).
- For precedence features, test each pair explicitly
  (`flag > env`, `env > config`, `flag > config`) rather than only
  end-to-end. See the `TestStateDir_*` cluster as the pattern.

## Commands

```sh
go build ./...        # compile check
go test ./...         # full test suite (CLI tests spawn real git repos)
go vet ./...          # catches misuse the compiler misses
```

Before reporting a task complete, run `go test ./... && go vet ./...`.

## Style

- **No comments that describe *what* the code does.** Identifier names
  do that. Add a comment only when the *why* is non-obvious (an
  invariant, a subtle precedence rule, a workaround).
- Match the existing terse comment voice — see `state.Save`'s
  description of the temp-file dance.
- No emojis in code, commits, docs, or responses unless the user asks.
- Imports grouped stdlib / third-party / internal with blank lines
  between groups (gofmt + the existing files agree).

## README as a spec

The README is the user-facing spec, not marketing. When changing
behavior, update the README in the same change. Pay attention to:

- **Use cases** — keep concrete examples working and honest.
- **CLI reference / Per-invocation overrides / Environment variables
  / Sharing markers** — these sections cross-reference. Touch one,
  audit the others.
- **FAQ** — likely to contain answers that touch the changed area.
- **Link fragments** — if you rename a heading, fix every
  `#heading-slug` reference in the file.

## Working with Claude on this repo

- When the user asks for an opinion or proposes a new flag / env /
  config field with multiple valid shapes, reach for the
  [iterate-design](.claude/skills/iterate-design/SKILL.md) skill —
  sketch options, pick one, get sign-off before coding.
- When you implement a non-trivial feature, audit plan-vs-actual
  proactively before reporting done (same skill).
- Hold the line on recommendations. If the user pushes back, restate
  the original reasoning first. Only flip if new information genuinely
  invalidates it — and say so explicitly.

## Harness (.claude/)

- `settings.json` pre-allows read-only dev commands (`go test`,
  `go vet`, `git status`, `gh pr view`, ...) to cut permission
  prompts.
- `hooks/go-vet-on-edit.sh` is a PostToolUse hook: after every
  Edit/Write on a `.go` file it runs `go vet ./...`, and it dogfoods
  markgate to skip the run when repo state hasn't changed since the
  last pass. A vet failure exits 2, so the diagnostic is surfaced
  back to Claude as blocking feedback.

### How the dogfood works (no install needed)

The hook invokes markgate via `go run ./cmd/markgate` rather than a
globally installed binary. Benefits:

- Always reflects the current source — no `go install` / `make build`
  to keep in sync.
- Go's content-based build cache makes steady-state invocations fast
  (~0.1s skip, ~0.3s run after cold compile). Only the first compile
  after `go clean -cache` is slow (~2–3s).
- Nothing lands outside the repo.

Marker for this hook lives at `.git/markgate/hook-vet.json` (default
location, git-ignored by virtue of being inside `.git/`).
