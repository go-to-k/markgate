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
- `internal/hasher/` — `GitTree`, `Files` and `Diff` strategies
  implement `Hasher`.
- `internal/gitutil/` — `git rev-parse` wrappers (top-level, git-dir,
  HEAD SHA), merge-base resolution, and the raw working-tree delta a
  `hash: diff` gate digests.
- `internal/key/` — key syntax validation (`[a-z0-9][a-z0-9-]*`).

## Design principles

- **Zero-config default must keep working.** Adding a feature MUST NOT
  require editing `.markgate.yml` to preserve existing behavior. Any
  new override follows the chain:
  **CLI flag > env var > `.markgate.yml` > default**.
  The canonical wire-up point is `newGateCtx` in
  [internal/cli/helper.go](internal/cli/helper.go); whatever precedence
  helper an existing override uses is the template for the next one.
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
- **A command's preconditions are the ones it actually has.** Config
  validation lives in `config.Load`, and every command that resolves
  a hasher goes through it. `clear` does not — it deletes a file, and
  its only config dependency is `state_dir` — so it uses
  `config.Parse` instead. Requiring a valid document there put the
  recovery path inside the trap it recovers from: one bad gate made
  *every* marker unremovable, including markers for valid gates, with
  hand-editing YAML as the only way out. Skipping validation for one
  command is not the same as weakening it; if you add a command that
  genuinely needs less than `newGateCtx` provides, give it a narrower
  resolver rather than loosening the shared one.
- **A digest that cannot change must never read as fresh.** An empty
  scope hashes to the SHA-256 of nothing — the same constant for
  every such gate — so the marker matches forever and the gate can
  never block. Two sentinels mark it: a dead `include` list
  (`hasher.ErrDeadScope`) and an empty `hash: diff` delta
  (`hasher.ErrDiffBase`).
  **Refuse at `set`, not on the read path.** `set` is where the
  constant would be recorded, and by then any gated command has had
  its chance to fill the scope. `verify` must stop *passing*, which
  exit 1 already achieves; escalating it to exit 2 stops `markgate
  run` from executing the command that would refill the scope, and a
  gate on build output between `make clean` and the next build is
  then recoverable only via `clear`. That regression shipped once —
  the test covered bootstrapping with no marker and missed the second
  build cycle.
  **Refuse only where the configuration is at fault.** A live scope
  this branch happens not to have touched is legitimate and must stay
  usable. What counts as "live" is per strategy: `files` hashes what
  is on disk, `diff` draws from a git delta that can never contain an
  ignored path, so using the working tree as the universe for `diff`
  is a false all-clear, not a false alarm.

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
go vet ./...          # basic misuse check
make lint             # golangci-lint with govet shadow + gocritic +
                      # staticcheck + gosec + etc. Mirrors CI.
                      # Runs a pinned version via `go run`, so it needs
                      # no local install and cannot drift from
                      # .golangci.yml (which is v2-format: a v1 binary
                      # exits with a config error and lints nothing).
