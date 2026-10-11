# Initial mandatory two-axis review

Fixed point: `1228bfa44d70a1e6b0650ebdaaa48307dcb092fe`.
Reviewed HEAD: `7e03b50120d504116455fbc63f1199f2efbd03d2`.
Diff: `git diff 1228bfa...HEAD`.
Commit: `7e03b50 Build Cursor OAuth provider for CLIProxyAPI (#1)`.

Standards sources: `docs/design.md`, `docs/cursor-wire.md`, `docs/tooling.md`,
`README.md`, and sibling CPA plugin build/ABI conventions. Reviewers separately
checked Standards (including the skill's Fowler smell baseline) and Spec.

## Standards

1. Documented violation: execution/discovery accept other-provider or malformed
   credential records, bypassing auth parsing. Violates the design's requirement
   to reject unsupported provider records and malformed credentials.
2. Possible speculative generality: nativeRun has unreachable production
   transport/proxy setup; it is only called with a test-injected client.
3. Possible duplicated code: concurrency/timeout/endpoint defaults differ across
   modules despite the canonical Config defaults.

## Spec

1. Inference HTTP/Connect errors lose status, preventing CPA scheduling from
   distinguishing authentication, throttling and retryable server failures.
2. Execution/discovery bypass provider and credential validation.
3. Quota capability and identifier/describe/fetch/reset methods are absent.
4. Advertised agent-base-url has no effect on production host transport.

## Resolution gate

These are findings, not a clean review or a completion claim. The quota follow-up
must fix them, pass the native ABI tests and repeat both review axes until clean.
Live-account compatibility remains unverified regardless of fixture results.

One generic prior reviewer recommended using AgentService model discovery based
on a community client. That recommendation is rejected: the inspected official
CLI calls `aiserver.v1.AiService/GetUsableModels` on the API backend. The official
schema and route take precedence over the community implementation.
