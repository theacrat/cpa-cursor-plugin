# retained HTTP scope contract proposal

## status

The operator approved live client-tool loops and the necessary CPA host extension.
This wire contract is proposed for the implementation-plan review. Names in this
document are new APIs, not methods available in the deployed host.

Target host baseline: CLIProxyAPI v8.0.29, commit
`5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3`.
Behaviour reference: opencode-cursor 2.5.1, commit
`ef3721207a5941454ab281aab2b14f180e7657e6`.

## capability negotiation

Add an optional `host_capabilities.retained_http` object to native lifecycle input:

```json
{
  "host_capabilities": {
    "retained_http": {
      "version": 1,
      "max_idle_timeout_ms": 3600000,
      "max_scopes_per_plugin": 24,
      "max_pending_handoff_ms": 5000
    }
  }
}
```

The host advertises this only when ownership, transport, logging, delivery and
cleanup support are all enabled. Absence means unsupported. Existing ABI/schema
numbers alone do not imply support. The C function table is unchanged. The plugin
may decode additive JSON fields and use the existing string callback interface
without replacing its SDK dependency solely for this feature.

## callback methods

| method | request | response |
| --- | --- | --- |
| `host.http.scope_open` | `host_callback_id`, optional `idle_timeout_ms` | `scope_id`, `attachment_id`, effective `idle_timeout_ms` |
| `host.http.scope_prepare_park` | `host_callback_id`, `scope_id`, `attachment_id` | `state: prepared` |
| `host.http.scope_attach` | `host_callback_id`, `scope_id` | new `attachment_id`, `state: attached` |
| `host.http.scope_close` | `scope_id`, current `attachment_id` | empty success result |

The host generates opaque unpredictable scope and attachment handles. Methods do
not accept plugin-selected account, caller, session, provider, model or proxy
authority. One callback may open one scope. Repeated open/prepare/attach operations
from their current owning callback are idempotent. Foreign callers receive only
a fixed unavailable error and cannot cancel the original owner's resources.
Concurrent attach is a conflict. A stale attachment cannot close a newer one.

Add optional `scope_id` to HTTP operation opening and request envelopes. It is
mutually exclusive with `host_callback_id`. Scoped HTTP operations use the retained
scope's bound client and context, not a nil-auth or plugin-supplied replacement.
Existing stream handles retain plugin-instance ownership and close when their
owning scope closes. Old request-scoped operations remain unchanged.

Fixed error codes: `retained_http_unsupported`, `scope_unavailable`,
`scope_conflict`, `scope_not_ready` and `scope_limit`. No error embeds handles,
credential IDs, session IDs, proxy addresses or request content.

## state and delivery handoff

`attached -> prepared -> parked -> attached`, with any state able to close.

Create the upstream lifetime from the host lifecycle before opening the network
request. Register cancellation against the current active request separately.
Do not attempt to reparent an existing HTTP request or retain its Gin context.

PreparePark records intent only and leaves active cancellation effective. After
the handler successfully writes and flushes the terminal tool-call response, a
host-internal commit atomically removes the request's cancellation attachment and
marks the scope parked. A cancellation, delivery failure or stale attachment
that wins before commit closes it instead. Server write/flush success does not
prove the client consumed the response, so result correlation remains mandatory.

Callback cleanup may leave a prepared scope pending only for the advertised short
handoff interval. It must not commit parking because a plugin channel closed.
The handler must propagate write errors instead of ignoring them. Asynchronous
request-completion notifications are not a commit mechanism. Attach racing an
unfinished handoff returns `scope_not_ready` without stealing it.

Non-streaming and streaming paths need their own verified successful-delivery
handoff. A protocol without that seam cannot advertise retained support. Close on
normal completed assistant output, not only on failures.

Initial support is limited to chat-completions requests, with both response modes
tested. Other client protocols retain stateless execution until their handlers
provide the same verified delivery boundary.

## authoritative ownership

Capture an immutable binding at executor selection before serialisation removes
the selected host HTTP client. It includes the loaded plugin instance, host-derived
authenticated caller scope, actual OpenCode session, request purpose, selected
upstream account/version, exact model/variant and effective transport policy.
Do not retain arbitrary context values, request/response objects or raw API keys.

Use exactly one valid `x-opencode-session-id`. OpenCode's `x-opencode-session`,
`X-Session-Id` and parent headers are lineage/affinity and may be shared by children
or forks. They cannot own a parked run. Pair session identity with the authenticated
caller and selected account. Store versioned digest keys, not raw identifiers.
Missing, anonymous or conflicting ownership uses the agreed stateless fallback.

Explicit request purpose must be propagated before stateful mode is enabled for
the client chain. A proposed `x-cpa-request-kind` header carries exactly `primary`,
`compaction`, `generate` or `title`, from the existing OpenCode HTTP hook's `kind`.
The host pairs this client assertion with authenticated scope. Only `primary`
requests may park. Missing/invalid purpose stays stateless. This client-hook change
requires separate approval for the provider integration repository.

On attach, derive the binding again from the new request. Disabled/replaced/removed
credentials, different callers/sessions, model or catalogue changes, incompatible
proxy policy, reload and plugin-instance changes prevent reuse. A user steer
retires pending work without discarding completed caller history.

## retained transport and logging

Use the selected execution transport with its credential and proxy policy. A
retained log sink owns immutable policy/correlation metadata and can finalise
independently of the old client response. Preserve host enablement, redaction and
upstream capture semantics. Never retain a Gin request, writer or whole context
through `context.WithoutCancel` as a substitute for explicit capture ownership.

While parked, scoped network housekeeping does not authorise local/native tools
or client-tool execution. Keep the plugin's protocol queue and append writer
bounded. Apply the approved blob limits, parked capacity, one-hour abandoned-park
expiry and per-step deadline. Cancel/drain on explicit close, expiry, revoke,
disable, quiesce, unload and host shutdown.

## acceptance tests

- Old plugins ignore the additive input and remain request-scoped. New plugins
  refuse to emulate unsupported retained callbacks.
- A fake upstream survives a successful tool-call response and accepts results
  through a later authorised request on the same Run.
- Cancellation before commit, failed write/flush, handoff expiry and concurrent
  attach cannot leak or steal a scope.
- Foreign plugin/instance/caller/session/account/model/proxy attachments fail.
- Independent logging continues after normal response completion and finalises
  without retaining a request object or leaking secrets.
- Explicit tool errors, large skills, result IDs and complete batches remain
  correctly associated. Unknown delivery does not replay tool execution.
- Expiry, revocation, disable, reload, unload and shutdown release operations,
  blocked reads, buffers and goroutines under deterministic race fixtures.

## source basis

- https://github.com/router-for-me/CLIProxyAPI/blob/5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3/internal/pluginhost/rpc_client_stream.go
- https://github.com/router-for-me/CLIProxyAPI/blob/5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3/internal/pluginhost/http_operation_bridge.go
- https://github.com/router-for-me/CLIProxyAPI/blob/5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3/internal/pluginhost/callback_contexts.go
- https://github.com/anomalyco/opencode/blob/843a6c514e606b436a7fe713422ace0a93ad3d61/packages/core/src/session/model-request.ts
