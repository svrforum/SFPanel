# Maintainer Knowledge Base

> **For Claude / future maintainers.** This is the "why / gotcha / decision / open-issue" layer — the tribal knowledge that isn't a feature walkthrough. For *what a feature does*, read `docs/specs/` and `docs/superpowers/specs/`. For *how the code is*, read the code and `git log`.
>
> **Trust order when things disagree:** code & `git log` > this wiki > longform specs. These pages are maintained by hand and can lag; if a page contradicts the code, the code wins — fix the page.

## How to use this

- Each page is short and scannable. References point at a **file + symbol** (e.g. `internal/api/response/sanitize.go:SanitizeOutput`) rather than line numbers, which drift.
- When you learn something non-obvious while working (a trap, a "why", a deferred bug), add it here — that's the whole point. Keep entries terse.
- This is committed to the repo (unlike the assistant's private session memory and `CLAUDE.md`, which are out of band), so any contributor or future AI session sees it.

## Pages

- [gotchas.md](gotchas.md) — non-obvious traps that have bitten us; read before touching the relevant area.
- [decisions.md](decisions.md) — design decisions and the reasoning behind them (so they don't get "fixed" by accident).
- [known-issues.md](known-issues.md) — unresolved / deferred problems, with how to reproduce and current state.

_Last reviewed: 2026-06-04._
