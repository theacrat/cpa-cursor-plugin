# Cursor provider plugin

## Objective

Build `cpa-cursor.so` / `cpa-cursor.dylib` as a native CLIProxyAPI provider plugin. Follow the sibling `cpa-plugin-model-context` project: Go, `cmd/` ABI shell, testable `internal/` packages, mise and Make tasks, native C ABI smoke tests, and Linux/macOS amd64/arm64 artefacts.

## Contract

The provider identifier is `cursor`. The filename/config identifier is `cpa-cursor`. Implement auth parsing, browser PKCE login start and polling, token refresh, authenticated model discovery, and inference execution. Declare chat-completions input/output so CPA performs its existing protocol translations for other client protocols. Never register models without a usable Cursor credential.

CPA owns credential persistence and scheduling. Persist provider-owned storage JSON with `type: cursor`, access/refresh tokens and expiry. Preserve host identity, prefixes, proxy settings, disabled state and user metadata across refresh. Login secrets remain in bounded, expiring process state, not in URLs or host-visible metadata. No automatic harvesting of desktop credentials.

## Upstream protocol

Cursor does not publish a standalone chat-completions API. Its browser login and native Connect/protobuf inference are unofficial interfaces and can change. Validate endpoints and schemas against current community clients and record pinned source references before building the wire implementation. Do not substitute an invented OpenAI upstream endpoint.

Authentication and unary discovery use the CPA HTTP callback when available. Native agent runs require bidirectional HTTP/2 and cannot be modelled as a fixed request body followed by response chunks. Determine whether the pinned host exposes a duplex transport before choosing between native transport and a documented bridge. A transport exception must preserve proxy policy, cancellation, deadlines and credential isolation; it must be explicitly documented rather than silently bypass host request logging.

## Safety and lifecycle

Reject unsupported provider records and malformed credentials. Never include upstream response bodies, tokens or PKCE verifiers in errors. Validate endpoint origins and prohibit credential-bearing arbitrary HTTP forwarding. Bound login sessions, response sizes, Connect frames and concurrent streams. Cancel upstream runs before unload, drain dispatch before replacing configuration, and prevent callbacks after shutdown. Do not execute model-requested local filesystem or shell operations inside the plugin: return client tool calls or reject unavailable operations.

## Verification

Deterministic fixtures cover login pending/success/expiry/replay, refresh rotation and identity preservation, authenticated discovery, conversation/tool conversion, fragmented and compressed Connect frames, upstream errors, stream close/cancellation, reload and shutdown. Gate changes with gofmt, go vet, CGO-off tests, CGO race tests, and a native C loader exercising the real exported ABI. CI builds supported target libraries and release bundles. A real account end-to-end check is separate from fixture verification and is not claimed without explicit evidence.

## Sources

- CLIProxyAPI SDK `pluginapi` and `pluginabi`; latest module discovered at project start: v7.3.20.
- Cursor CLI authentication: <https://cursor.com/docs/cli/reference/authentication>.
- Cursor API scope: <https://cursor.com/docs/api>.
- Community protocol reference under investigation: <https://github.com/Rahularya01/pi-cursor>.
