# client tools and model-switch compatibility

## observed failures

The reported Grok session `ses_eae92fe84ffe37TpTO7P7R5xC2` contains two durable
client-tool calls, both completed `skill` invocations. Its claimed read, shell and
dynamic-tool failures did not produce OpenCode client-tool records. Those claims
alone cannot prove the client catalogue was missing.

The second reported session `ses_eb46c55dfffekif88EzQqKMN1z` records Cursor Opus
returning HTTP 429 at `msg_1515a4cdd001u4Eyh5biROGIu8`. After switching to the
opencode-go Grok route, compaction returned HTTP 403 with `plugin call failed` at
`msg_1515f7c11001f3L3CrAqXFtZMN`. The compaction row has no model field, but the
preceding switch and OpenCode's dispatch logic indicate the opencode-go route.
There is no captured Cursor compaction failure. Quota, eligibility and bridge
protocol failures must remain separate diagnoses.

## reproduced bridge defects

The bridge did not recognise the provider-qualified advertised spelling
`cpa-mcp_cpa_read` when supplied through McpArgs.name. The public catalogue, request
instructions and refusal messages did not explain the exact registered entries,
while generic native failures made it easy to conclude that all tools were absent.
Typed refusals, explicit executable-client guidance, available-tool lists and a
bounded rejection counter now distinguish these paths. No native execution or
automatic native-to-client argument conversion is introduced.

An initial assistant-ended summary fixture was rejected by the bridge, but source
inspection of OpenCode v2.0.29 confirms its real compaction request ends in a user
summary prompt. The speculative input relaxation was removed. Faithful fixtures
now retain historical exchanges, preserve the final summary prompt and do not
re-expose tools missing from the current catalogue. Assistant prefills remain
unsupported, rather than being silently reinterpreted as summaries.

## cumulative review corrections

Shared discovery no longer propagates a cancelled fetch owner's context error to
other live requests. A live waiter can become the replacement owner with its own
host request. An upstream failure or the discovery request's own timeout still
propagates without a retry loop.

Thinking metadata distinguishes the selected bare/default route from a marker on
another variant. A marked thinking variant no longer hides a valid off/high toggle
when the bare route itself is unmarked.

Catalogue filtering removes discovery-generated model-ID qualifiers when their
duplicates are no longer visible, while preserving unrelated custom suffixes.

Tool identities are reserved across the union of historical and current original
function names. A historical-only tool remains distinct from a differently named
current tool even when their normalised aliases would overlap. Only the current
enabled catalogue enters executable resolution, so replay cannot revive a removed
tool or dispatch a different function through its old identity.

All four cumulative review findings have regression fixtures and were independently
re-reviewed after correction. Mutations that disable cancellation re-election,
restore family-wide thinking suppression, retain obsolete label qualifiers or
remove historical alias reservations are caught by the tests. No new blocker was
reported in the integrated review at `85901d1`.

## Composer interaction follow-up

The Composer 2.5 session `ses_eadc532d7ffe5EjWmJ52zZfhNz` recorded two failures
with `Cursor interaction query requires an unsupported interactive approval flow`.
The bridge previously aborted every interaction query without returning a result.
Supported queries now receive typed negative replies and preserve correlation IDs,
then allow a genuine registered MCP call. No approval, answer, plan or VM success
is invented. Unknown shapes and VM setup remain fail-closed, and interaction
refusals share the existing retry bound. The stored failures do not identify the
exact query subtype, so live Composer acceptance is not claimed.

## Grok skill follow-up

The later Grok 4.6 session `ses_ead01bc29ffedg5XVnuUniEin4` completed its four
skill loads and reached file operations before the linked step was interrupted.
Ten skill calls across the four scoped sessions completed with valid arguments
and non-empty results. Their tool-call IDs contained literal newlines and exceeded
64 bytes, while the four observed Sol skill-call IDs did not.

The inspected reference `yobo2u/omsub`, branch `cursor`, commit
`c43af3c564d444067401e013a74f7a1ec35549a8`, normalises the same composite ID
shape. This supports output-boundary hardening rather than changing skill files,
discarding skill results or flattening the structured conversation. A redacted
OpenChamber timeline without exception messages or stack traces does not prove
that these IDs caused its renderer errors. Native-tool rediscovery claims and
interrupted model steps remain separate from successful client skill execution.

Newly emitted tool-call IDs preserve ASCII letters, digits, underscores and
hyphens up to 64 bytes. Other upstream IDs become deterministic 48-character
`call_` IDs using the SHA-256 digest of the complete original value, encoded as
base64url. Missing IDs retain the fresh-ID fallback. Existing transcript IDs are
never rewritten, and a new public ID matching historical calls is rejected with
a fixed error rather than risking result misassociation. Skill content and tool
arguments remain unchanged in both response modes and full conversation replay.

## verification boundary

Local TLS HTTP/2 fixtures exercise native refusal followed by a real client-tool
call, all known aliases, namespace collisions, typed failure replies, bounded
retries, actual user-ended summary requests and preserved HTTP 403/429.
These fixtures do not claim a live Grok success or a repaired external quota.

Deployment requires explicit authorisation. No live model request, quota override,
provider switch or external repository modification is authorised by these fixes.

## external findings

The linked 403's original explanation is not recoverable from the stored error.
Pinned CPA replaces empty plugin error messages with `plugin call failed`, then
labels generic HTTP 403 as `insufficient_quota`. OpenCode classifies that code as
quota before considering generic permission failures. This proves a rejection,
not specifically exhausted quota. The responsible external provider plugin and
its sanitised upstream error need inspection before claiming a quota fix.

OpenCode v2.0.29 normally uses the selected session model for compaction, unless a
compaction-agent override exists. Its estimation can reuse previous token usage
when only the aggregator provider ID matches, even after changing model routes.
The last successful response before this switch reported 830,216 input tokens.
This explains an immediate compaction attempt, but historical model limits are
not stored and do not prove that compaction was unnecessary. No usage or context
limits are falsified to suppress it.

Relevant OpenCode source is version 2.0.29, commit
`042d0832f5f3a73eaa039a58a7d565776f6d4290`, especially
`packages/core/src/session/compaction.ts:213-220,285-296,581-590,918-923`.
The provider-only token provenance check and external Go-provider error handling
are outside this repository. Neither is reported as fixed by this patch.
