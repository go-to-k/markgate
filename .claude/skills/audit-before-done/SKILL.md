---
name: audit-before-done
description: Pre-push / pre-done-declaration discipline for markgate. Covers implementation anti-patterns (no churn, no silent deletion), a widened proactive audit (grep every introduced name, check --help output, smoke the built binary, mutation-test every check you add, run make lint — go vet is not enough), why a self-audit is not a review, and the post-merge manual smoke pattern. Use whenever you're about to say "done", mark a task complete, push a branch, create a PR, or ask for merge. Companion to `iterate-design`, which covers the pre-code design phase.
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
- **Check your branch before every `git commit` / `git push`.**
  Run `git branch --show-current`. If it says `main`, stop — create
  or switch to a feature branch first. The `guard-main-branch.sh`
  PreToolUse hook blocks `git commit`/`push`/`merge`/`rebase` on
  main as a safety net, but the check belongs in your head too. A
  lost-then-recovered commit from main is expensive to unwind (cherry
  -pick onto a new branch, hard-reset main); the 2-second branch
  check before committing prevents it.
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
- **Delete each check you added and confirm a test fails.** A suite
  that stays green without your check is not protecting it. Over one
  session that was true of eight checks, one of which reintroduced
  the very bug its PR closed while `go test ./...` and the e2e smoke
  both stayed green. Do it on a copy
  (`git archive HEAD | tar -x -C /tmp/...`), never the worktree.
  The same pass catches assertions that never reach the code they
  name — a test calling `Scope` when the check lives in `Hash`, an
  exit-code assertion on a flag the command does not register, a
  fixture seeded *after* the precondition broke so the file never
  existed. Three separate cases in that session, all invisible until
  the mutation. Mutate in both directions when the change has one:
  a guard that must fire, and must not fire on the legitimate case.

Report the result — "no gaps after N checks" or "one stale link,
fixing" — in the same message that announces completion. If the
user then finds something the audit missed, that's a signal the
checklist above needs a new entry.

## A self-audit is not a review

Phase 4 is a *self*-audit, and it keeps growing because self-audits
keep coming back clean while something is still wrong. Over one
session it reported "no gaps" five times; an independent reviewer
found a real defect all five, including a blocker that broke the
exact workflow the README recommends.

So for anything that changes behavior, get an independent review
before asking for merge — a subagent with `isolation: "worktree"`,
briefed to *verify claims by running things* rather than read the
diff, and told the author judged its own work. Hand it the failure
modes to hunt, not a summary of what you did.

Two things make the difference, and neither is available to you on
your own work:

- It re-derives your claims instead of checking them off. Numbers in
  a PR body are claims: a "142ms -> 58ms" in that session came from
  a different fixture and did not reproduce.
- It has no stake in the design holding, so it probes the case you
  had already decided was fine. That is where the blocker was — a
  scope that empties out *after* a marker exists, which the
  bootstrapping test covered only in the no-marker half.

The bar for filing what it finds does not move because you wrote the
code. Having just authored something is a reason to file, not to
wait for more occurrences — "only twice so far" was used to defer a
report in that session and was wrong on the numbers (twice out of
two opportunities, mechanism already understood).

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
make lint        # pinned golangci-lint via `go run`, mirrors CI
```

`make lint` catches — in descending order of this session's hit
rate — shadow declarations, unused variables, unchecked errors,
and format-string mismatches. If you only ran `go test && go vet`
and `make lint` is still unproven, the CI run is the audit, which
is too late.

CI also validates the **PR title**
(`amannn/action-semantic-pull-request`): the type comes from a fixed
list, and the scope from an allowlist of `internal/` package names
(`cli`, `config`, `gitutil`, `hasher`, `key`, `state`, plus `deps`
and `run`). A change under `.claude/` has no matching scope — use
no scope rather than inventing one. `chore(hooks):` fails.

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
