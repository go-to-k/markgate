# markgate

`markgate` is the mechanical enforcement layer between an AI
coding agent and its hooks — ensure **non-duplicate runs**,
enforce **non-command tasks** (e.g., LLM review), and aggregate
**multi-task verdicts** into one.

## What markgate does

Coding agents forget — context loss, token pressure, hurry.
Wiring hooks to reliably run a required task (a check (lint, test,
build), an LLM-judged review, a code-generation step, or any
operation with a pass/fail outcome) runs into **three recurring
challenges**. markgate addresses these with **three patterns**,
each backed by one of **two primitives** — `markgate run`
(one-shot) or `markgate set` + `markgate verify` (the Gate
pattern).

| Pattern | Goal | Mechanism |
| --- | --- | --- |
| 1 | Ensure non-duplicate runs | `markgate run` |
| 2 | Enforce non-command tasks | `markgate set` + `markgate verify` |
| 3 | Scope each task & aggregate the verdict | `.markgate.yml` with `composes` |

### Pattern 1: ensure non-duplicate runs (`markgate run`)

You tell your coding agent to run `/check` (test, lint, build, doc
consistency) before committing. **Sometimes it forgets** — context
loss, token pressure, hurry — and commits anyway.

![Agent forgets the check and commits anyway](docs/images/forgetting-check.png)

So you add a pre-commit hook to enforce the check. Now every commit
runs the check twice, once by the agent, once by the hook. Heavy
checks slow the dev loop; light ones still add up.

![Agent and hook double-run the check](docs/images/duplicate-execution.png)

Pulling the check out of the agent and leaving it only in the hook
isn't the answer — you can't run it before you're ready to commit.
Per-edit hooks aren't either — they pay the cost on every edit.

`markgate run` resolves the dilemma: keeping both the check site and
the hook in place, **the hook re-runs the check only when the agent
forgot**. When the agent ran the check properly, **the hook becomes
a near-instant no-op** — no duplicate execution.

![markgate run fires the hook only when the agent forgot](docs/images/markgate-resolves.png)

Adoption is one line — prefix your check command in **both** the place
that runs it and the hook that enforces it:

```diff
- pnpm build
+ markgate run -- pnpm build
```

In your Claude Code `PreToolUse` hook on `git commit*`:

```diff
// .claude/settings.json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "if": "Bash(git commit*)",
        "hooks": [
-         { "type": "command", "command": "pnpm build" }
+         { "type": "command", "command": "markgate run -- pnpm build" }
        ]
      }
    ]
  }
}
```

