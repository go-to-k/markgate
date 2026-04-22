---
name: iterate-design
description: Pre-code design iteration for non-trivial features on markgate. Forces a design sketch with trade-offs before writing code, and guards against silent opinion-flipping when the user pushes back. Use when the user asks for an opinion ("what do you think?", "should we?", "recommended?"), proposes a new flag / env / config field, or raises a feature with multiple valid shapes. For the matching pre-push / done-declaration discipline (implementation anti-patterns, audit checklist, smoke-the-binary), see the companion `audit-before-done` skill.
---

# iterate-design

A compact pre-code workflow for design-heavy work on markgate. Goal:
agree on the shape before any code is written, and don't drift on the
recommendation once taken. Implementation discipline and the
pre-push audit live in the companion skill `audit-before-done`.

The patterns this skill guards against:

1. Jumping to implementation before the shape is agreed.
2. Flipping recommendation based on user reframing without anchoring
   on the original reasoning.

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
   because …". Then address the sub-question.
4. Never present an opinion as "it depends" unless it really does.
   Pick one and defend it.

Caution signs that you are drifting:

- Your previous message recommended A; this message recommends B
  with no new information cited.
- You labeled something "not recommended" without a concrete failure
  mode. Warnings without evidence are noise.
- You explained a failure mode as "the marker becomes stale" and
  stopped there. Finish the chain: "stale → verify returns 1 →
  check re-runs → the whole point of the feature is defeated."

## Handoff

Once the user signs off on the sketch and you start writing code,
the `audit-before-done` skill takes over: implementation discipline
(no churn, no deletion on uncertainty), the widened audit
(grep every introduced name, check `--help`, smoke the built binary,
run `make lint` not just `go vet`), and the pre-push checklist.

## What this skill is not

- Not a substitute for reading the code before proposing changes.
- Not a reason to over-consult the user on trivial decisions. If the
  change is one-line or obvious, just do it.
