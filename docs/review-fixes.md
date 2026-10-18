# provider reliability verification

The findings recorded against `53ff244` are addressed by focused regression tests
and the pinned CPA host contract fixtures.

| finding | protection |
| --- | --- |
| shared provider model ids | discovery publishes an empty catalogue when prefix-only routing is off, removing stale registrations without making an upstream call |
| dormant api-key refresh | current token metadata and the host refresh interval survive parsing and refresh, with stale host metadata unable to restore an old token |
| cross-account model lookup blocking | network fetches run outside the cache mutex, same-account misses share a result and cancelled waiters return promptly |
| discovery deadline | the configured control-plane timeout bounds model discovery separately from the inference execution deadline |
| tool alias collisions | each allocated tool name reserves its raw and generated alias spellings together |
| lost enabled thinking | native thinking survives the host's implicit auto translation without overriding explicit suffixes or client defaults |
| go advisories | the build toolchain is pinned to go 1.27.2 |

## gates

`mise run check` covers formatting, helper syntax, vet, pure Go tests, race tests,
coverage, the shared-library build and the native C ABI smoke. The ABI smoke also
checks that unsafe host routing produces an empty model catalogue.

`mise run test-host` runs provider-registry, refresh-scheduler and thinking-translator
checks against a disposable copy of the pinned host. It feeds real plugin auth data
into the host scheduler and passes the host translator's output back into the plugin.
These fixtures run in CI and before releases.

`govulncheck` v1.9.0 with go 1.27.2 reports no known vulnerabilities. Mutating the
prefix-only routing guard and removing the refresh interval each fail their focused
regression test.

Live OAuth, live inference, deployment and cross-platform builds are not claimed by
these local fixtures. Set CPA's top-level `force-model-prefix: true` and restart CPA
after installing the new shared library.
