# verified model metadata

Implement the approved contract in `docs/model-metadata.md` without deployment.

## decisions

Use Cursor's authenticated metadata and the executor's verified capabilities, never
model-name guesses or another provider's limits. Restore metadata stripped by the
host only for known namespaced Cursor rows, with no network calls on list requests.

## slices

| slice | acceptance | verification |
| --- | --- | --- |
| discovery | match upstream family and variant metadata without inventing limits or image support | optional catalogue and malformed protobuf fixtures |
| reasoning | expose only discovered reasoning controls without changing execution fallback | variant tests |
| presentation | enrich existing Cursor model-list rows and preserve other providers | list interception and native dispatch fixtures |
| host contract | preserve metadata through prefixing and host list serialisation | pinned-host fixture |

## final gate

Review the complete diff, then run `mise run check` and `mise run test-host` without
concurrent source edits.

No remote publish, deployment or live credential use is required.
