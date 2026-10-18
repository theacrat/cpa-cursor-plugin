# provider reliability fixes

Fix the six reproduced review findings without changing upstream protocol behaviour or executing a live login.

## decisions

Cursor model discovery requires the host's existing `force-model-prefix` policy, preventing bare-id registration rather than introducing a second namespace system.

Dashboard api-key credentials receive the host-supported refresh interval and current expiry metadata, while browser-only credentials keep their explicit reauthentication behaviour.

Model cache access uses short critical sections and cancellable per-credential in-flight lookups, with network requests outside the cache mutex.

Translated reasoning preserves explicit suffix and payload choices, recovering native thinking only when translation has replaced enabled thinking with an implicit auto selection.

## slices

| slice | acceptance | verification |
| --- | --- | --- |
| provider isolation | unsafe host policy returns an empty catalogue before network activity, safe policy preserves prefixed routing | focused discovery tests and native abi smoke |
| refresh policy | api-key auth remains refreshable after its deadline and refreshed metadata cannot restore stale tokens | auth fixtures and real host scheduler regression |
| cache concurrency | unrelated accounts stay independent, same-account misses coalesce, waiters cancel promptly | deterministic cache tests and race checks |
| discovery timeout | control-plane deadline applies without reducing inference execution time | discovery and inference deadline tests |
| tool identity | raw names and all advertised aliases identify only the intended tool | collision fixtures and executor tool tests |
| thinking intent | enabled native thinking survives translation while explicit defaults remain defaults | reasoning precedence fixtures |
| toolchain | patched go version builds and passes the vulnerability scan | complete repository gate and govulncheck |

## final gate

Review the complete diff, run `mise run check` and govulncheck without concurrent source edits, and retain reproducible evidence for pending review.

No remote publish, deployment or live credential use is required.
