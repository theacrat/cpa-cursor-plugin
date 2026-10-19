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
    source = host / "internal/runtime/executor/helps/retained_http_log.go"
    original = source.read_text()
    guard = "if !l.closed && l.full && l.responses != nil {"
    if original.count(guard) != 1:
        raise RuntimeError("retained audit mutation target changed")
    with tempfile.TemporaryDirectory(prefix="retained-audit-mutation-", dir=scratch) as directory:
        temporary = Path(directory)
        mutated = temporary / source.name
        mutated.write_text(original.replace(guard, "if !l.closed && l.responses != nil {"))
        overlay = temporary / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(mutated)}}))
        result = subprocess.run(
            ["go", "test", "-overlay", str(overlay), "./internal/runtime/executor/helps", "-run",
             "^TestRetainedHTTPLogErrorOnlyDoesNotSpoolResponsePayloads$", "-count=1", "-timeout=30s"],
            cwd=host, capture_output=True, text=True, check=False,
        )
        output = result.stdout + result.stderr
        if result.returncode == 0 or "error-only policy spooled response payloads" not in output:
            raise RuntimeError(f"retained audit mutation survived or failed unexpectedly\n{output}")
        print("caught: error-only retained response capture")


if __name__ == "__main__":
    main()
