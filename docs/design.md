# Cursor provider plugin

## Objective

Build `cpa-cursor.so` / `cpa-cursor.dylib` as a native CLIProxyAPI provider plugin. Follow the sibling `cpa-plugin-model-context` project: Go, `cmd/` ABI shell, testable `internal/` packages, mise and Make tasks, native C ABI smoke tests, and Linux/macOS amd64/arm64 artefacts.

## Contract

The provider identifier is `cursor`. The filename/config identifier is `cpa-cursor`. Implement auth parsing, browser PKCE login start and polling, token refresh, authenticated model discovery, and inference execution. Declare chat-completions input/output so CPA performs its existing protocol translations for other client protocols. Never register models without a usable Cursor credential.

CPA owns credential persistence and scheduling. Persist provider-owned storage JSON with `type: cursor`, access/refresh tokens and expiry. Preserve host identity, prefixes, proxy settings, disabled state and user metadata across refresh. Login secrets remain in bounded, expiring process state, not in URLs or host-visible metadata. No automatic harvesting of desktop credentials.

## Upstream protocol

Cursor does not publish a standalone chat-completions API. Its browser login and native Connect/protobuf inference are unofficial interfaces and can change. Validate endpoints and schemas against current community clients and record pinned source references before building the wire implementation. Do not substitute an invented OpenAI upstream endpoint.

Authentication and unary discovery use the CPA HTTP callback. Native agent runs require bidirectional communication and cannot be modelled as a fixed request body followed by response chunks. The official CLI provides HTTP/1-compatible emulation with `AgentService/RunSSE` and `BidiService/BidiAppend`; this permits all inference traffic to use CPA's transport, cancellation and request-log policy without a direct-process HTTP exception.

The current official CLI exchanges dashboard API keys at `/auth/exchange_user_api_key`; it does not use that endpoint to rotate stored browser refresh tokens. Browser credentials therefore require a new login when expired unless a verified refresh interface is added later. The login response's selected team must be retained and supplied as `x-cursor-team-id` on RPC requests.

## Safety and lifecycle

Reject unsupported provider records and malformed credentials. Never include upstream response bodies, tokens or PKCE verifiers in errors. Validate endpoint origins and prohibit credential-bearing arbitrary HTTP forwarding. Bound login sessions, response sizes, Connect frames and concurrent streams. Cancel upstream runs before unload, drain dispatch before replacing configuration, and prevent callbacks after shutdown. Do not execute model-requested local filesystem or shell operations inside the plugin: return client tool calls or reject unavailable operations.

## Verification

Deterministic fixtures cover login pending/success/expiry/replay, refresh rotation and identity preservation, authenticated discovery, conversation/tool conversion, fragmented and compressed Connect frames, upstream errors, stream close/cancellation, reload and shutdown. Gate changes with gofmt, go vet, CGO-off tests, CGO race tests, and a native C loader exercising the real exported ABI. CI builds supported target libraries and release bundles. A real account end-to-end check is separate from fixture verification and is not claimed without explicit evidence.

## Sources

- CLIProxyAPI SDK `pluginapi` and `pluginabi`; latest module discovered at project start: v7.3.20.
- Cursor CLI authentication: <https://cursor.com/docs/cli/reference/authentication>.
- Cursor API scope: <https://cursor.com/docs/api>.
- Community protocol reference under investigation: <https://github.com/Rahularya01/pi-cursor>.
- Official CLI inspected: <https://downloads.cursor.com/lab/2026.10.01-e373342/linux/x64/agent-cli-package.tar.gz>, client version `cli-2026.10.01-e373342`.
- Independent native implementation: <https://github.com/Yuki13929/cursor2api/tree/ebd29e0f9377a56f0b609683b9927cd75d18173f>.
