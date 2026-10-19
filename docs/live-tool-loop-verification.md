# live tool-loop verification

## scope

The compatibility change spans the Cursor plugin, a CPA host extension based on
v8.0.29 and the OpenCode CPA provider hook. All source changes are local feature
commits. No deployment, live inference, service restart, push or main-branch merge
has been performed.

Production remains on Cursor plugin revision
`5235a60f7848218a24cc0a0a1b311e85a7a9a654`.

## verified behaviour

- Retain one upstream Run through multiple client-owned tool rounds.
- Collect every call in a 250 ms burst and correlate each result with its exact
  upstream execution identity.
- Preserve complete skill results and actual tool errors.
- Reject foreign, conflicting and duplicated results before partial forwarding.
- Restore an omitted final call list only with stored pending ownership proof.
- Accept equivalent JSON argument replay without changing stored arguments.
- Separate actual sessions, callers, accounts, selected teams and model/catalogue
  changes. Title, compaction, generate and tool-free work cannot claim a coding run.
- Park only after host-verified terminal response write and flush.
- Keep unverifiable writers and unsupported transports on the stateless path.
- Bound retained state, audit capture, expiry and recovery attempts.

The native fixture loads the actual shared library through CPA, its auth manager
and the Gin chat-completions handler. Its upstream is a local synthetic server,
not Cursor. Buffered and streaming continuation, two-call/two-round continuation,
all request purposes and missing-session fallback pass. The unmodified v8.0.29
host also passes its explicit stateless mode.

## commands

Use the pinned environments in each checkout. Host shell commands require
`eval "$(mise env -s zsh)"` before invoking Go or Zig directly.

```sh
mise run check
mise run test-host
mise run build-linux-amd64
mise run build-linux-arm64
python3 -I scripts/check-live-mutations.py
python3 -I scripts/check-live-host.py <host-checkout> --race
python3 -I scripts/check-live-host.py <baseline-checkout> --expect-stateless --race
```

The host gates include its complete `go test ./...`, the full affected-package
race suite and native production-server builds for Linux AMD64 and ARM64. No test
is skipped or disabled to obtain a passing feature result.

## verification boundaries

The full upstream host suite has unrelated intermittent failures. Exact final
revision results and baseline reproductions must remain part of the handoff.
Passing retained-feature tests do not make a failed broader suite green.

The native integration's `--race` instruments CPA only. Separately instrumenting
both loaded Go runtimes failed ThreadSanitizer allocation before a test could run.
The plugin's package-level race suite passes independently. No cross-runtime race
coverage is claimed.

ARM64 builds are compile and architecture checks, not ARM64 runtime verification.
macOS builds and runtime checks require its SDK and were not performed locally.
Real Cursor behaviour and the complete installed OpenCode chain remain unverified
without separately authorised live inference.

## remaining final gate

The production audit finaliser's lifecycle-cancellation fix must be integrated,
independently reviewed and verified at the final host revision before this record
is marked complete.
