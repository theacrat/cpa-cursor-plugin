# live client-tool loop specification

## objective

Match the observable client-tool loop of `openchamber/opencode-cursor` rather
than restarting Cursor after each returned tool call. Keep an upstream run alive
while the caller executes tools, return results to its pending executions and
continue that run without rediscovering the catalogue or restarting the task.

Reference revision: `ef3721207a5941454ab281aab2b14f180e7657e6`, version 2.5.1.
Reference host: CLIProxyAPI v8.0.29, revision
`5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3`.

The operator approved this contract and the CPA host-extension scope. Deployment,
live inference and unrelated repository changes remain separately authorised.

## behaviour

1. Collect genuine registered MCP calls until 250 milliseconds after the latest
   call, then finish the client response with the complete batch and `tool_calls`.
   The upstream run remains parked. Each execution is exposed once with a unique
   bounded public ID and unchanged function arguments.
2. Keep the public ID, original upstream ID and original execution ID pair for
   every pending call. Return caller-supplied results only to their matching
   executions on the same upstream run. Preserve actual errors and complete skill
   content. Do not turn missing results into success.
3. Consume only the newest pending result batch. Repeated full transcripts must
   not resend completed results. A result without a replayed assistant call is
   accepted only when an existing pending mapping proves its ownership. Reject
   duplicate, conflicting and foreign results before sending a partial batch.
4. Distinguish normal response completion after tool calls from a genuine user
   interruption. A new user steer cancels pending work. Normal client connection
   closure after a complete batch must not cancel the parked run. Interruption
   during batch collection must not silently lose calls already exposed.
5. Isolate state by credential/account, stable session identity, request purpose,
   exact upstream model selection, system instructions and catalogue identity.
   Never identify a session only by its first user message. Without a trustworthy
   stable identity, use validated full-history execution rather than sharing state.
6. Preserve completed conversation history when models change, but never send
   results into a parked run for a different model, credential or tool catalogue.
   Tool-free, title and summary requests must not consume a coding agent's run.
7. Recover a dead parked run from a validated checkpoint and retained completed
   results, or from complete verified transcript history. Continue reasoning,
   without executing client tools again. If result delivery is uncertain, cancel
   the old run before a permitted recovery. Otherwise fail with a structured error.
8. Use phase-aware stall budgets: 180 seconds before initial output, 90 seconds
   of total silence after tool results, 45 seconds of initial reasoning-only
   silence, 180 seconds of post-tool reasoning-only silence and 45 seconds after
   visible text. Heartbeats, catalogue chatter and repeated prefixes are not
   forward progress. Retain an absolute per-step execution deadline.
9. Permit at most three safe recovery attempts with one, two and four second
   backoff, and at most one after visible output. Do not reset the budget merely
   because the restarted run emits the same prefix. Preserve permission, quota
   and rate-limit errors. Never infer that no output means an operation was safe.
10. Bound parked runs to 24 and active model steps to the existing configured
    concurrency limit. Bound retained blobs globally to 256 MiB and per run to
    the existing 64 MiB limit. Expire abandoned parked runs after one hour even
    when results never arrive. Reject excess admission instead of silently
    evicting a live pending batch. Shutdown/reload cancels and drains all state.

## host transport prerequisite

The deployed host's executor callbacks are request-scoped. Streaming completion
closes the callback scope, which cancels attached HTTP operations. Non-streaming
callbacks are also closed on return. A Go plugin context alone cannot extend that
host lifetime.

Persistent runs therefore require an explicitly supported, bounded host-owned
conversation HTTP lease or an equivalent verified host capability. The host must
retain credential and proxy policy, request logging, plugin-instance ownership,
revocation and cancellation. A follow-up request may reattach only after matching
its account and session. Parking must be distinct from arbitrary cancellation,
and host shutdown, plugin unload, credential revocation and lease expiry must close
both the HTTP stream and its append operations.

Do not bypass the host with a private HTTP client, omit callback identity, create
an unscoped background operation or keep client responses open to prevent the
host from releasing them. Unsupported hosts retain the existing stateless path
or return an explicit unsupported capability error when live reuse is required.
The approved CPA host changes are limited to this retained-lifetime contract and
its verification. They do not permit unrelated host or provider modifications.

Verified host sources at the pinned revision:

- https://github.com/router-for-me/CLIProxyAPI/blob/5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3/internal/pluginhost/rpc_client_stream.go
- https://github.com/router-for-me/CLIProxyAPI/blob/5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3/internal/pluginhost/http_operation_bridge.go
- https://github.com/router-for-me/CLIProxyAPI/blob/5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3/internal/pluginhost/callback_contexts.go

The existing callback-free HTTP path is not equivalent: it loses the active
request's cancellation attachment and contextual transport/logging ownership.
Changing an operation's stored callback ID cannot reparent an HTTP request that
has already been opened. No retain/park/attach API is advertised by this host.

## implementation setting

Use the existing Go modules, generated protobuf types and native plugin ABI.
Go and Zig versions remain pinned by the project environment configuration. Avoid
new dependencies. Keep transport ownership separate from conversation state and
per-response streaming. Follow surrounding naming and Go formatting conventions.
Add no explanatory code comments or lint suppressions.

## commands and verification

- Focused local protocol fixtures: `mise run test -- -run LiveToolLoop -count=1`.
- Complete native checks and ABI build: `mise run check`.
- Pinned-host integration checks: `mise run test-host`.
- Linux target builds: `mise run build-linux-amd64` and
  `mise run build-linux-arm64`.
- macOS target builds require a macOS host and SDK through the existing workflow.

Implementation belongs in the existing executor, transport and native adapter
modules, with focused conversation-lifetime helpers only where necessary.
Regression fixtures remain beside the existing Go protocol and host tests.
Host lease contract tests belong with the host adapter fixtures.

Tests must first fail for same-run multi-round continuation, burst batching,
client-close versus interrupt, duplicate/missing/orphan results, safe public IDs,
actual tool errors and large skill output. Deterministic-clock and race fixtures
must cover expiry, phase watchdogs, admission, shutdown and concurrent resumes.
Account/session/model/purpose isolation and unavailable-host fallbacks are gates.
Native rejection, metadata, prefixes and existing cancellation tests must remain.
Retain bounded payload-free diagnostics. Independently review each ownership
boundary and the integrated implementation before the final exact-commit builds.

## safety and non-goals

Native filesystem, shell, network and subagent operations stay disabled. Tools
remain caller-owned. No invented consent, answers, success or usage. No silent
skill truncation, guessed call association, unrestricted provider aliases or
automatic replay of tools with unknown effects. Preserve model IDs, lowercase
provider qualifiers, fast tiers and verified context limits.

Do not copy the reference's prompt-derived session fallback, empty native results,
error-as-assistant-text responses or unlimited pending retention. This is behaviour
adaptation, not replacement of auth, discovery or the plugin with a separate app.
If substantial MIT-licensed implementation is adapted, retain the reference's
copyright and permission notice with its exact pinned source revision.

## approved scope and remaining gates

The live-loop contract and host-owned lifetime extension are approved. The host
capability and stable session identity must be proven before the implementation
plan is finalised. Review the ordered implementation plan before coding.

Parked-resource expiry and phase-aware progress watchdogs are intentional,
feature-specific exceptions to the upstream host's established-network timeout
convention. They must not add a blanket HTTP timeout, alter unrelated provider
behaviour or weaken cancellation and unknown-outcome protections.
