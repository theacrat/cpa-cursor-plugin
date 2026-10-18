# verified Cursor model metadata

## approved scope

Expose context size, output limits, image support, client tool support and reasoning
options for Cursor models in the model picker, using verified values only. Unknown
values remain absent. Preserve Cursor's separate namespace and leave deployment
outside this change.

## contract

Cursor's authenticated catalogue is authoritative for limits and image support.
Match exact upstream IDs before unambiguous family metadata, keeping parameterised
and max-mode variants separate. Do not infer limits from model names or copy vendor
API limits into Cursor models. The executor's implemented client MCP tools and text
output are provider capabilities. Reasoning controls describe discovered variants,
not guessed model-family capabilities.

Publish standard plugin model metadata for the host registry and restore fields
omitted by model-list serialisation only on existing, namespaced Cursor rows. Never
add models, alter routing IDs, modify another provider's rows or intercept inference.
Keep metadata account-scoped, bounded, cancelled on reload and safe when optional
catalogue enrichment fails.

## implementation and checks

Use the existing Go packages and host callback transport without new dependencies.
Keep protobuf metadata parsing and its fixtures in `internal/cursor`, with native
dispatch coverage in `cmd/cpa-cursor` and pinned-host checks in `tests/host`.
Use the repository's Go formatting and naming conventions, without new explanatory
code comments or lint suppressions.

Add failing fixtures for optional-field presence, upstream-ID matching, conservative
family aggregation, exact reasoning levels, existing model-list preservation and
provider isolation, then implement each path. Run focused `go test` commands during
development, `mise run check` and `mise run test-host` before completion, and build
the arm64 artefact separately if requested for deployment.

## boundaries

Always preserve credential validation, limits, namespaces and no-network list
interception. Never guess context or output limits, use live credentials, modify
other projects or deploy without a separate request. Ask before adding metadata
sources, dependencies or changing request execution behaviour.

## verified wire fields

Source: the official Cursor CLI `2026.10.01-e373342`,
<https://downloads.cursor.com/lab/2026.10.01-e373342/linux/x64/agent-cli-package.tar.gz>,
`dist-package/index.js`, the `AvailableModelsResponse.AvailableModel` descriptor.

| protobuf field | meaning |
| --- | --- |
| 1 | model name |
| 5 | optional agent support |
| 10 | optional image support |
| 15 | optional default context-token limit |
| 16 | optional max-mode context-token limit |
| 18 | optional server model name |
| 30 | variants |
| 36 | legacy slugs |
| 37 | ID aliases |

Variant field 11 is an optional legacy slug, while field 3 marks max mode. Field 9
is a representation string, not an authoritative routing ID, and is not used for
matching. The descriptor does not expose an output-token limit, so none is invented.

Text output and client MCP tool handling are implemented by this executor, but
tool support is advertised only when Cursor reports agent support. Named reasoning
controls come from usable variants. A discovered `-thinking` variant maps to the
host-supported `high` control. A native thinking marker without selectable variants
does not invent levels or a toggle. A bare family ID alone is its default effort,
not proof that reasoning can be disabled. A distinct bare/thinking pair supplies
that toggle only when the bare model has no native thinking marker.

The pinned host's `sdk/api/handlers/openai/openai_handlers.go` deliberately reduces
standard lists to identity fields. `response.intercept_after` restores metadata on
known namespaced Cursor rows, using bounded per-account snapshots. Existing fields,
including explicit false values and empty reasoning controls, remain authoritative.
Snapshots are replaced on successful discovery, discarded on reload/shutdown and
never persisted with credentials.

## verification record

Focused fixtures were observed failing before their implementations, including
family matching, model-list restoration, alias precedence, explicit metadata
preservation and default-effort toggle semantics. Reversing the provider-owner
guard makes the provider-isolation fixture fail.

`mise run check` passes pure Go tests, race tests, static analysis, formatting,
helper syntax, the shared-library build and native ABI smoke. The Cursor package
reached 86.4% statement coverage in the metadata verification run.

`mise run test-host` verifies real plugin discovery metadata through the pinned
host registry prefixing and actual OpenAI model-list handler, then restores the
stripped fields through the plugin interceptor. Live account behaviour and visual
picker rendering are not claimed, and nothing was deployed by this change.
