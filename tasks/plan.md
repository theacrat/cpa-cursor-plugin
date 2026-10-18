# model labels and fast tiers

Fix misleading effort-labelled model names across every Cursor model family,
expose discovered fast tiers separately and deploy to `root@192.168.1.1`.

## decisions

Keep existing standard route IDs, suffix fast routes with `-fast`, and keep low/high
in each route's reasoning selector. Clean only recognised trailing effort/speed
labels while retaining model identity and annotations. Add `(Cursor)` to provider
labels, preserve custom names and leave other providers untouched.

## slices

| slice | acceptance | verification |
| --- | --- | --- |
| grouping | standard and fast tiers keep distinct upstream IDs and effort tables | variant and metadata tests |
| labels | all families have neutral effort-independent names and explicit provider/speed labels | fixture table and model-list tests |
| deployment | arm64 library loads, live labels are clean and provider routes stay separate | native ABI fixture and authenticated catalogue checks |

## final gate

Review the complete diff, then run `mise run check` and `mise run test-host` without
concurrent source edits.

No repository publication is required. Deploy the verified arm64 build without
configuration changes or retained backups, then remove temporary remote files.
