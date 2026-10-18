# Native ABI smoke

The fixture also verifies quota registration, discovery, disabled-provider rejection,
a credential-scoped quota fetch through deterministic host HTTP callbacks, and
unsupported reset. No real network or live credentials are used. The normalised
quota response must not expose the fixture access token.

Run `mise run test-abi`, or `bash scripts/abi-smoke.sh /absolute/path/to/cpa-cursor.so`
to test an existing build (`.dylib` on macOS). The matching generated c-shared
header must be beside the library. It requires a native C compiler, Go and Python 3;
no Python packages or network access are needed.

The C host loads the real shared library and uses its generated function table. It
checks ABI version 1, plugin registration with base64-encoded `enabled: false`, the
`cursor` auth identifier, quiesce, disabled reconfigure, shutdown, response ownership and
idempotent shutdown. The Python driver validates the JSON responses and enforces a
30-second timeout. The Go runtime remains loaded until process exit rather than
calling `dlclose`.

Host callbacks simulate quota HTTP operations, accept diagnostic log calls and
reject every other upstream request. The fixture verifies an empty model catalogue
when prefix-only routing is off. It re-enables the provider for its synthetic quota fetch;
no login, auth refresh or real upstream requests run.
Compiler warnings remain errors, except unused static helpers copied by cgo into
the generated header (suppressed only around that include).
