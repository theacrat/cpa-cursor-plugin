# cpa-cursor

Unofficial Cursor OAuth provider plugin for CLIProxyAPI. This repository follows the native Go/C ABI and build conventions of the sibling `cpa-plugin-model-context` project.

The plugin owns Cursor login, auth-file parsing, model discovery and inference. CPA owns credential persistence, scheduling and conversion between client protocols and the plugin's chat-completions input/output. It does not harvest credentials from Cursor's desktop app or execute local shell/filesystem tools.

## Compatibility

CLIProxyAPI v7.3.20 or newer is required for cancellable host HTTP operations. Cursor's CLI protocol is private and may change independently of CPA; the reference client version is `cli-2026.10.01-e373342`. Cursor does not offer a public standalone chat-completions endpoint. See [the design](docs/design.md) for protocol sources and verification boundaries.

## Installation

Build the library and copy `cpa-cursor.so` (Linux) or `cpa-cursor.dylib` (macOS) into CPA's configured plugin directory. The basename is the configuration key; the auth provider identifier is `cursor`.

```yaml
force-model-prefix: true

plugins:
  enabled: true
  dir: plugins
  configs:
    cpa-cursor:
      enabled: true
      api-base-url: https://api2.cursor.sh
      login-url: https://cursor.com/loginDeepControl
      client-version: cli-2026.10.01-e373342
      request-timeout: 30
      login-timeout: 300
      max-concurrent-streams: 32
      execution-timeout: 600
      model-prefix: cursor
```

CPA also writes a host-owned `priority` key into this block; the plugin accepts it and ignores it. Other unknown keys are rejected.

Restart CPA after installing the library. Endpoint overrides are administrative settings, not request-controlled URLs; only trusted operators should change them.

## Model IDs

Cursor serves many models that other providers also serve under the same ID,
such as `claude-opus-5-5`. CPA keeps one registry row per ID and routes a bare
ID to any provider registering it, so unprefixed Cursor models would hide the
other provider's row and quietly take some of its traffic. Cursor models are
therefore published as `cursor/<id>` (for example `cursor/claude-opus-5-5`);
CPA strips the prefix before the request reaches this plugin. Set
`model-prefix` to choose another namespace, or store `prefix` in a credential
file to override it per account. The configured default is applied when a
credential is loaded or logged in, not written to its file; refreshes keep the
host's current prefix.

CPA's top-level `force-model-prefix: true` is required. Without it the plugin
withholds its model catalogue rather than registering colliding bare IDs, allowing
CPA to remove stale registrations too. This host setting also requires explicit
prefixes for other accounts that have one.

This is a breaking change: saved configs must switch to `cursor/<id>`.

Unique picker labels use only the model family name, without effort words,
context sizes or NO ZDR annotations. `(Fast)` and `(Cursor)` appear only when
needed to distinguish duplicate names in the returned catalogue, with the route ID
as a last resort. Discovered fast tiers remain separate `cursor/<id>-fast` models,
each with its own thinking selector, even when only one tier is visible. Structured
metadata and existing standard model IDs remain unchanged. The legacy `gpt-5.5-extra` family is folded into
`gpt-5.5` with `xhigh` effort, because `extra-high` names the effort, not a model.

## Thinking switcher

