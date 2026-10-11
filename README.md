# cpa-cursor

Unofficial Cursor OAuth provider plugin for CLIProxyAPI. This repository follows the native Go/C ABI and build conventions of the sibling `cpa-plugin-model-context` project.

The plugin owns Cursor login, auth-file parsing, model discovery and inference. CPA owns credential persistence, scheduling and conversion between client protocols and the plugin's chat-completions input/output. It does not harvest credentials from Cursor's desktop app or execute local shell/filesystem tools.

## Compatibility

CLIProxyAPI v7.3.20 or newer is required for cancellable host HTTP operations. Cursor's CLI protocol is private and may change independently of CPA; the reference client version is `cli-2026.10.01-e373342`. Cursor does not offer a public standalone chat-completions endpoint. See [the design](docs/design.md) for protocol sources and verification boundaries.

## Installation

Build the library and copy `cpa-cursor.so` (Linux) or `cpa-cursor.dylib` (macOS) into CPA's configured plugin directory. The basename is the configuration key; the auth provider identifier is `cursor`.

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    cpa-cursor:
      enabled: true
      api-base-url: https://api2.cursor.sh
      login-url: https://cursor.com/loginDeepControl
      agent-base-url: https://agentn.us.api5.cursor.sh
      client-version: cli-2026.10.01-e373342
      request-timeout: 30
      login-timeout: 300
      max-concurrent-streams: 32
```

Restart CPA after installing the library. Endpoint overrides are administrative settings, not request-controlled URLs; only trusted operators should change them.

## Authentication

Choose provider `cursor` in CPA's plugin OAuth login interface. Open the returned Cursor browser URL and sign in. CPA polls the login and persists the resulting provider credential in its auth directory. Pending sessions are process-local and expire; restarting/reloading the plugin requires starting login again.

Cursor uses a custom PKCE-style browser flow, not RFC 8628 device authorisation. The verifier stays inside the plugin; it is sent only to Cursor's poll endpoint. Do not publish credential files or upstream request logs containing login query parameters.

The official CLI does not implement browser refresh-token exchange. Expired browser credentials require another login. Dashboard API keys can be exchanged for fresh access tokens; this is a different operation from rotating browser refresh tokens.

## Security

- Use only your own authorised Cursor account and observe Cursor's applicable terms and usage limits.
- Protect CPA's management API and auth directory. Auth files contain secrets.
- No arbitrary authenticated HTTP forwarding is provided.
- Tools remain client-owned. The plugin must not perform model-requested local operations on the CPA server.
- Fixture tests do not prove compatibility with a live account. Live OAuth and inference verification must be reported separately.

## Development

The project uses Go, CGO and a C compiler. Build artefacts are placed under `dist/<os>-<arch>/`. Toolchain and CI commands are documented by `mise.toml` and `Makefile`; the checks include pure Go tests, race tests and a native C loader.
