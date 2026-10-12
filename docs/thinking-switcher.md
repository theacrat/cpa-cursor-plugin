# Thinking switcher instead of model switcher

## Problem

Cursor publishes one model-list row per reasoning-effort variant
(`claude-opus-4-8-low`, `-medium`, `-high`, `-max`) sharing a single display
name. The picker shows four identical entries and the user must switch models
to change effort.

## Design

Publish one row per base model and resolve the effort variant per request,
using CPA's native `model(level)` suffix convention. CPA routes `base(level)`
to the registered `base` row and hands the full string to the executor
unstripped, so `claude-opus-4-8(high)` works end to end with no host changes.

- Discovery groups variant rows by family base ID, because families share
  display names only by accident and CPA's v8 model list drops provider
  display names entirely (a same-owner catalog backfill would otherwise
  re-duplicate every pair). Trailing `-thinking|-minimal|-low|-medium|-high|`
  `-xhigh|-max|-fast|-none` segments fold into the family; the first
  effort token from the right sets the kind, anything else is kind `default`.
  One row per family carries `ThinkingSupport` with the servable canonical
  levels, so management clients can render a thinking switcher. Speed-tier
  (`-fast`) and `none` variants fold into the family table rather than the
  list; the standard tier wins each kind.
- The executor resolves the variant per request and sends that variant ID
  upstream as `RequestedModel.ModelId`. Effort precedence: `(suffix)` in the
  requested model, then `reasoning_effort` in the chat payload, then the
  native field of the original request by source format (OpenAI
  `reasoning_effort`, Anthropic `effort`/`thinking`, Gemini `thinkingLevel`,
  Codex `reasoning.effort`), then the base default.
- Level mapping: exact suffix wins, then the `thinking` variant, then the
  default. `xhigh` prefers a real `xhigh` variant, then `max`. `none`,
  `auto` and numeric budgets select the default. Unknown values never fail
  the request; they select the default. The default is the bare variant
  when present, otherwise medium, high, low, max, minimal, xhigh, thinking,
  in that order.
- The variant table is cached per credential (5 minute TTL) and shared
  between discovery and execution. A cold executor fetches it over host HTTP;
  if that fetch fails the request fails instead of guessing a variant ID.
- Legacy suffixed IDs (`claude-opus-4-8-high`) are no longer registered, so
  saved configs must switch to the base ID with an optional `(level)` suffix.
  This is an intentional breaking change for a usable list.

## Acceptance

- One row per base model; no duplicated cursor display names.
- `(level)`, `(none)`, `(auto)`, numeric suffixes, payload `reasoning_effort`
  and native per-format fields all route to the documented variant, with
  fallbacks covered by fixtures.
- Full gate (fmt, vet, CGO-off/race tests, native ABI smoke) and repeated
  two-axis review until clean. No live-account claim without a live test.