Each Cursor model and speed tier appears once, with its reasoning efforts behind CPA's
native `model(level)` suffix: `cursor/claude-opus-4-8` for the default effort,
`cursor/claude-opus-4-8(high)` for high, and so on through `minimal`, `low`,
`medium`, `high`, `xhigh` and `max`. A plain `reasoning_effort` request
parameter (or the client's native thinking field) selects the variant too;
an explicit `(level)` suffix always wins. Requests without any effort signal
use the bare variant when one exists, otherwise medium.

Only discovered effort levels appear in metadata. A thinking toggle is exposed
as `high`, which selects its discovered `-thinking` variant. Automatic fallback
for an explicitly requested unsupported effort remains unchanged.

Earlier versions registered one row per effort variant
(`claude-opus-4-8-high`, …). Those IDs are gone; switch saved configs to the
prefixed base ID with an optional `(level)` suffix.

## Model metadata

Cursor's authenticated catalogue supplies context limits, image support and agent
tool support when reported. Exact upstream IDs take precedence over aliases, and
shared family limits use the smallest verified value across selectable variants.
Unknown limits, including output-token limits not reported by Cursor, remain absent.
No limits are guessed from model names or copied from a vendor's direct API.

Model lists expose verified metadata on existing namespaced Cursor rows, including
input/output modalities, client-tool support and available reasoning controls.
This restores fields CPA's normal OpenAI model list omits without adding models,
changing route IDs or modifying other providers. Explicit list metadata is preserved.
List requests use cached discovery data and never make upstream requests.

See [metadata sources and boundaries](docs/model-metadata.md).

## Quota

The plugin reports Cursor Dashboard usage through the host HTTP transport. It publishes only fields returned by Cursor; monetary values are upstream cents converted to USD, and absent or null limits remain absent. Quota reset is not supported.

## Authentication

Choose provider `cursor` in CPA's plugin OAuth login interface. Open the returned Cursor browser URL and sign in. CPA polls the login and persists the resulting provider credential in its auth directory. Pending sessions are process-local and expire; restarting/reloading the plugin requires starting login again.

The native management endpoint is `GET /v0/management/cursor-auth-url`. Use your usual CPA management authentication header; the response contains `url` and `state`. After signing in, poll `GET /v0/management/get-auth-status?state=<state>` with the same management authentication. Do not put management keys into URLs.

Cursor uses a custom PKCE-style browser flow, not RFC 8628 device authorisation. The verifier stays inside the plugin; it is sent only to Cursor's poll endpoint. Do not publish credential files or upstream request logs containing login query parameters.

The official CLI does not implement browser refresh-token exchange. Expired browser credentials require another login. Dashboard API keys can be exchanged for fresh access tokens; this is a different operation from rotating browser refresh tokens.

Dashboard API-key credentials supply CPA's refresh policy and current token expiry,
so automatic re-exchange starts five minutes before expiry. Browser-only credentials
do not receive that refresh policy.

Existing credentials can also be imported through CPA's auth-file interface:

```json
{
  "type": "cursor",
  "id": "cursor-my-account",
  "access_token": "REPLACE_WITH_ACCESS_TOKEN",
  "refresh_token": "REPLACE_WITH_REFRESH_TOKEN",
  "expires_at": "2026-10-11T12:00:00Z"
}
```

Use a stable unique `id` for access-token-only records. Include an optional `api_key` only when deliberately enabling dashboard API-key re-exchange. `selected_team_id`, `prefix`, `proxy_url`, `label`, `email` and `disabled` are preserved when supplied. Values shown above are placeholders, not working credentials.

## Security

- Use only your own authorised Cursor account and observe Cursor's applicable terms and usage limits.
- Protect CPA's management API and auth directory. Auth files contain secrets.
- No arbitrary authenticated HTTP forwarding is provided.
- Tools remain client-owned. The plugin must not perform model-requested local operations on the CPA server.
- Fixture tests do not prove compatibility with a live account. Live OAuth and inference verification must be reported separately.

## Development

The project uses Go, CGO and a C compiler. Build artefacts are placed under `dist/<os>-<arch>/`. Toolchain and CI commands are documented by `mise.toml` and `Makefile`; the checks include pure Go tests, race tests and a native C loader.

```bash
mise install
mise run check
mise run build
git config core.hooksPath .githooks
```

See [tooling](docs/tooling.md) for cross builds and [the ABI smoke fixture](tests/abi/README.md) for native-loader verification. Tag releases as `vX.Y.Z` to publish platform archives and SHA-256 checksums.
