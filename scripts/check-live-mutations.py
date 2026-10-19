import json
from pathlib import Path
import subprocess
import tempfile


def main() -> None:
    root = Path(__file__).resolve().parent.parent
    scratch = root / ".scratch"
    scratch.mkdir(exist_ok=True)
    cases = [
        (
            "internal/cursor/live_progress.go",
            "if p.thinkingBytes > p.maxThinking {",
            "if p.thinkingBytes >= p.maxThinking {",
            "^TestLiveProgressRepeatedPrefixesCannotExtendRecoveryForever$",
            "replayed reasoning prefix refreshed the deadline",
        ),
        (
            "internal/cursor/live_history.go",
            "if index == len(expected)-1 && len(got.ToolCalls) == 0 {",
            "if len(got.ToolCalls) == 0 {",
            "^TestLiveResultMessagesRejectIncompleteForeignDuplicateAndSteeredBatches/removed_earlier_calls$",
            "invalid result association did not fail safely",
        ),
        (
            "internal/cursor/live_identity.go",
            'singleIdentityHeader(req.Headers, "X-Opencode-Session-Id")',
            'singleIdentityHeader(req.Headers, "X-Opencode-Session")',
            "^TestLiveToolLoopIdentityUsesActualSessionAndCaller$",
            "affinity changed actual-session ownership",
        ),
        (
            "internal/cursor/live_loop.go",
            "if pending.exec.Id == exec.Id && pending.exec.ExecId == exec.ExecId {",
            "if false && pending.exec.Id == exec.Id && pending.exec.ExecId == exec.ExecId {",
            "^TestLiveToolLoopRejectsDuplicateExecutionIdentity$",
            "duplicate execution identity escaped as client calls",
        ),
    ]
    for relative, original, replacement, test, expected in cases:
        source = root / relative
        text = source.read_text()
        if text.count(original) != 1:
            raise RuntimeError(f"mutation target changed: {relative}")
        with tempfile.TemporaryDirectory(prefix="live-mutation-", dir=scratch) as directory:
            temporary = Path(directory)
            mutated = temporary / source.name
            mutated.write_text(text.replace(original, replacement))
            overlay = temporary / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(source): str(mutated)}}))
            result = subprocess.run(
                ["go", "test", "-overlay", str(overlay), "./internal/cursor", "-run", test, "-count=1", "-timeout", "15s"],
                cwd=root, capture_output=True, text=True, check=False,
            )
            output = result.stdout + result.stderr
            if result.returncode == 0 or expected not in output:
                raise RuntimeError(f"mutation survived or failed unexpectedly: {relative}\n{output}")
            print(f"caught: {test}")


if __name__ == "__main__":
    main()
