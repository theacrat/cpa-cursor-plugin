# client tool and session compatibility

Investigate the linked Grok tool-selection and model-switch/compaction failures,
fix reproduced bridge defects, then independently review all session changes from
`53ff244` and repeat fixes and review until no blocking findings remain.

## decisions

Only registered client MCP operations may leave the bridge. Native Cursor file,
shell, network and subagent operations must stay rejected, with truthful typed
errors and actionable catalogue guidance rather than ambiguous tool absence.
Keep control-plane catalogue replies distinct from executable local operations.

Separate actual quota/rate-limit failures from protocol failures. Never fabricate
permission or quota, silently change providers or retry an unavailable operation
without a bound. Preserve complete valid history across model changes and tool-free
summary requests. Keep deployment on hold and leave other repositories untouched.

## slices

| slice | acceptance | verification |
| --- | --- | --- |
| tool surface | catalogue and refusals explain registered tools accurately, aliases map without collisions, disabled native retries are bounded | request and stream protocol fixtures |
| session compatibility | tool-free compaction and switched-model transcripts remain valid without losing messages or hiding upstream errors | history and status fixtures |
| independent review | all changes since `53ff244` reviewed for correctness, security and structure, with findings fixed and re-reviewed | separate read-only reviewers and final exact-SHA gates |

## final gate

Review the complete diff, then run `mise run check` and `mise run test-host` without
concurrent source edits.

Do not connect to the deployment host, use live credentials, upload artefacts or
restart services. Launch read-only review agents in the background, with no nested
delegation or writes to the shared checkout.
