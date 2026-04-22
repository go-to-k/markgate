---
name: audit-before-done
description: Pre-push / pre-done-declaration discipline for markgate. Covers implementation anti-patterns (no churn, no silent deletion), a widened proactive audit (grep every introduced name, check --help output, smoke the built binary, run make lint — go vet is not enough), and the post-merge manual smoke pattern. Use whenever you're about to say "done", mark a task complete, push a branch, create a PR, or ask for merge. Companion to `iterate-design`, which covers the pre-code design phase.
---

# audit-before-done

The discipline you apply **between writing code and declaring it
finished**. The goal is to catch the things the user would otherwise
find — stale help text, missed edge-case tests, CI lint failures
that your local `go vet` didn't catch, smoke bugs in `main.go`
wiring that unit tests miss.

Invoke this whenever you're tempted to say any of:

- "done"
- "no gaps"
- "ready to commit"
- "pushing now"
- "ready for review"
- "complete"

And also right before `git push`, `gh pr create`, or moving a todo
to `completed` on non-trivial work.

## Phase 3 — implementation discipline

- Use `TodoWrite` to break the work into verifiable steps.
- Keep diffs minimal — no drive-by refactors, no hypothetical
  configurability, no "just in case" validation.
- **Don't delete something because you're unsure it works — test
  it.** If a hook, script, or feature might be broken, build a
  minimal reproduction (temp dir, fake input, run it) and verify.
  Removing the thing to avoid shipping uncertainty is
  under-delivering; confirming with a 30-second experiment is what
  the user is paying for.
- **Don't churn edits on the same region.** If an edit turns out
  to be wrong, revert to the previous clean state in one operation,
  then make the correct edit. A sequence of tiny patches that adds
  then removes the same phrase is a sign of indecision, not
  progress — the diff review becomes noisy and the history becomes
  harder to follow.

## Phase 4 — audit (proactive)

Before reporting done, self-audit without waiting to be asked:

- Every item from the pre-code sketch was delivered?
- Precedence / edge-case tests exist (absolute & relative paths,
  empty strings, all pairwise precedence combinations, orthogonal
  feature combos)?
- README consistency: Use cases, CLI reference, Sharing markers,
  FAQ, link fragments, any "out of scope" notes that just became
  in-scope?
- `init.go` skeleton needs a comment?
- Package doc comment (`// Package xyz …`) still accurate?

**Don't settle for "no gaps" from a superficial check.** A
self-audit that returns empty is usually a narrow audit, not a
complete feature. When tempted to declare "complete", widen the
lens:

- Grep for every name introduced (flag names, env vars, config
  keys, new exported symbols) and confirm every doc and code
  reference matches.
- **Check the runtime surface, not just files.** Run `<binary>
  <subcommand> --help` and compare against the README's CLI
  reference and env var list. When a new source is added to a
  precedence chain (e.g. config layer added to `flag > env >
  default`), godoc on the related constants, flag `--help`
  strings, and package doc comments all tend to still describe the
  *old* chain. These are user-facing too and drift silently.
- Run a fresh smoke test of the *built* binary in a throwaway
  repo, not just `go test ./...`. Failed smokes are valuable
  data — they often expose docs gaps or wiring bugs that unit
  tests don't.
- Cross-check claims that point at other sections: if the Why
  section says "four passes, one change", are all four passes
  actually wired up, or is one of them aspirational?

Report the result — "no gaps after N checks" or "one stale link,
fixing" — in the same message that announces completion. If the
user then finds something the audit missed, that's a signal the
checklist above needs a new entry.

## Mirror what CI runs, locally

`go test ./... && go vet ./...` is **not** what CI runs. This
project's CI runs golangci-lint with `govet: enable: [shadow]`
plus `gocritic`, `staticcheck`, `gosec`, `errcheck`, `errorlint`,
`ineffassign`, `misspell`, `nilerr`, `nilnil`, `unconvert`,
`unparam`, `unused`, and formatters (`gofmt`, `goimports`). Plain
`go vet` doesn't enable most of these.

Before pushing or marking done, run:

```sh
go test ./...
make lint        # golangci-lint run, mirrors CI
```

`make lint` catches — in descending order of this session's hit
rate — shadow declarations, unused variables, unchecked errors,
and format-string mismatches. If you only ran `go test && go vet`
and `make lint` is still unproven, the CI run is the audit, which
is too late.

## Post-merge: manual smoke

Integration tests cover most cases, but when the feature changes
how the built binary behaves (new flag / env / path handling), run
a quick smoke against a fresh build in a throwaway repo.
Integration tests exercise the cobra root, not the compiled
binary — they won't catch `main.go` wiring bugs or
version-injection issues.

```sh
go build -o /tmp/markgate ./cmd/markgate
cd "$(mktemp -d)" && git init -q -b main && \
  git config user.email t@e && git config user.name t && \
  echo seed > seed.txt && git add . && git commit -qm init
/tmp/markgate <new-flag-under-test>
```

Fold surprising smoke findings back into the README or code
immediately — a smoke that needs an undocumented setup step is
usually a docs gap, not a smoke problem.

## What this skill is not

- Not a license to over-audit one-line fixes. Size the audit to
  the risk.
- Not a substitute for Phase 1-2 design discipline — this skill
  assumes the design was already agreed via `iterate-design`.
