# Build and checks

Install [mise](https://mise.jdx.dev/), then run:

```sh
mise trust
mise install
mise run hooks
mise run check
```

The toolchain pins match the sibling model-context plugin: Go 1.27.1 and Zig
0.17.0. A native C compiler and Python 3 are also required. `make` delegates to
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
