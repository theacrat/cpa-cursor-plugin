# Native ABI smoke

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

Host callbacks reject everything except logging. Registration and reconfiguration
both keep the plugin disabled, so no login, auth refresh or upstream requests run.
Compiler warnings remain errors, except unused static helpers copied by cgo into
the generated header (suppressed only around that include).
