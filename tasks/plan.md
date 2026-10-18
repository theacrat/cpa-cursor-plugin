# minimal model labels

Use only the model name when it is unique and add the minimum needed to distinguish
duplicate names. All deployments are on hold.

## decisions

Remove recognised effort, context-size, provider and NO ZDR annotations from base
labels. Inspect the returned catalogue before adding Fast, Cursor or route-ID
qualifiers, only while duplicates remain. Keep custom names, routing, model IDs and
structured metadata intact. Do not modify another provider's rows.

## slices

| slice | acceptance | verification |
| --- | --- | --- |
| base labels | unique names contain no recognised metadata decorations | family-label fixtures |
| duplicate names | only visible collisions get the minimum qualifiers, removed when the duplicate disappears | OpenAI and Codex catalogue fixtures |
| host contract | metadata and provider isolation survive host serialisation | pinned-host fixture |

## final gate

Review the complete diff, then run `mise run check` and `mise run test-host` without
concurrent source edits.

Do not connect to the deployment host, upload artefacts or restart services.
