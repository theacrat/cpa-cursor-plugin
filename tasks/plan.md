# live client-tool loop implementation plan

## objective and approval

The operator approved the live-loop specification and the narrowly scoped CPA
host extension. Earlier reliability, metadata, label and public-ID work is
complete. The operator approved this implementation sequence and all required
compatibility changes. Deployment and live inference remain separate decisions.

Match the reference tool-loop behaviour without bypassing host transport policy:
park the actual Cursor run, return a 250 ms call burst to the client, and feed its
results back into the same pending upstream executions.

## pinned boundaries

- Behaviour reference: opencode-cursor 2.5.1 at
  `ef3721207a5941454ab281aab2b14f180e7657e6`.
- CPA host baseline: v8.0.29 at
  `5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3`.
- OpenCode identity source: v2.0.32 at
  `843a6c514e606b436a7fe713422ace0a93ad3d61`.
- Native C ABI stays additive. Existing request-scoped callback behaviour remains
  unchanged unless a new host capability is explicitly negotiated.
- Host changes are developed against the pinned source in a separate branch,
  never by editing a dependency cache or the installed server.

## dependency order

`host lease ownership/delivery/logging -> plugin lease adapter -> parked single-tool round trip
-> call batching -> recovery/watchdogs -> integrated verification`

Identity provenance and result-correlation helpers can proceed alongside the
host lease after their shared contract is frozen. Each writing agent owns one
separate worktree and may not delegate further. Read-only reviewers do not write
to active implementation checkouts.

## architecture decisions

### host-owned retained HTTP lease

Introduce an additive capability for an HTTP scope that is created before the
upstream request opens. The host retains selected credential/provider, effective
proxy policy, immutable logging metadata and plugin-instance ownership. It uses
an independent bounded lifetime with an explicit cancellation attachment to the
currently active client request.

The proposed additive wire contract has open, prepare-park, attach and close
callbacks. Parking commit is host-internal after successful response write/flush,
not a plugin acknowledgement or asynchronous completion event. The host capability
is unavailable until all participating delivery and logging seams are implemented.
The first participating client protocol is chat completions, in streaming and
non-streaming modes. Other protocol paths remain stateless until their delivery
handoff is independently implemented and verified. This keeps the initial host
patch bounded without silently changing unrelated providers.

Parking commits atomically at a successful tool-call handoff. Cancellation before
handoff closes upstream. Normal downstream cleanup after a committed park does
not. Attaching a subsequent request requires the same caller/account/session and
compatible route, with one active consumer. Expiry, revocation, disable, unload,
quiesce and shutdown close the scope and all operations. No retained Gin context,
callback-ID omission, private HTTP client or background-policy bypass is allowed.

### identity

Use the actual `x-opencode-session-id`, not shared affinity or parent headers.
Require one bounded valid session value, the host's authenticated `caller_scope`,
selected `AuthID` and provider. Child/fork affinity may be shared and is therefore
not a live-run owner. Store digest keys rather than raw identifiers.

The HTTP hook knows request purpose, but current outgoing headers do not carry
it. Exact primary/title/compaction/generate isolation needs explicit propagation
or conservative stateless handling. Never infer authoritative purpose solely from
prompt text. Missing, conflicting or unproven identity uses existing stateless
execution and cannot consume a parked agent run.

Recommended purpose propagation is a small provider-scoped HTTP hook that copies
OpenCode's existing `kind` into `x-cpa-request-kind`. Only `primary` may park.
This is approved for the provider integration repository in addition to the
host and Cursor plugin. Without the header, that client stays stateless.

### plugin state and replay

Separate retained conversation transport from per-response execution leases.
Keep bounded pending execution mappings, public IDs, the verified completed
transcript prefix, checkpoint/blob state and cumulative usage provenance. Only
new matching results can advance the run. Reject duplicates/conflicts before
writing any result batch. Old-host and missing-identity requests remain valid
through the existing full-transcript path.

### behavioural adaptations

Preserve actual tool errors and complete skill text. Reject excess capacity
instead of evicting a live pending batch. Retain native-tool rejections, global
policy delivery and payload-free diagnostics. Do not copy first-prompt session
keys, unsafe broad retries, guessed orphan association, error-as-content responses,
indefinite parked retention or silent output truncation.

Parked expiry and progress watchdogs are explicit feature-scoped exceptions to
the host's network-timeout convention. No generic post-connect HTTP timeout is
introduced for other host users.

## ordered slices

| task | scope | acceptance | verification |
| --- | --- | --- | --- |
| L01 | host capability/types | additive scope contract negotiated without changing old callbacks | compatibility fixtures, host build |
| L02 | host ownership capture | immutable caller/account/proxy/session scope retained without request objects | adversarial ownership fixtures, host build |
| L03 | independent upstream log capture | retained operations keep host policy and capture after client response completion | log ownership/privacy fixtures, host build |
| L04 | retained lifetime/HTTP operations | atomic open/prepare/attach/close, expiry/unload bounded | deterministic state/race and fake upstream fixtures, host build |
| L05 | delivery-bound handoff | host commits parking only after verified write/flush, active cancellation still wins | participating-protocol delivery races, full host build |
| checkpoint A | host boundary review | cancellation-versus-park race and foreign attach rejected | independent review, full host tests/build |
| L06 | plugin capability adapter | optional retained client negotiated through native callbacks, unsupported host safe fallback | C ABI fake-host fixtures, native build |
| L07 | identity/result ownership and approved purpose propagation | actual-session digest scope, strict latest-batch correlation, no duplicate delivery | account/child/fork/purpose and replay fixtures |
| L08 | single-tool live round trip | one call returned, actual upstream survives, next request sends its result to exact exec | real local TLS two-request fixture and host adapter contract |
| checkpoint B | retained execution review | same-run proof and cancellation/reload cleanup, no stale callback use | independent ownership review and mutation tests |
| L09 | call burst batching | 250 ms debounce returns every distinct call once and parks once | deterministic multi-call/close/interrupt tests |
| L10 | checkpoint recovery | dead run rebuild retains completed results without re-executing tools | dropped-stream, unknown-delivery and model-switch fixtures |
| L11 | watchdog/resource bounds | approved phase deadlines, retry/backoff caps, one-hour park expiry, bounded buffers/capacity | deterministic clocks, races and no-progress mutations |
| L12 | integration and handoff | old/new host compatibility and full reference-visible tool loop verified | complete plugin/host tests, native ABI, Linux builds, review loop |

Task acceptance details are recorded in the companion checklist. Keep commits
atomic and verifiable, with no unrelated settings, dependencies or suppressions.

## final gates

Freeze source changes and record both host and plugin HEADs. Run the complete host
suite and production server build, the plugin's `mise run check` and
`mise run test-host`, Linux AMD64/ARM64 builds and architecture verification.
Use the existing macOS workflow for targets requiring the macOS SDK. Never claim
cross-platform runtime results that were not executed. Keep source/permission
checks and host capability absence as explicit cases.

No live credentials, requests, deployments, pushes or service restarts are part
of these gates. Independent reviewers must clear implementation and privacy
findings, with fixes re-tested and re-reviewed before handoff.

## implementation gate

The plan, provider integration and retained HTTP wire contract are approved.
Implement in isolated branches with the stated tests and independent review.
