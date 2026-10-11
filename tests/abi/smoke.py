"""Validate the small, network-free native ABI lifecycle smoke test."""

import json
import subprocess
import sys


def main() -> None:
    host, library = sys.argv[1:]
    try:
        result = subprocess.run([host, library], capture_output=True, text=True,
                                timeout=30, check=True)
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        print(error.stdout or "", file=sys.stderr)
        print(error.stderr or "", file=sys.stderr)
        raise
    records = [json.loads(line) for line in result.stdout.splitlines()]
    assert [record["method"] for record in records] == [
        "plugin.register", "auth.identifier", "plugin.quiesce",
        "plugin.reconfigure", "plugin.shutdown",
    ], records
    assert all(record["code"] == 0 for record in records), records
    responses = {record["method"]: record["response"] for record in records}
    for method, envelope in responses.items():
        assert envelope["ok"] is True, (method, envelope)
        assert "result" in envelope and "error" not in envelope, envelope
    assert responses["auth.identifier"]["result"]["identifier"] == "cursor"
    for method in ("plugin.register", "plugin.reconfigure"):
        registration = responses[method]["result"]
        assert registration["schema_version"] == 6, registration
        assert registration["metadata"]["Name"] == "cpa-cursor", registration
        assert registration["capabilities"]["auth_provider"] is True, registration
    for method in ("plugin.quiesce", "plugin.shutdown"):
        assert responses[method]["result"] == {}, responses[method]
    print("native ABI smoke passed (register, cursor auth identifier, lifecycle)")


if __name__ == "__main__":
    main()
