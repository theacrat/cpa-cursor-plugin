# live tool-loop verification

## scope

The compatibility change spans the Cursor plugin, a CPA host extension based on
v8.0.29 and the OpenCode CPA provider hook. All source changes are local feature
commits. No deployment, live inference, service restart, push or main-branch merge
has been performed.

Production remains on Cursor plugin revision
`5235a60f7848218a24cc0a0a1b311e85a7a9a654`.

## verified behaviour

Last native integration: host `04d02ccaeaf2f72523ec53e50b30a6c55a3032c7`,
plugin `576be9639059f7ec85366e7ad7a53dc9cb27ef82`, provider hook
`3c3c486f13c092d8b05c21e31a92c638492acb61`.

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

At the current host checkpoint
`04d02ccaeaf2f72523ec53e50b30a6c55a3032c7`, using Go 1.27.2:

- The full suite failed in `TestResponsesInterruptStopsHTTPUpstream` and
  `TestPutOpenAICompatConcurrentWithAuthFieldsPatch_Issue6559`.
- The full affected-package race suite failed only in
  `TestResponsesInterruptStopsHTTPUpstream`. No data-race report appeared.
- Both failures also reproduced on the untouched baseline. All retained-feature
  race checks and the native nine-case integration passed.
- Production server builds passed for Linux AMD64 and CGO-enabled ARM64.

The production audit finaliser now accepts lifecycle cancellation. Its real Home
submission fixture proves that cancellation closes only the operation-owned
connection, releases spools/reservations and leaves the shared Home client usable.
Ordinary filesystem syscalls cannot be forcibly interrupted. Cancellation is
checked before and between operations without abandoning an I/O goroutine.

At host `18167e530b8b0c8784b65c30b4b2f431534933ff`, using Go 1.27.2:

- `go test ./... -count=1 -timeout=240s` failed in
  `TestWebsocketRetryBindFailureClearsActiveSessionState/Codex_stream` and
  `TestPutOpenAICompatConcurrentWithAuthFieldsPatch_Issue6559`.
- The affected-package race run failed only in
  `TestResponsesLateInterruptDoesNotPoisonNextCreate`, without a data-race report.
- The retained-feature and legacy HTTP/stream regression race selection passed
  across all six affected packages.
- Native server builds passed for Linux AMD64 and CGO-enabled ARM64.

The OpenAI compatibility race and late-interrupt failures reproduced on the
untouched host baseline. The retry-bind EOF remains unclassified. Its fixture and
relevant WebSocket implementation are unchanged, but 1,000 focused baseline
iterations passed. That is not proof of a pre-existing failure and it must not be
reported as one. Earlier runs also reproduced baseline Codex/XAI keepalive upload
failures. No unrelated tests or provider code were changed to hide these results.

The bounded retry-bind baseline check used a fresh archive of
`5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3` with this command:

```sh
go test -json ./internal/runtime/executor \
  -run '^TestWebsocketRetryBindFailureClearsActiveSessionState$/^Codex_stream$' \
  -count=1000 -timeout=120s
```

It recorded exactly 1,000 target-subtest passes and zero failures. The final-model
compatibility failure reproduced in the same baseline with:

```sh
go test ./sdk/cliproxy \
  -run '^TestPutOpenAICompatConcurrentWithAuthFieldsPatch_Issue6559$' \
  -count=50 -timeout=120s
```

The native integration's `--race` instruments CPA only. Separately instrumenting
both loaded Go runtimes failed ThreadSanitizer allocation before a test could run.
The plugin's package-level race suite passes independently. No cross-runtime race
coverage is claimed.

ARM64 builds are compile and architecture checks, not ARM64 runtime verification.
macOS builds and runtime checks require its SDK and were not performed locally.
Real Cursor behaviour and the complete installed OpenCode chain remain unverified
without separately authorised live inference.

## handoff status

The production audit finaliser's lifecycle-cancellation fix is integrated,
independently reviewed and verified. A separate memory-only registry integration
check confirmed that cancelling its drain reaches a real retained capture and
releases the closing slot, spool files and byte reservations exactly once.

The feature review is clean. The broad host suite is not green, so this is not a
deployment-ready or live-compatibility claim. Changes remain on the local
`feat/live-tool-loop`, `feat/retained-http` and `feat/request-purpose` branches.
The source checkouts are retained for handoff. Merged auxiliary worktrees and
disposable verification copies are removed.
