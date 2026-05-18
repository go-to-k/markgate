# Changelog

## [v0.3.4](https://github.com/go-to-k/markgate/compare/v0.3.3...v0.3.4) - 2026-05-18
- docs: add use case for composes / requires gate dependencies by @go-to-k in https://github.com/go-to-k/markgate/pull/49
- docs: reframe Gate pattern around aggregate verify by @go-to-k in https://github.com/go-to-k/markgate/pull/51
- docs: define marker inline at first use in Gate pattern by @go-to-k in https://github.com/go-to-k/markgate/pull/52
- docs: mirror blog clarity pass on Gate pattern by @go-to-k in https://github.com/go-to-k/markgate/pull/53
- docs: restructure README around three usage patterns by @go-to-k in https://github.com/go-to-k/markgate/pull/54
- docs: thread "forgetting" framing through README front half by @go-to-k in https://github.com/go-to-k/markgate/pull/55
- docs: rewrite Pattern 2 re-edit narrative for user-desire + plain language by @go-to-k in https://github.com/go-to-k/markgate/pull/56
- docs: show requires parents with own include, name the composes/requires convention by @go-to-k in https://github.com/go-to-k/markgate/pull/57
- test: cover composes/requires with own include by @go-to-k in https://github.com/go-to-k/markgate/pull/58
- docs: add overview table to 'What markgate does' by @go-to-k in https://github.com/go-to-k/markgate/pull/59
- docs: align overview table rows with Pattern subsection headings by @go-to-k in https://github.com/go-to-k/markgate/pull/60
- docs: separate 'challenges' from 'markgate's patterns' in intro by @go-to-k in https://github.com/go-to-k/markgate/pull/61
- docs: fix Pattern 2 overstatement that 'a hook can't execute' LLM skills by @go-to-k in https://github.com/go-to-k/markgate/pull/62
- docs: inline the claude -p caveat instead of carrying it in parens by @go-to-k in https://github.com/go-to-k/markgate/pull/63

## [v0.3.3](https://github.com/go-to-k/markgate/compare/v0.3.2...v0.3.3) - 2026-05-09
- docs: relocate Shell completion from Install to CLI reference by @go-to-k in https://github.com/go-to-k/markgate/pull/46
- fix(cli): config lint mirrors runtime validation by @go-to-k in https://github.com/go-to-k/markgate/pull/48

## [v0.3.2](https://github.com/go-to-k/markgate/compare/v0.3.1...v0.3.2) - 2026-05-09
- fix: address three bug reports from cdkd against v0.3.1 by @go-to-k in https://github.com/go-to-k/markgate/pull/44

## [v0.3.1](https://github.com/go-to-k/markgate/compare/v0.3.0...v0.3.1) - 2026-05-09
- chore: add e2e CLI smoke + pre-merge hook + verify-e2e skill by @go-to-k in https://github.com/go-to-k/markgate/pull/36
- chore: post-batch follow-ups (drop LoadStrict, surface glob errors, README notes) by @go-to-k in https://github.com/go-to-k/markgate/pull/38
- refactor(cli): dedupe resolveStateDir / resolveMarkerPath by @go-to-k in https://github.com/go-to-k/markgate/pull/39
- refactor(cli): single state.Load + hasher.Hash per status invocation by @go-to-k in https://github.com/go-to-k/markgate/pull/40
- refactor(state): replace hash_type 'deps-only' sentinel with Marker.Kind by @go-to-k in https://github.com/go-to-k/markgate/pull/41
- fix(cli): TTL propagation through composes/requires chain by @go-to-k in https://github.com/go-to-k/markgate/pull/42
- docs: split README into core gates vs advanced configuration by @go-to-k in https://github.com/go-to-k/markgate/pull/43

## [v0.3.0](https://github.com/go-to-k/markgate/compare/v0.2.0...v0.3.0) - 2026-05-09
- docs: restructure README for clarity and flow by @go-to-k in https://github.com/go-to-k/markgate/pull/10
- chore: block git commit/push on main via PreToolUse hook by @go-to-k in https://github.com/go-to-k/markgate/pull/13
- docs: clarify run shape covers all invocation sites by @go-to-k in https://github.com/go-to-k/markgate/pull/12
- docs: cover 'edit after set' case in Two shapes bullets by @go-to-k in https://github.com/go-to-k/markgate/pull/14
- docs: add multi-gate pre-commit use case with overlapping scope by @go-to-k in https://github.com/go-to-k/markgate/pull/15
- docs: fix pre-commit framework config filename by @go-to-k in https://github.com/go-to-k/markgate/pull/16
- docs: add mise install method by @go-to-k in https://github.com/go-to-k/markgate/pull/17
- docs: restructure README to follow blog narrative flow by @go-to-k in https://github.com/go-to-k/markgate/pull/18
- docs: refresh README images by @go-to-k in https://github.com/go-to-k/markgate/pull/19
- docs: drop redundant 'run' from pnpm examples by @go-to-k in https://github.com/go-to-k/markgate/pull/20
- docs: add use case for AI-judgment checks (non-scriptable reviews) by @go-to-k in https://github.com/go-to-k/markgate/pull/21
- feat: shell completion for bash / zsh / fish / powershell by @go-to-k in https://github.com/go-to-k/markgate/pull/30
- feat: markgate config lint by @go-to-k in https://github.com/go-to-k/markgate/pull/32
- feat: TTL on markers by @go-to-k in https://github.com/go-to-k/markgate/pull/34
- feat: --explain on verify, status, run by @go-to-k in https://github.com/go-to-k/markgate/pull/31
- feat: bare 'status' lists all gates with freshness by @go-to-k in https://github.com/go-to-k/markgate/pull/33
- feat: gate dependencies (composes / requires) by @go-to-k in https://github.com/go-to-k/markgate/pull/35

## [v0.2.0](https://github.com/go-to-k/markgate/compare/v0.1.0...v0.2.0) - 2026-04-23
- docs: restructure README for clearer flow by @go-to-k in https://github.com/go-to-k/markgate/pull/4
- docs: Update README.md by @go-to-k in https://github.com/go-to-k/markgate/pull/6
- chore: add Claude Code harness (CLAUDE.md, skill, hooks, permissions) by @go-to-k in https://github.com/go-to-k/markgate/pull/8
- fix: add top-level description to release composite action by @go-to-k in https://github.com/go-to-k/markgate/pull/9
- feat: allow overriding marker storage directory for CI / commit sharing by @go-to-k in https://github.com/go-to-k/markgate/pull/7

## [v0.1.0](https://github.com/go-to-k/markgate/commits/v0.1.0) - 2026-04-22
- feat: initial markgate implementation by @go-to-k in https://github.com/go-to-k/markgate/pull/2
