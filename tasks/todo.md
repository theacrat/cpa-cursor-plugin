# live client-tool loop tasks

- [x] Complete the earlier provider/tool/metadata/public-ID fixes and reviews.
- [x] Obtain approval for the live-loop specification and CPA host scope.
- [x] Approve the ordered plan and freeze the retained-scope wire contract.
- [x] Approve provider request-purpose propagation with conservative fallback.

## L01: host wire contract

- Acceptance: additive capability/types expose open/prepare/attach/close without
  changing the C function table or existing callback semantics.
- Verify: old/new plugin compatibility fixtures and host production build.
- Likely modules: public ABI/types, lifecycle input, compatibility tests.

## L02: immutable executor ownership

- Acceptance: additive capability carries host-selected caller/account/proxy scope,
  without retaining request objects or changing old callback behaviour.
- Verify: foreign plugin/instance/account/caller cases fail, original callbacks
  remain request-scoped, host production binary builds.
- Likely modules: ABI types, RPC schema, callback scope capture, contract tests.

## L03: independent logging ownership

- Acceptance: retained operations preserve host logging policy and correlation
  after a response ends, without retaining Gin/context request objects.
- Verify: standalone capture lifetime, privacy and finalisation fixtures, build.
- Likely modules: logging helper seam, retained sink, focused tests.

## L04: host lease and HTTP lifecycle

- Acceptance: atomic open/park/attach/close preserves active cancellation and
  bounded parked expiry. Concurrent attach, revoke, unload and shutdown drain.
- Verify: deterministic state/race tests, cancellation versus handoff mutations,
  host production build.
- Likely modules: retained registry, callback cleanup, lifecycle tests.

## L05: successful-delivery handoff

- Acceptance: PreparePark is intent only. The host commits after successful
  tool-call response write/flush. Cancellation/failure before commit closes it.
- Verify: deterministic cancel/delivery/attach races and handoff timeout, build.
- Likely modules: delivery tracker, stream adapter, participating protocol handlers.

## checkpoint A

- [ ] Independent host ownership review is clean.
- [ ] Full host tests, race checks and production build pass at a fixed HEAD.

## L06: plugin retained-host adapter

- Acceptance: negotiated host capability exposes a retained client to the executor.
  Unsupported hosts use stateless execution without omitting callback identity.
- Verify: native C ABI capability/absence/cancellation fixtures and full build.
- Likely modules: native host adapter, dispatch request decoding, ABI fixtures.

## L07: conversation identity and result correlation

- Acceptance: actual session plus caller/account owns a run. Only its newest
  pending batch can consume results. Duplicate/conflicting results are rejected.
- Verify: child/fork/shared-affinity, missing-purpose, changed model/catalogue and
  repeated-history fixtures. Safe public IDs and full skill results survive.
- Likely modules: identity helper, pending mapping, focused protocol fixtures.

### purpose propagation

- Acceptance: a provider-scoped HTTP hook transmits OpenCode's existing request
  kind. Only `primary` can park. Missing or invalid purpose remains stateless.
- Verify: primary/compaction/title/generate header tests and existing variant
  rewrite tests. Preserve all unrelated headers, bodies and provider settings.
- Scope: provider integration repository, approved and implemented on its feature
  branch. All four kinds, stale headers, session identity and variant rewrites pass.

## L08: one live tool round trip

- Acceptance: client receives a tool call while upstream remains alive, then the
  next client request sends the actual result to that same upstream execution.
- Verify: two-request local TLS/host fixture proves one upstream Run, no original
  task replay, actual tool errors and clean interrupt/reload cancellation.
- Likely modules: executor orchestration, retained transport, run state, tests.

## checkpoint B

- [ ] Independent same-run and callback-lifetime review is clean.
- [ ] No new native execution, fabricated approval or direct transport bypass.

## L09: batch collection

- Acceptance: collect a 250 ms burst, return every distinct call once, preserve
  execution mapping and distinguish response closure from a genuine interruption.
- Verify: fake-clock single/multiple/late-call and abort-during-debounce fixtures.
- Likely modules: run event handling, batch timer, protocol tests.

## L10: safe dead-stream recovery

- Acceptance: recovery retains completed results without re-running client tools,
  consuming foreign state or hiding uncertain delivery. Invalid state fails.
- Verify: checkpoint loss, partial delivery, repeated result, switched model and
  summary/title isolation fixtures.
- Likely modules: checkpoint validation, recovery orchestration, protocol tests.

## L11: progress and resource bounds

- Acceptance: phase budgets, safe recovery/backoff limits, parked expiry and
  capacity/blob ceilings are finite. Heartbeats/repeated prefixes are not progress.
- Verify: deterministic clocks, admission pressure, no-progress mutations, race
  cleanup and bounded payload-free diagnostic assertions.
- Likely modules: watchdog, parked registry, configuration bounds, tests.

## L12: final integration

- [ ] Old-host fallback and new-host multi-round/batched loops pass end to end.
- [ ] Reference-visible semantics and intentional safety differences documented.
- [ ] Independent review/fix loop is clean at fixed host/plugin HEADs.
- [ ] Full host tests/build and plugin check, host fixtures, native ABI and Linux
  AMD64/ARM64 builds pass. Unavailable platform runtime checks are disclosed.
- [ ] No deployment, live inference or service changes without a separate request.
