---
name: iterate-design
description: Structured design iteration for non-trivial features on markgate. Forces a design sketch with trade-offs before code, guards against silent opinion-flipping when the user pushes back, and audits plan-vs-implementation proactively at the end. Use when the user asks for an opinion ("what do you think?", "should we?", "recommended?"), proposes a new flag / env / config field, or raises a feature with multiple valid shapes.
---

# iterate-design

A compact workflow for design-heavy work on markgate. The goal is to
avoid three patterns that waste the user's time:

1. Jumping to implementation before the shape is agreed.
2. Flipping recommendation based on user reframing without anchoring
   on the original reasoning.
3. Shipping a feature, then discovering the audit the user would have
   asked for anyway.

## Phase 1 — design sketch (before code)

Produce a short sketch the user can react to:

1. **Goal in one sentence.** What problem, for whom.
2. **Options.** 2–4 total. More than 4 causes paralysis. Each option
   gets a one-liner plus its main trade-off.
3. **Recommendation.** Pick one. Say why, referring to the goal.
4. **Decisions that aren't obvious.** Flag name, env var name,
   precedence chain, config field location, validation boundaries,
   where the docs need to change.
5. **Ask for sign-off.** Do not write code yet.

When the feature is an override, default to the markgate precedence:
**CLI flag > env var > `.markgate.yml` > default**. Deviating needs
an explicit reason.

## Phase 2 — hold the line

When the user pushes back, don't silently flip. Sequence:

1. Restate **why** the original position was taken (the constraint,
   the past failure mode, the existing invariant).
2. If the new information actually changes that reasoning, say so
   explicitly: "this changes the calculation because X → Y → Z".
3. If it doesn't, say that too: "the concern you raised still holds,
   because ...". Then address the sub-question.
4. Never present an opinion as "it depends" unless it really does.
   Pick one and defend it.

Caution signs that you are drifting:

- Your previous message recommended A; this message recommends B with
  no new information cited.
- You labeled something "not recommended" without a concrete failure
  mode. Warnings without evidence are noise.
- You explained a failure mode as "the marker becomes stale" and
  stopped there. Finish the chain: "stale → verify returns 1 → check
  re-runs → the whole point of the feature is defeated."

## Phase 3 — implementation

- Use `TodoWrite` to break the work into verifiable steps.
- Keep diffs minimal — no drive-by refactors, no hypothetical
  configurability, no "just in case" validation.
- Run `go test ./... && go vet ./...` before claiming done.
- **Don't delete something because you're unsure it works — test it.**
  If a hook, script, or feature might be broken, build a minimal
  reproduction (temp dir, fake input, run it) and verify. Removing the
  thing to avoid shipping uncertainty is under-delivering; confirming
  with a 30-second experiment is what the user is paying for.
- **Don't churn edits on the same region.** If an edit turns out to
  be wrong, revert to the previous clean state in one operation, then
  make the correct edit. A sequence of tiny patches that adds then
  removes the same phrase is a sign of indecision, not progress — the
  diff review becomes noisy and the history becomes harder to follow.

## Phase 4 — audit (proactive)

Before reporting done, self-audit without waiting to be asked:

- Every item from the Phase 1 sketch was delivered?
- Precedence / edge-case tests exist (absolute & relative paths,
  empty strings, all pairwise precedence combinations, orthogonal
  feature combos)?
- README consistency: Use cases, CLI reference, Sharing markers, FAQ,
  link fragments, any "out of scope" notes that just became in-scope?
- `init.go` skeleton needs a comment?
- Package doc comment (`// Package xyz ...`) still accurate?

**Don't settle for "no gaps" from a superficial check.** A self-audit
that returns empty is usually a narrow audit, not a complete feature.
When tempted to declare "complete", widen the lens:

- Grep for every name introduced (flag names, env vars, config keys,
  new exported symbols) and confirm every doc and code reference
  matches.
- **Check the runtime surface, not just files.** Run `<binary>
  <subcommand> --help` and compare against the README's CLI
  reference and env var list. When a new source is added to a
  precedence chain (e.g. config layer added to `flag > env > default`),
  godoc on the related constants, flag `--help` strings, and package
  doc comments all tend to still describe the *old* chain. These are
  user-facing too and drift silently.
- Run a fresh smoke test of the *built* binary in a throwaway repo,
  not just `go test ./...`. Failed smokes are valuable data — they
  often expose docs gaps or wiring bugs that unit tests don't.
- Cross-check claims that point at other sections: if the Why
  section says "four passes, one change", are all four passes
  actually wired up, or is one of them aspirational?

Report the result — "no gaps after N checks" or "one stale link,
fixing" — in the same message that announces completion. If the user
then finds something the audit missed, that's a signal the checklist
above needs a new entry.

## Post-merge: manual smoke

Integration tests cover most cases, but when the feature changes how
the built binary behaves (new flag / env / path handling), run a quick
smoke against `go build -o /tmp/markgate ./cmd/markgate` in a throwaway
`git init` repo. Integration tests exercise the cobra root, not the
compiled binary — they won't catch `main.go` wiring bugs.

## What this skill is not

- Not a substitute for reading the code before proposing changes.
- Not a reason to over-consult the user on trivial decisions. If the
  change is one-line or obvious, just do it.
