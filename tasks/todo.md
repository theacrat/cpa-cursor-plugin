# client tool and session compatibility tasks

- [x] inspect both linked sessions and distinguish actual errors from model claims.
- [x] reproduce and repair client-tool catalogue and disabled-native dispatch defects.
- [x] verify actual user-ended summaries and preserve model-switch history and statuses.
- [x] review every change since `53ff244` independently and loop corrective fixes.
- [x] retain rerunnable verification evidence without deployment.

Final handoff requires `mise run check` and `mise run test-host` at the committed
HEAD with no concurrent tracked edits. The exact SHA and results are recorded in
`.scratch/review-loop.md`. Live account behaviour and the external opencode-go 403
remain unverified, not reported as fixed.
