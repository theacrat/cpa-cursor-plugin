# Build and checks

Install [mise](https://mise.jdx.dev/), then run:

```sh
mise trust
mise install
mise run hooks
mise run check
```

The toolchain pins are Go 1.27.2 and Zig 0.17.0. A native C compiler and Python 3
are also required. `make` delegates to
mise, so `make check` and `mise run check` run the same checks.

`check` runs Go formatting checks, shell/Python syntax checks, `go vet`, tests with
CGO disabled, race tests with CGO enabled and coverage, then the native ABI smoke.
The pre-commit hook runs the same suite without formatting or staging files. The
checks examine the worktree, not only the staged changes. Run `mise run fmt` to
apply Go formatting explicitly.

```sh
mise run build                  # Native shared library and generated C header
mise run build-linux-amd64
mise run build-linux-arm64
mise run build-darwin-amd64
mise run build-darwin-arm64
mise run test-abi
mise run test-host
```

Build outputs go to `dist/<os>-<arch>/cpa-cursor.so` on Linux and
`dist/<os>-<arch>/cpa-cursor.dylib` on macOS, with a matching `cpa-cursor.h`. Linux
cross-builds use Zig targeting glibc 2.36; macOS builds require a macOS host and
SDK. Unsupported OS/architecture combinations fail explicitly. Native builds
respect `CC`; the ABI smoke expects `CC` to name a native compiler executable.

CI runs native checks on Linux and macOS, then builds all four supported targets
on macOS. It checks Linux ELF architectures, macOS Mach-O architectures and the
generated headers before uploading the libraries and headers together. Coverage
is uploaded separately for each native platform.

See [the native ABI smoke notes](../tests/abi/README.md) for the network-free test
contract and how to smoke-test an existing shared library.

`test-host` verifies the pinned CPA implementation using disposable source copies
under `.scratch`, including provider registry separation, API-key refresh scheduling
and native thinking translation. It does not modify the dependency cache or use
live credentials.

`python3 -I scripts/check-live-mutations.py` applies isolated Go overlays to four
live-loop guards and verifies that the targeted regressions fail. It checks
repeated-prefix progress, earlier history ownership, actual session identity and
upstream execution identity. Source files are not modified. Disposable overlays
are created under `.scratch` and removed after each check.

`python3 -I scripts/check-live-host.py <host-checkout>` snapshots committed host
and plugin revisions, builds the real native library, then runs the live-loop
fixture through CPA's auth manager and Gin chat-completions handler. The host must
descend from the pinned v8.0.29 baseline. The fixture expects the retained HTTP
extension, so the unmodified baseline intentionally fails. Both buffered and
streaming requests must retain exactly one upstream Run, return a real tool result
to its execution identity and preserve complete skill content. The only network
traffic is the fixture's randomly allocated loopback server. Dependency downloads
are disabled and disposable source/build copies are removed after the run.
Add `--expect-stateless` when verifying the unmodified host baseline. Both primary
response modes must then close their upstream run normally without probing any
retained callback.
Add `--race` to instrument both the native library and its host fixture with Go's
race detector.
