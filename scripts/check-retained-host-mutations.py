import argparse
import json
from pathlib import Path
import subprocess
import tempfile


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("host", type=Path)
    arguments = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    host = arguments.host.resolve(strict=True)
    scratch = root / ".scratch"
    scratch.mkdir(exist_ok=True)
    cases = [
        (
            "internal/runtime/executor/helps/retained_http_log.go",
            "if !l.closed && l.full && l.responses != nil {",
            "if !l.closed && l.responses != nil {",
            "^TestRetainedHTTPLogErrorOnlyDoesNotSpoolResponsePayloads$",
            "error-only policy spooled response payloads",
        ),
        (
            "internal/logging/request_logger_home.go",
            "return cancellable.RPushRequestLogContext(ctx, raw)",
            "_ = cancellable\n\t\treturn client.RPushRequestLog(ctx, raw)",
            "^TestRetainedHTTPLogCloseContextCancelsProductionHomeSubmission$",
            "production Home I/O ignored retained lifecycle cancellation",
        ),
    ]
    for relative, guard, replacement, test, expected in cases:
        source = host / relative
        original = source.read_text()
        if original.count(guard) != 1:
            raise RuntimeError(f"retained audit mutation target changed: {relative}")
        with tempfile.TemporaryDirectory(prefix="retained-audit-mutation-", dir=scratch) as directory:
            temporary = Path(directory)
            mutated = temporary / source.name
            mutated.write_text(original.replace(guard, replacement))
            overlay = temporary / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(source): str(mutated)}}))
            result = subprocess.run(
                ["go", "test", "-overlay", str(overlay), "./internal/runtime/executor/helps", "-run",
                 test, "-count=1", "-timeout=30s"],
                cwd=host, capture_output=True, text=True, check=False,
            )
            output = result.stdout + result.stderr
            if result.returncode == 0 or expected not in output:
                raise RuntimeError(f"retained audit mutation survived or failed unexpectedly: {relative}\n{output}")
            print(f"caught: {test}")


if __name__ == "__main__":
    main()
