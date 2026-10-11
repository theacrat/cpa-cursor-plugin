# Cursor provider completion

## Scope and acceptance criteria

Complete CPA's Cursor provider integration by implementing its quota capability and fixing the initial standards/spec review findings against `1228bfa...7e03b50` before release.

- Advertise `quota_provider`; implement `quota.identifier`, `quota.describe`, `quota.fetch`, and an explicitly unsupported `quota.reset` without upstream mutation.
- Fetch account-scoped Cursor DashboardService/GetCurrentPeriodUsage using CPA host HTTP, bounded deadlines/body parsing and stored selected-team identity. Keep secrets out of errors and normalised responses.
- Publish only upstream-reported subscription, currency spend, allowance utilisation and billing reset dates. Unknown allowances are not fabricated as empty or unlimited. Do not guess a Pro plan or convert token usage into subscription quota.
- Cover selected team, missing optional fields, malformed/non-finite/negative values, exhausted allowance, zero allowance, unlimited usage, auth/rate-limit/server errors, cancellation, disabled provider and the real C ABI quota dispatch.
- Share credential validation across auth, discovery, inference and quota. Preserve upstream HTTP/Connect error status for CPA scheduling.
- Remove misleading production agent endpoint configuration and unreachable direct-process transport setup; canonical defaults come from Config.
- Preserve sibling native Go/C ABI/build conventions. All production calls use CPA HTTP callbacks; no live-account success is claimed without testing it.

## Review and merge gate

Run mattpocock-skills:code-review with independent Standards and Spec agents, fixed baseline `1228bfa44d70a1e6b0650ebdaaa48307dcb092fe` for the complete implementation and `7e03b50120d504116455fbc63f1199f2efbd03d2` for follow-up changes. Report axes separately; fix findings and repeat until clean. Confirm final HEAD; run formatting, vet, CGO-off/race tests and native ABI smoke without edits during the gate. Wait for CI checks tied to the exact PR head before merging.

## Confirmed upstream schema

Official CLI release `2026.10.01-e373342` contains the DashboardService schema in
`dist-package/index.js`. Source archive:
<https://downloads.cursor.com/lab/2026.10.01-e373342/linux/x64/agent-cli-package.tar.gz>.
The request accepts optional `teamId` and `includePooledUsage`.
Response billing-cycle dates are int64 epoch milliseconds (JSON strings).
`planUsage` includes total/included/bonus spend, remaining and limit plus optional
auto/API spend, limits and utilisation percentages. `spendLimitUsage` includes
individual, pooled and overall spend/limit/remaining fields and `limitType`.
It does not report a subscription plan name; do not infer one from `limitType`.
Community monetary display uses cents, independently of protobuf JSON field types:
<https://github.com/Rahularya01/pi-cursor/blob/ef3cafb5c38081ce6e06df8e594241aabd9621e3/src/usage.ts>.