For other hook managers (husky, lefthook, pre-commit framework), the
shape is identical — see [Drop into your hook manager](#drop-into-your-hook-manager).

### Pattern 2: enforce non-command tasks (`set` + `verify`)

Some tasks aren't commands. "Did `/check-docs` find docs out of
sync with src?" "Did `/investigate-aws` find anything wrong?" An
LLM-led skill can work through these — judging, investigating, or
updating step by step. You want the **agent's own session** to do
this work, where its built-up context (conversation history, open
files, prior decisions) is already in play. A hook can't reach
into that session, and shelling out to `claude -p` only spawns a
fresh one with no access to the agent's state. So when the agent
forgets or skips the task, the hook has no grip on whether it
actually happened.

`markgate set` + `markgate verify` give the hook a grip by splitting
the run. The skill — wherever it naturally lives, like `/check-docs`,
`/investigate-aws`, or any agent-driven step — ends with `markgate set`,
which writes a small **marker** recording the pass. The hook calls
`markgate verify` to read it. The hook still can't run the skill
itself, but it can **refuse to proceed unless the marker confirms it
ran**.

The agent implements, runs `/check-docs`, the marker passes. But
after another code edit, you'd want `/check-docs` to run again —
and if the agent forgets, the marker no longer matches the new
code, so the hook blocks until `/check-docs` runs again.

![set drops a marker; verify reads it](docs/images/markgate-set-verify.png)

Adoption is one line on each side:

```sh
# At the end of /check-docs (or any agent-driven step):
markgate set

# In a pre-commit hook (.claude/settings.json, PreToolUse on git commit*):
markgate verify || { echo "Run /check-docs before committing." >&2; exit 1; }
```

### Pattern 3: scope each task to its files, aggregate the verdict (`.markgate.yml` + `composes`)

As tasks accumulate — code check on `src/**`, docs review on
`docs/**`, vuln scan on `package-lock.json` — you want each one to
**fire only when its own files change**. The default whole-repo
hash doesn't allow that: a code-only edit invalidates the docs
marker, a docs-only edit invalidates the vuln-scan marker, so the
hook re-fires tasks that nothing relevant moved. And lining up N
`markgate verify` calls in the hook clutters the config in
proportion to how many tasks you add.

With `.markgate.yml` (created by `markgate init`), each task gets
its own **scoped gate** (its own `include` globs), and the hook
verifies a **parent gate** that ANDs them all via `composes:`. The
code check fires when `src/**` moves; the docs review fires when
`docs/**` moves. Edits outside every scope (CI config, editor
settings) invalidate nothing — the hook stays silent.

```yaml
# .markgate.yml
gates:
  check:
    hash: files
    include: ["src/**", "tests/**"]
  docs:
    hash: files
    include: ["src/**", "docs/**", "README.md"]
  pre-commit:
    composes: [check, docs]
```

Once each task owns its scope, the hook needs **just one verify** —
when every child is fresh, the parent passes.

![Children freshen markers; parent verify ANDs them](docs/images/composes-aggregate.png)

Adoption:

```sh
# Each task freshens its own marker, wherever it lives:
pnpm build && markgate set check
# Inside the /check-docs skill body (LLM-led — see Pattern 2):
markgate set docs

# One verify in the hook covers both:
markgate verify pre-commit || { markgate status pre-commit >&2; exit 1; }
```

`markgate run` can't write an aggregate gate: `run` executes a single
command, and an aggregate gate has none. **Aggregate verify is
split-only.**

See [Use case 4](#4-pre-commit-isolate-a-slow-check-with-its-own-scoped-gate)
for the invalidation matrix and a real-world wire-up,
[Use case 5](#5-pre-commit-collapse-multiple-scoped-gates-into-one-verify-composes)
for the aggregate `composes` shape on top of it, and
[Gate dependencies](#gate-dependencies-composes-vs-requires) for the
strict variant (`requires`) that refuses `set` on a stale child.

## Use cases

Each section follows the same shape: **Scope** (what triggers
re-verify — a [`hash`](#hashing-strategies-git-tree-vs-files-vs-diff)
strategy) → **Commands** (what goes in your shell / hook). All
examples below use scoped `files`-hash gates defined in
[`.markgate.yml`](#markgateyml-reference) at the repo root, and the
[`set` + `verify` shape](#pattern-2-enforce-non-command-tasks-set--verify)
above. (For the broad whole-repo `git-tree` shape with no config,
see [Pattern 1](#pattern-1-ensure-non-duplicate-runs-markgate-run).)

### 1. Pre-PR: docs consistency

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
# Inside the /check-docs skill body (LLM-led — see Pattern 2):
markgate set pre-pr

# Before `gh pr create`:
markgate verify pre-pr || {
  echo "Docs are out of date. Run check-docs." >&2
  exit 1
}
```

### 2. Pre-image-push: vulnerability scan freshness

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

### 3. Pre-push: coverage report freshness

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

### 4. Pre-commit: isolate a slow check with its own scoped gate

**Scope**: two gates on the same `git commit` event. `check` covers code artifacts; `docs` covers code **and** documentation. Source files appear in both `include` lists on purpose — a src edit invalidates both gates (forcing both checks), while a tests-only edit invalidates only `check` and a docs-only edit invalidates only `docs`.

Useful when one pre-commit check is much slower than the others — typically an LLM-judged "are the docs still consistent with src?" review. Bundling it into the fast code check would force every tests-only or bug-fix commit to pay the doc-review cost. Splitting it into its own scoped gate means each edit only pays for the scope it actually invalidated.

```yaml
# .markgate.yml
gates:
  check:
    hash: files
    include:
      - "src/**"
      - "tests/**"
      - "package.json"
  docs:
    hash: files
    include:
      - "src/**"        # src edits invalidate docs too — see matrix below
      - "docs/**"
      - "README.md"
```

Invalidation matrix:

| edit                         | `check` | `docs` | re-runs needed          |
|------------------------------|---------|--------|-------------------------|
| `tests/**` only              | stale   | fresh  | fast code check only    |
| `docs/**` / `README.md` only | fresh   | stale  | slow docs check only    |
| `src/**`                     | stale   | stale  | both                    |
| outside both scopes          | fresh   | fresh  | neither — commit passes |

The last row is what makes the idiom scale: edits that land in neither `include` list (CI config, editor settings, hook scripts, tooling dotfiles) keep both markers fresh, so a hook verifying both stays silent when nothing relevant moved. That's only possible because each gate owns its own scope — `hash: files` + per-gate `include` is the primitive that makes it work.

**Commands**:

```sh
# Fast code check (src / tests / config):
pnpm typecheck && pnpm lint && pnpm build && markgate set check

# Slow docs consistency check (src / docs / README) — inside the /check-docs
# skill body (LLM-led — see Pattern 2):
markgate set docs

# One pre-commit hook verifies both; the failing gate names itself:
markgate verify check || { echo "run the code check" >&2; exit 1; }
markgate verify docs  || { echo "run the docs check" >&2; exit 1; }
```

A working wire-up lives in [go-to-k/cdkd](https://github.com/go-to-k/cdkd):

- [`.markgate.yml`](https://github.com/go-to-k/cdkd/blob/main/.markgate.yml) — gate definitions.
- [`.claude/hooks/check-gate.sh`](https://github.com/go-to-k/cdkd/blob/main/.claude/hooks/check-gate.sh) — pre-commit hook that runs `markgate verify` for each gate.
- [`/check`](https://github.com/go-to-k/cdkd/blob/main/.claude/skills/check/SKILL.md) and [`/check-docs`](https://github.com/go-to-k/cdkd/blob/main/.claude/skills/check-docs/SKILL.md) skills produce the markers (the latter has a diff-based short-circuit to keep the LLM cost low on internal src edits).

### 5. Pre-commit: collapse multiple scoped gates into one verify (`composes`)

**Scope**: a parent gate that ANDs the freshness of its children. No own `include:` — the parent has no scope of its own, so its verdict is purely "every child is fresh."

Builds on use case 4. There, the hook had to call `markgate verify` once per child to surface a per-gate error. When the hook only needs *one* verdict ("can this commit proceed?"), a parent that `composes` the children collapses that into a single call.

```yaml
# .markgate.yml — adds `pre-commit` on top of use case 4's gates
gates:
  check:
    hash: files
    include:
      - "src/**"
      - "tests/**"
      - "package.json"
  docs:
    hash: files
    include:
      - "src/**"
      - "docs/**"
      - "README.md"

  pre-commit:
    composes: [check, docs]
```

**Commands**:

```sh
# Each child is set as its own check finishes (same as use case 4):
pnpm typecheck && pnpm lint && pnpm build && markgate set check
# Inside the /check-docs skill body (LLM-led — see Pattern 2):
markgate set docs

# One verify covers both:
markgate verify pre-commit || {
  markgate status pre-commit >&2   # names the stale child in the note column
  exit 1
}
```

`markgate set pre-commit` is unconditional — the parent records its marker even if a child is stale. That's the right default for *summary* gates that observe child state.

**Strict variant (`requires`)** — same `verify` propagation, but `markgate set <parent>` is refused (exit 2) when any child is stale, and the error names the offending child. Reach for it when the parent gate represents a **declaration** that should be refused unless its dependencies are fresh — like `pr-ready` requiring `check` and `docs`, or `merge-ok` requiring all CI checks. See [Gate dependencies](#gate-dependencies-composes-vs-requires) for the full shape.

## How it works

When `markgate run -- <cmd>` is invoked:

1. It computes a **hash** of the current repo state.
2. If a saved marker matches, `<cmd>` is skipped (exit 0
   immediately).
3. Otherwise `<cmd>` runs. On success, the hash is saved as the new
   marker. On failure, the marker is left untouched.

(For the split shape, `markgate set` writes step 3's marker;
`markgate verify` does step 2's match check.)

```sh
# First run — nothing cached yet, so `pnpm build` runs and the pass is cached.
$ markgate run -- pnpm build
building...
passed in 7.2s

# Second run — nothing changed since the last success: instant skip.
$ markgate run -- pnpm build

# After you edit a file — cache is stale, `pnpm build` runs again.
$ echo '// fix typo' >> src/foo.ts
$ markgate run -- pnpm build
building...
passed in 7.1s
```

The marker is a small JSON file under `.git/markgate/`, one per
gate (the file name matches the gate name, e.g. `default.json`).
Not committed, not tracked, isolated per worktree. With
`--state-dir <dir>`, `MARKGATE_STATE_DIR=<dir>`, or `state_dir:`
in `.markgate.yml`, markers go to `<dir>/` instead — see [Sharing
markers](#sharing-markers-across-machines-ci--teammates). The
on-disk JSON layout is an implementation detail; don't parse it.

## Install

> **Note:** `markgate` is meant to run inside a git repository.

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

### mise

Pin a version per repo via [`.mise.toml`](https://mise.jdx.dev/configuration.html):

```toml
[tools]
"ubi:go-to-k/markgate" = "0.2.0"
```

Or one-shot:

```sh
mise use "ubi:go-to-k/markgate@0.2.0"
```

### `go install`

```sh
go install github.com/go-to-k/markgate/cmd/markgate@latest
```

### Prebuilt binaries

Linux / macOS / Windows archives (amd64 / arm64 / 386) — see
[GitHub Releases](https://github.com/go-to-k/markgate/releases).

## Drop into your hook manager

Substitute `pnpm build` with your verification command. Use
`markgate run --` when the hook itself runs the check, or
`markgate verify` when it sits in front of a separate `markgate set`
(see [Pattern 2](#pattern-2-enforce-non-command-tasks-set--verify)).

**husky** — `.husky/pre-commit`:

```sh
markgate run -- pnpm build
```

**lefthook** — `lefthook.yml`:

```yaml
pre-commit:
  commands:
    check:
      run: markgate run -- pnpm build
```

**pre-commit framework** — `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: local
    hooks:
      - id: markgate-check
        name: markgate check
        entry: markgate run -- pnpm build
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

In your `/check` skill: `pnpm build && markgate set`. See
[Pattern 2](#pattern-2-enforce-non-command-tasks-set--verify) for the
full flow.

## `.markgate.yml` reference

Lives at `$(git rev-parse --show-toplevel)/.markgate.yml` (no
parent-dir walking).

`markgate init` writes a starter file at the repo root:

```sh
markgate init          # writes .markgate.yml at the repo root
markgate init --force  # overwrite an existing one
```

The generated file enables the `default` gate with `git-tree` hash,
plus commented-out examples (an `exclude` list on `git-tree`, a
`files`-type gate, and a `diff`-type gate) — uncomment what you need.

Per-gate fields:

| field | purpose |
| --- | --- |
| `hash` | `git-tree` (default), `files`, or `diff` |
| `include` | glob list; required for `hash: files`, optional for `hash: diff` (omitted = the branch's whole delta) except on a gate with `composes`/`requires`, where omitting it would make the gate [deps-only](#gate-dependencies-composes-vs-requires) — that combination is rejected rather than silently ignoring `hash`/`base` |
| `exclude` | glob list |
| `base` | ref a `hash: diff` gate measures its delta from (e.g. `origin/main`); required there, rejected everywhere else — see [Hashing strategies](#hashing-strategies-git-tree-vs-files-vs-diff) |
| `state_dir` | optional override of marker storage location — see [Sharing markers](#sharing-markers-across-machines-ci--teammates) |
| `ttl` | optional wall-clock expiry for the marker — see [Wall-clock expiry (`ttl`)](#wall-clock-expiry-ttl) |
| `composes` | child gate keys whose freshness is ANDed into this one — see [Gate dependencies](#gate-dependencies-composes-vs-requires) |
| `requires` | like `composes`, but `set` of this gate is refused unless every required child is fresh — see [Gate dependencies](#gate-dependencies-composes-vs-requires) |

Example:

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

Each gate's key (the YAML map key — `default`, `pre-pr` above) must
match `[a-z0-9][a-z0-9-]*` (kebab-case ASCII). `default` is what
`markgate set` / `verify` use when no key argument is given:

```sh
markgate set               # same as `markgate set default`
markgate set pre-pr        # a second, independent gate
```

### Hashing strategies: `git-tree` vs `files` vs `diff`

The `hash` field above picks one of three strategies:

| aspect | `git-tree` (default) | `files` | `diff` |
| --- | --- | --- | --- |
| What it hashes | `HEAD` + diff-vs-HEAD ∪ untracked-not-ignored | whatever matches your `include` globs | the delta against `merge-base(base, HEAD)`, narrowed by `include` |
| `HEAD` in the hash? | **Yes** | **No** | **No** |
| Commits invalidate the marker? | Yes | Only if they touch in-scope files | Only if they change your delta |
| Changes pulled from the base branch invalidate? | Yes | Yes, if in scope | Only for files you also changed |
| `.gitignore` respected? | Yes (automatic) | No — scope is explicit | Yes (untracked-not-ignored) |
| Needs config? | No | Yes (`include` required) | Yes (`base` required) |
| Strictness | Strictest | Narrowed to your globs | **Least strict** — a base-branch change your branch does not touch is trusted unverified; see the limitation below |

When to use which:

- **`git-tree`** = "re-verify on *any* repo change". Broad gates
  (pre-commit running lint/test/build). Add `exclude` patterns to
  skip `vendor/`, `node_modules/`, etc. — HEAD-aware invalidation
  is kept.
- **`files`** = "re-verify *only* when these paths change, ignore
  other commits". Narrow gates (docs consistency, vuln scan rooted
  on a lockfile, coverage for one sub-tree).
- **`diff`** = "re-verify only when *my branch's* changes move".
  Expensive gates on long-lived branches, where most invalidations
  come from merging an updated base branch rather than from your own
  work. It is the **least strict** of the three and the only one that
  can call a state fresh that was never verified in that exact
  combination — adopt it deliberately, after reading
  [what it does not catch](#hash-diff--ignore-changes-that-arrive-from-the-base-branch).

Rule of thumb: start with `git-tree` (add `exclude` if needed).
Reach for `files` only when you specifically want the "ignore
commits that don't touch these paths" semantics, and for `diff` only
when base-branch churn is what keeps re-triggering an expensive gate.

#### A dead `include` is refused, not silently empty

Both scoped strategies digest a *set of paths*. When that set comes
out empty, the digest is the SHA-256 of nothing — a constant, the
same value for every such gate — so the marker matches forever and
the gate can never block. A typo (`scr/**`), a renamed directory, or
a path that moved lands you there without any other symptom.

markgate distinguishes the two ways a scope can be empty:

| situation | treatment |
| --- | --- |
| no `include` pattern can match anything the gate can see | **`set` errors (exit 2); `verify` is a mismatch (exit 1)** |
| patterns match, but this branch changed none of them | fine (`diff` warns on `set`, `files` is silent) |
| `exclude` removed everything `include` matched | fine — that is a deliberate configuration |
| at least one pattern is live, others are dead | fine at run time; `config lint` warns per pattern |

**Why `verify` is a mismatch and not an error.** The bug is that a
dead scope *passes*; exit 1 fixes that, and it means "re-run the
check", which is what both cases need. A gate on build output is
legitimately empty between `make clean` and the next build, so
erroring on the read path would stop `markgate run dist -- make dist`
from executing the very command that refills the scope — leaving the
gate recoverable only via `markgate clear`. `set` is where the
constant would actually be recorded, and by then the gated command
has had its chance, so a still-empty scope there is the
configuration's fault and is refused. A typo therefore re-runs its
command and then fails loudly at `set`, and no marker is ever
written.

**"Anything the gate can see" differs per strategy.** `files` hashes
whatever is on disk, so an include matching only gitignored paths is
a real scope. `diff` digests a git delta, which can never contain an
ignored path or anything under `.git/`, so the same include there is
permanently empty and is refused. Getting this backwards would be a
false all-clear rather than a false alarm.

The message names the patterns, because the gate's behavior gives
you no other way to tell which one is wrong. Bare `markgate status`
shows the offending row as a mismatch and keeps listing the rest,
and `--explain` still prints the (empty) scope rather than failing:
it is a diagnostic and never changes what a command does.

The trade-off is a scope that is legitimately absent for good and
not merely between builds — a sparse checkout, an optional subtree.
Its gate now reports mismatch forever and refuses to record, instead
of passing. Scope such a gate to a path that is always present, or
don't gate it.

#### `hash: diff` — ignore changes that arrive from the base branch

`git-tree` and `files` both digest **content**, so neither can tell
the work you did from the work that arrived when you pulled or
rebased onto an updated base branch. On a busy repo that becomes the
dominant source of invalidation, and it carries no information about
your branch: every change that reached the base branch passed the
same gates in its own PR.

`diff` digests the **delta against `merge-base(base, HEAD)`**
instead:

```yaml
gates:
  integ:
    hash: diff
    base: origin/main
    ttl: 14d
    include:
      - "src/providers/**"
```

| event | `files` | `diff` |
| --- | --- | --- |
| the base branch changes an **unrelated** in-scope file | stale | **fresh** |
| the base branch changes a file **this branch also changed** | stale | stale |
| you edit an in-scope file (committed or not) | stale | stale |
| you edit an out-of-scope file | fresh | fresh |

The correlation check is file-granular and needs no configuration: an
incoming change to a file your branch also touched is part of your own
delta, so it still invalidates.

**Requirements, all failing closed with exit 2:**

- `base:` is **required** and has no default. It must resolve locally,
  so fetch it first (`git fetch origin`). markgate deliberately does
  not guess `origin/HEAD`: a base ref that resolved differently on a
  developer machine and in CI would make the same gate mean two
  different things.
- The delta must be non-empty. A clean checkout of the base branch —
  or a branch with no changes yet, or one whose changes were all
  reverted — has nothing to hash, so the digest would be a constant
  the marker could never fall out of. markgate errors rather than
  report a freshness that can never expire. A fresh branch with
  **uncommitted** work is fine: that delta is not empty, which is what
  pre-commit gates need.
- Uncommitted edits and untracked files count, exactly as they do
  under the other two strategies.

**What it deliberately does not catch.** Cross-file interaction: if
caller `C` uses `A` and `B`, you change `A`, and the base branch
changes `B`, the two deltas never overlap and the marker stays fresh
even though that combination was never verified. `files` catches it
incidentally, so `diff` is a real reduction in strictness, traded
against not re-running on unrelated churn. Two things bound the
residual risk: pair `diff` with [`ttl`](#wall-clock-expiry-ttl) as a
time-based backstop, and note that the whole model assumes the base
branch is itself gated — if the same gate does not run on the base
branch's own PRs, `diff` is trusting changes nothing ever verified.

**Determinism.** The digest is built from git object identity (the
blob each path holds at the merge base) plus the current bytes on
disk — never from the text `git diff` prints, which moves with
`diff.algorithm`, `diff.context`, `diff.noprefix`, `color.diff` and
other per-user settings. Two machines holding the same content
therefore agree on the digest, which is what makes a `diff` gate safe
to share (see [Sharing markers](#sharing-markers-across-machines-ci--teammates)).
Binary files are compared as bytes. A mode-only change (`chmod`) on a
path that was not already in the delta adds it and invalidates; on a
path the branch had already modified it does not, because markgate
hashes content and never permission bits (`hash: files` and
`git-tree` ignore mode entirely).

`ttl`, `composes`, and `requires` are **optional** — the basic
gate pattern works without them. Skip the rest of this section
unless you hit one of the patterns above.

### Wall-clock expiry (`ttl`)

By default, a marker stays valid until something in the gate's scope
changes. Some checks verify against **state outside the repo** that
drifts on its own — a real-cloud destroy test that depends on AWS
behaviour, a vulnerability database that gains new CVEs, an SDK
that's revved upstream. For those, "nothing in the repo changed"
isn't enough; you also want the marker to expire after a fixed
amount of wall-clock time.

`ttl:` adds that expiry, **per gate**:

```yaml
gates:
  integ-destroy:
    hash: git-tree
    ttl: 7d
```

When `ttl` is set, `markgate verify` (and the verify pre-flight inside
`markgate run`) treats the marker as a mismatch (exit 1) once
`now - marker.created_at > ttl`, even if the digest still matches.
`markgate set` always writes a fresh marker, so the countdown
restarts on every successful run. Omitting `ttl` (the default)
preserves existing behaviour exactly — markers never expire on time
alone.

**Duration syntax** is `time.ParseDuration` extended with `d` and `w`:

| unit | meaning |
| --- | --- |
| `s` | seconds |
| `m` | **minutes** (Go-standard, **not** months) |
| `h` | hours |
| `d` | days (24h) |
| `w` | weeks (168h) |

Mixed units compose: `1h30m`, `1d12h`, `2w3d`. Months (`mo`) and
years (`y`) are intentionally **not supported** — month length is
ambiguous (28-31 days) and year length varies with leap years, so
neither rounds to a fixed duration. Use `d`/`w` for stable expiries.

### Gate dependencies: `composes` vs `requires`

A gate can declare child gates whose freshness is ANDed into its
own. Two shapes are available:

- **`composes`** (loose) — `verify` of the parent is mismatch when
  any child (recursively) is mismatch. `set` of the parent is
  unconditional: marking the parent doesn't care whether children
  are fresh.
- **`requires`** (strict) — same `verify` propagation, *and* `set`
  of the parent is refused (exit 2) unless every required child is
  fresh. The error names the offending child.

A gate may use one keyword but not both (config load error). Cycles
and references to undeclared gates are also load errors.

```yaml
gates:
  # composes: parent fails verify if any composed child is stale,
  # but `markgate set verify-pr` is always allowed.
  verify-pr:
    composes: [check, docs]

  # requires: same propagation plus `markgate set pr-ready` is
  # refused unless every required child is fresh. The pr-ready
  # gate declares its own `include:`, since its marker captures
  # the state being declared "ready".
  pr-ready:
    hash: files
    include: ["src/**", "docs/**"]
    requires: [check, docs]

  check:
    hash: files
    include: ["src/**", "tests/**"]
  docs:
    hash: files
    include: ["docs/**", "README.md"]
```

#### Parent's own scope

If the parent declares its own `include:`, the parent's digest is
computed and ANDed with children — both must match. If the parent
omits `include:` (and only has `composes`/`requires`), there is *no*
own scope: the parent's freshness is purely the AND of its
children. This is the right default — without it, a parent gate
without `include:` would inherit the `git-tree` default and become
almost always stale.

A `markgate set <parent>` on a deps-only gate still records a
marker, so `markgate clear <parent>` keeps working as the user
expects.

Because a deps-only gate never consults a hasher, `hash: diff` on
one is a config error: its `base:` would be silently discarded, and
a gate whose ref never has to resolve is exactly the kind of
misconfiguration that reads as working. Add `include:` if you want
the gate to have its own delta as well as children, or drop `hash:`
and `base:`. `hash: git-tree` stays legal there — it is the default
and carries no scope configuration to discard.

#### Which one should I use?

- Reach for **`composes`** when the parent is a *summary* gate that
  records "all the pieces I care about are currently fresh." Useful
  for `verify-pr` shaped gates that combine independent checks; you
  set each child gate as that check finishes, and the parent's
  verdict tracks them automatically.
- Reach for **`requires`** when the parent gate represents a
  **declaration** that should be refused unless its dependencies
  are demonstrably fresh. The `set` itself is the declaration
  moment (e.g., `markgate set pr-ready` after `check` / `docs`
  have passed) — refusing to `set` prevents the declaration from
  being recorded.
- If unsure, start with `composes`. It's the looser of the two and
  doesn't change `set` semantics; you can promote to `requires`
  once you know you want `set` to refuse.

Gates with `composes:` are typically deps-only (no `include:`) —
they exist purely to aggregate the verdicts of their dependencies.
Gates with `requires:` typically declare their own `include:`
since their marker captures the state being declared (so the
declaration can later be verified against the current state).

## CLI reference

```text
markgate set        [key]              Record the current state hash.
markgate verify     [key]              Exit 0 match, 1 mismatch (incl. ttl
                                       expiry), 2 error.
markgate status     [key]              Show marker + match status (bare:
                                       list every known gate).
markgate clear      [key]              Delete the marker (idempotent).
                                       The only command that does not
                                       require .markgate.yml to be valid
                                       — it needs the marker's location,
                                       not the gate's semantics — so a
                                       config error elsewhere cannot
                                       leave markers unremovable. The
                                       error is still reported.
markgate run        [key] -- <cmd>...  Sugar for verify + <cmd> + set.
markgate init                          Write a starter .markgate.yml.
markgate config lint                   Warn per pattern on dead
                                       include/exclude globs (an include
                                       list where *every* pattern is dead
                                       is warned about here and also
                                       refused by `set`),
                                       unknown fields, and every rule that
                                       would make `markgate run` exit 2
                                       (unknown hash, ttl parse, undeclared
                                       composes/requires refs, cycles).
                                       Exit 0 clean, 1 warnings, 2 error.
                                       --json emits an array of
                                       {path, severity, message}.
markgate version                       Print the version.
markgate completion <shell>            Emit a completion script (bash / zsh / fish / powershell).
```

`markgate run` passes stdio through and forwards `SIGINT` / `SIGTERM`
to `<cmd>`. On `<cmd>` failure, the marker is **not** updated and
`<cmd>`'s exit code is returned as-is.

`verify`, `status`, and `run` accept `--explain` / `-e` to print the
files currently in scope to stderr (with `--json` for a structured
form on stdout). See [Debugging a stale gate](#debugging-a-stale-gate).

> When a gate sets [`ttl:`](#wall-clock-expiry-ttl), `verify` is no
> longer a pure function of the file tree — it also depends on the
> wall clock, returning mismatch once `now - marker.created_at >
> ttl` even if the digest still matches.
>
> `markgate run --explain --json` is only stdout-clean on the skip
> path (when the gate matches). On mismatch the child runs with
> `Stdout = os.Stdout`, so its output concatenates after the JSON
> object and `jq` will choke. Use plain `--explain` (text form,
> stderr) when you want explain output alongside a real run, or
> compose with `markgate verify <key> --explain --json` ahead of
> the child.

### Exit codes

Exit codes follow the `grep` / `diff` convention, so `||` composes
naturally:

| exit | meaning                                                   |
| ---- | --------------------------------------------------------- |
| 0    | verified — state matches the marker, safe to skip         |
| 1    | not verified — no marker, state differs, or TTL expired   |
| 2    | error — not in a repo, bad config, bad key, etc.          |

### `markgate status` (bare): list all gates

Without a `[key]`, `markgate status` prints one row per known gate —
the union of `gates:` keys in `.markgate.yml` and marker files in the
state directory:

```text
$ markgate status
KEY            STATE        AGE        NOTE
check          match        3m ago     -
docs           mismatch     1h ago     digest differs
integ-destroy  match        2d ago     -
verify-pr      no marker    -          (configured)
extra-gate     match        5m ago     (unconfigured)
```

Notes:

- `(configured)` — gate is in `.markgate.yml` but no marker exists
  yet (run the check or `markgate set <key>`).
- `(unconfigured)` — a marker file is present but the gate isn't in
  `.markgate.yml` (stale from a renamed / deleted gate, or written
  by a script that bypassed the config).
- `child <key> is stale` — this gate `composes` / `requires` the
  named child, and the child's own row is mismatch. The bare list
  recurses through dependencies, so the parent's verdict here always
  agrees with `markgate verify <parent>`.

Exit code: `0` if every row matches, `1` if any row is mismatched or
missing a marker, `2` on internal error.

`--json` emits a machine-readable array (one object per row) using
the same `state` / `note` vocabulary as the table; `markgate status
<key> --json` emits a single object with the same shape.

> **Behavior change in v0.x:** `markgate status` (no key) used to
> operate on the `default` key. It now lists every gate. Use
> `markgate status default` to keep the old single-key behavior.
> `status` deviates from `set` / `verify` / `clear`'s "no-arg =
> default key" rule on purpose: it's an introspection command (think
> `git status`), so the bare form is the overview, not a shortcut to
> one specific gate.

### Per-invocation overrides

`set` / `verify` / `status` / `clear` / `run` each accept these flags,
so one-off scopes don't need a `.markgate.yml`:

```text
--hash git-tree|files|diff
                         Override hash type for this call.
--include <glob>         Repeatable. Override the gate's include list.
--exclude <glob>         Repeatable. Override the gate's exclude list.
--base <ref>             Base ref for --hash diff (e.g. origin/main).
                         Required there, rejected on other hash types.
--state-dir <path>       Directory to store marker files. Takes
                         precedence over MARKGATE_STATE_DIR env and
                         state_dir: in .markgate.yml. Default:
                         <git-dir>/markgate. See "Sharing markers".
```

`verify` / `status` / `run` additionally accept a debug flag:

```text
--explain, -e            Print the in-scope file list to stderr ahead
                         of normal output. Does NOT change exit codes.
                         See "Debugging a stale gate" below.
--json                   With --explain: emit a single JSON object on
                         stdout instead of the text scope listing.
                         (--json without --explain is an error.)
```

Flag syntax is identical across hash types. With `--hash files`,
`--include` is required; with `--hash diff`, `--base` is — and
`--include` too, if the gate carries `composes`/`requires`, since
without it the gate would be
[deps-only](#gate-dependencies-composes-vs-requires) and never consult
a hasher. Example — exclude `vendor/` without any config file:

```sh
markgate run --exclude 'vendor/**' -- pnpm build
```

#### Debugging a stale gate

`--explain` lists the files **currently in scope** for the active
hasher (`git-tree`, `files`, or `diff` — for `diff` that is the delta
against the merge base, not the whole include set) after `--include` /
`--exclude` filtering. It is **not** a diff against the marker — markgate stores
only a single SHA-256, so "files that changed since `set`" cannot be
reconstructed post-hoc. What you see is the candidate set the hasher
would fold into the digest right now; if the wrong files appear (or
expected ones are missing), your globs are misconfigured.

```sh
$ markgate verify check -e
scope:
  go.mod
  internal/cli/helper.go
  internal/cli/status.go
  internal/state/state.go
state: mismatch
```

The state line uses one of `match`, `mismatch`, `no marker` — the
same vocabulary as the JSON form below. The exit code is unchanged
(0 / 1 / 2), so `--explain` is safe to leave on inside a hook while
debugging.

`--explain --json` emits a single object on stdout instead, suitable
for piping into `jq`:

```json
{
  "key": "check",
  "scope": ["go.mod", "internal/cli/helper.go"],
  "hasher": "git-tree",
  "state": "mismatch"
}
```

### Environment variables

```text
MARKGATE_STATE_DIR       Marker storage directory. Same effect as
                         --state-dir and state_dir: in config.
                         Precedence: --state-dir > this env >
                         state_dir: in .markgate.yml > default.
```

### Shell completion

`markgate completion <shell>` prints a completion script for `bash`,
`zsh`, `fish`, or `powershell`. Pipe it into the location your shell
loads.

```sh
# Bash (current session)
source <(markgate completion bash)
# Bash (persistent)
markgate completion bash > /etc/bash_completion.d/markgate

# Zsh — write into a directory on $fpath, e.g.
markgate completion zsh > "${fpath[1]}/_markgate"

# Fish
markgate completion fish > ~/.config/fish/completions/markgate.fish

# PowerShell
markgate completion powershell | Out-String | Invoke-Expression
```

Once installed, the gate-key positions on `set` / `verify` / `status` /
`clear` / `run` complete from the `gates:` map in `.markgate.yml` at
the repo top-level. With no `.markgate.yml` present, completion stays
silent — it never scans the marker directory or runs the gate.

## Sharing markers across machines (CI / teammates)

By default, markers live under `.git/markgate/` — strictly local. If
that's all you need, skip this section; the [use cases above](#use-cases)
all work with the default.

Read on if you want a check to **skip in CI (or on a teammate's
machine) based on a run that already happened elsewhere**. Typical
wins: coverage, vulnerability scan, e2e, image build — expensive
and deterministic, redundant to re-run. Trust model differs by
pattern (see [Two patterns at a glance](#two-patterns-at-a-glance)
below); pick the one that matches your trust assumptions.

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

[Bare `markgate status`](#markgate-status-bare-list-all-gates) honors
the same precedence: it walks `<dir>/` (with the override applied)
and lists every `<key>.json` it finds, alongside the `gates:` keys
in `.markgate.yml`.

### Two patterns at a glance

Both use `--state-dir` / `state_dir`; the difference is whether the
marker is **committed** to the repo.

| aspect | **A. Not committed** (CI cache / artifact) | **B. Committed** |
| --- | --- | --- |
| Marker in the repo? | No (typically gitignored, or outside the repo) | Yes, tracked in git |
| Works with hash type | any | **`files`, or `diff` with the state dir out of scope** — committing with `git-tree` breaks: the commit changes HEAD → digest is instantly stale |
| Local → CI sharing | Needs CI cache / artifact / shared volume | Just `git push` |
| Tamper surface | Whoever can write to the cache | Whoever has commit access |
| Extra infra | CI cache provider (e.g. `actions/cache`, `actions/upload-artifact`) | None — git is enough |
| Best for | CI-internal reuse across runs; teams already on remote cache infra | Zero-infra local→CI sharing for scoped gates (coverage, scans) |

### A. Not committed (CI cache / artifact)

Store the marker somewhere CI can pick it up, but keep it out of git.
`.markgate-cache/` at the repo root is a conventional choice; any
path outside `.git/` works. (If you'd rather commit the marker into
git so CI sees it without any cache layer, skip to
[Pattern B](#b-committed-scoped-hash) — that's a different shape, not
a variant of this one.)

#### Step 1. Add the state dir to `.gitignore`

**This is a required setup step on `hash: git-tree` — and on any
`hash: diff` gate whose `include`/`exclude` does not keep the state dir
out of the delta — not optional hygiene.** Do this *before* your first
`markgate run`:

```gitignore
# .gitignore — add the state dir you chose
/.markgate-cache/
```

You can skip this only if:

- the state dir is **outside the repo** (e.g. `$RUNNER_TEMP/mg`,
  `/tmp/mg`, `$HOME/.cache/markgate`), **or**
- you're on `hash: files`, or on a `hash: diff` gate whose
  `include`/`exclude` keeps the state dir out of the delta (gitignore
  then becomes hygiene, not required — see why below).

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

`hash: diff` sidesteps it the same way **when the gate is scoped** —
the marker is an untracked file, so it lands in the branch's delta
unless `include`/`exclude` filters it out. An unscoped diff gate (no
`include`) behaves exactly like `git-tree` here, so gitignore the
state dir or scope the gate.

</details>

#### Step 2. Wire up CI

**Across runs of the same workflow** — `actions/cache`, extending
the `pre-image-push` gate from [Use case 2](#2-pre-image-push-vulnerability-scan-freshness):

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

### B. Committed (scoped hash)

Keep the state directory **tracked in git** and commit the marker with
the code. Requires a hash type that ignores the commit itself:

- **`hash: files`** — always safe: the marker is only in the digest if
  an `include` glob matches it.
- **`hash: diff`** — safe as long as `include`/`exclude` keeps the
  state dir out of the branch's delta. An *unscoped* diff gate (no
  `include`) folds the marker into its own delta and self-invalidates
  the moment it is written, exactly as `git-tree` does.
- **`hash: git-tree`** — breaks: the commit changes HEAD and
  invalidates the marker it just wrote.

Typical fit: coverage reports, image vulnerability scans — expensive,
deterministic, and already re-running them on every push is waste
when nothing in scope changed.

Coverage example, extending the pre-push gate from [Use case 3](#3-pre-push-coverage-report-freshness):

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

### Caveats

- **Worktree isolation is lost** when the dir is shared across
  worktrees pointing at the same location. The default `.git/`-based
  layout preserves isolation; `--state-dir` does not.
- **Relative paths** resolve from the repo top-level, not cwd, so
  hook-invoked commands land in the same place regardless of where
  they run from.
- **Signing is not yet implemented** — markers are unsigned JSON.
  Tamper resistance depends on who can write to the directory (cache /
  repo).

## FAQ

- **Why not just `git status` in the hook?** `git status` tells you
  the tree is clean, not "did the check pass against this exact
  state." `markgate` records the success itself, so a passed check
  stays valid across hook invocations until something moves.
- **Does it work in git worktrees?** Yes. Markers live under each
  worktree's own `.git/` dir, so they don't leak across worktrees.
  (This isolation is lost if you point `--state-dir` at a shared
  location.)
- **Do I need to gitignore anything?** No for the default layout —
  markers are under `.git/`. If you use `--state-dir` pointing inside
  the repo, gitignore that directory.
- **What if I don't want HEAD in the hash?** Use
  [`hash: files`](#hashing-strategies-git-tree-vs-files-vs-diff) for that
  gate.
- **My gate keeps re-running because teammates keep merging into the
  base branch.** That is what
  [`hash: diff`](#hash-diff--ignore-changes-that-arrive-from-the-base-branch)
  is for: it hashes your branch's delta, so an incoming change only
  counts when it touches a file you also changed.
- **Why does my `diff` gate error on the base branch?** Because there
  is nothing between the base and your working tree, so the digest
  would be a constant that never goes stale. Run the gate from a
  branch that is ahead of the base — or make (not necessarily commit)
  a change first.
- **Does `files` respect `.gitignore`?** No. `files` is explicit
  scope by design. Use `git-tree` when you want `.gitignore`-aware
  behavior. (See [Hashing strategies](#hashing-strategies-git-tree-vs-files-vs-diff).)
- **Can markers be shared across machines / CI?** Yes, via
  `--state-dir`, `MARKGATE_STATE_DIR`, or `state_dir:` in
  `.markgate.yml`. See
  [Sharing markers](#sharing-markers-across-machines-ci--teammates) for patterns
  and trust considerations.
- **My `.markgate.yml` is broken and now nothing works.** `clear`
  still does. Every other command resolves a hasher and computes a
  digest, so a config error stops them by design — but `clear` only
  needs to know where the marker lives, and refusing there would put
  the recovery path inside the trap: markers for *valid* gates would
  be unremovable too. `clear` reports the config error on stderr and
  removes the marker anyway. If the YAML cannot even be parsed, its
  `state_dir:` is unknowable, so `clear` says which location it used
  and falls back to `--state-dir` / `MARKGATE_STATE_DIR` / the
  default.

- **Can the marker be tampered with?** Yes — it's a JSON file under
  `.git/` (or wherever `--state-dir` points). Trust whoever can write
  to that location. Signed markers are still a future consideration.
- **My check verifies external state (cloud APIs, vuln DB, …) — how
  do I force re-runs even when the repo is unchanged?** Add
  [`ttl:`](#wall-clock-expiry-ttl) to the gate. The marker is treated
  as a mismatch once it's older than the TTL, even if the digest still
  matches.
- **Why isn't `1mo` (months) a valid TTL?** Month length is ambiguous
  (28-31 days) and would make `now - created_at > 1mo` non-deterministic.
  Use `30d` or `4w` to be explicit. Same reasoning rules out `1y`.
  (See [Wall-clock expiry](#wall-clock-expiry-ttl).)
- **My gate keeps re-running. How do I debug it?** Run `markgate
  verify <key> --explain` (or `-e`). It lists the files currently in
  scope on stderr, so you can see whether your `include` /
  `exclude` globs match what you expect. Note: this is the
  *current* scope, not a diff against the marker — markgate stores a
  single hash, so "which files changed since `set`" can't be
  reconstructed. See
  [Debugging a stale gate](#debugging-a-stale-gate).
- **When should I use `composes` vs `requires`?** Use `composes`
  when the parent is a *summary* gate ("all the pieces I care about
  are currently fresh") — `set` of the parent is allowed regardless
  of child state, but `verify` propagates. Use `requires` when the
  parent gate represents a **declaration** that should be refused
  unless every dependency is fresh (e.g., `pr-ready` requiring
  `check` and `docs`): `set` is the declaration itself and is
  refused with exit 2 if a required child is stale. See
  [Gate dependencies](#gate-dependencies-composes-vs-requires).

## License

MIT. See [LICENSE](LICENSE).