```

Before reporting a task complete, run `go test ./... && make lint`.
Plain `go vet` is **not enough**: CI's golangci-lint enables shadow
detection, ineffassign, unused-variable, format-string checks, and
more that `go vet` alone misses. If only `go vet` passed, CI is your
audit — which is too late.

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

Two project skills split the workflow by triggering moment:

- **[iterate-design](.claude/skills/iterate-design/SKILL.md)** — fires
  when the user asks for an opinion or proposes a new flag / env /
  config field. Sketch options, pick one, hold the line if the user
  pushes back, get sign-off before coding.
- **[audit-before-done](.claude/skills/audit-before-done/SKILL.md)** —
  fires when you're about to say "done" / push / create a PR. Covers
  implementation discipline, the widened proactive audit, and the
  "run what CI runs" rule (`make lint`, not just `go vet`). Use this
  before every `git push` on non-trivial work.

### Subagents and branches

If you spawn an Explore or general-purpose subagent and the task
needs a specific branch, **pin the branch in the prompt** ("on branch
`feat/foo`, check …") and — since the subagent can `git checkout` in
your working tree — `git checkout` back to your original branch after
it returns. Otherwise subagent findings can come from the wrong
branch (e.g. "function X doesn't exist" because the agent was on a
branch where X hasn't landed yet).

For review tasks that span multiple branches, or to avoid *any* risk
of the subagent shifting your working tree, pass
`isolation: "worktree"` when spawning it. The agent runs against a
throwaway git worktree, so its checkouts / edits never leak back.

Always verify a subagent's negative findings ("X doesn't exist", "no
reference to Y") against your current branch with `grep` before
acting — the answer is only as good as the branch the agent read.

### Commit messages with non-ASCII

When a commit message contains Japanese or other non-ASCII text,
pass it via `git commit -F <file>` rather than a bash heredoc:
heredoc + `$(...)` substitution can mis-parse embedded
question-marks or quotes and fail with "unexpected EOF". Writing the
message to a temp file first sidesteps all shell escaping.

## Harness (.claude/)

- `settings.json` pre-allows read-only dev commands (`go test`,
  `go vet`, `git status`, `gh pr view`, ...) to cut permission
  prompts.
- `hooks/go-vet-on-edit.sh` is a PostToolUse hook: after every
  Edit/Write on a `.go` file it runs `go vet ./...`, and it dogfoods
  markgate to skip the run when repo state hasn't changed since the
  last pass. A vet failure exits 2, so the diagnostic is surfaced
  back to Claude as blocking feedback.
- `hooks/guard-main-branch.sh` is a PreToolUse hook on Bash: blocks
  `git commit` / `git push` / `git merge` / `git rebase` when the
  current branch is `main`. Protects against the "PR merged → local
  branch still on main (or got switched) → Claude commits to main"
  mistake, which the project's branch-protection rule catches at
  push time but only after the commit has already landed locally
  (expensive to unwind). Exits 2 with a message pointing at the fix
  (`git checkout -b <feature-branch>`). Matches the verb anywhere in
  the command, not just at the start: these almost always arrive
  inside a compound (`git add -A && git commit ...`), and a
  prefix-only match let exactly that through once. Global options are
  matched too (`git -C <dir> commit`), since operating on the repo
  from another cwd is how the mistake happens in the first place.
  Heredoc bodies are excised first, so writing a script that merely
  *contains* a git command is not mistaken for running one —
  `scripts/e2e.sh` has eight of them. Quoted strings deliberately are
  not excised: `bash -c "git commit"` is a real commit, so a heredoc
  body (which the shell can never execute) and a quoted string (which
  it can) are not the same case.
- `hooks/guard-main-branch.test.sh` is the matrix for the above. Run
  it after touching that hook: `bash
  .claude/hooks/guard-main-branch.test.sh` (exit code = failures).
  It asserts **both** directions, because the hook has failed in both
  — once missing a real commit, once blocking a file write — and each
  failure is silent in its own way: a miss puts a commit on `main`, a
  false positive teaches the reader to route around the guard. Adding
  a case to only the direction that last broke is how the other one
  comes back.
- `hooks/e2e-pre-merge.sh` is a PreToolUse hook on Bash: before any
  `gh pr merge ...` it runs `.claude/scripts/e2e.sh` (the full
  black-box CLI smoke), wrapped in `markgate run` so unchanged
  repos skip in ~0.1s. Failure exits 2 and blocks the merge. The
  same script is invokable manually via the `verify-e2e` skill.
- `scripts/e2e.sh` is the black-box CLI smoke (built binary +
  `git init` sandboxes per section). Covers original primitives
  (set / verify / clear / run / init / version, default key,
  `--hash files`, `--state-dir`, env-var, precedence) and every
  feature added in the 2026-05-09 batch (completion, config lint,
  TTL, `--explain`, bare status, composes / requires) plus
  `hash: diff` (#68) and the dead-include refusal shared by both
  scoped hash modes (#70). The script's summary prints the assertion count;
  exit code = number of failures. Do not restate the count here — it
  has no way to stay in sync and has already drifted once.

### How the dogfood works (no install needed)

The hook invokes markgate via `go run ./cmd/markgate` rather than a
globally installed binary. Benefits:

- Always reflects the current source — no `go install` / `make build`
  to keep in sync.
- Go's content-based build cache makes steady-state invocations fast
  (~0.1s skip, ~0.3s run after cold compile). Only the first compile
  after `go clean -cache` is slow (~2–3s).
- Nothing lands outside the repo.

Markers for these hooks live at `.git/markgate/hook-vet.json` and
`.git/markgate/hook-e2e-pre-merge.json` (default location, git-
ignored by virtue of being inside `.git/`).
