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
        "plugin.register", "auth.identifier", "quota.identifier", "quota.describe",
        "plugin.quiesce", "plugin.reconfigure", "quota.fetch", "plugin.reconfigure",
        "quota.fetch", "quota.reset", "plugin.shutdown",
    ], records
    assert all(record["code"] == 0 for record in records), records
    disabled_fetch = records[6]["response"]
    assert disabled_fetch["ok"] is False, disabled_fetch
    assert disabled_fetch["error"]["http_status"] == 503, disabled_fetch
    responses = {record["method"]: record["response"] for record in records}
    for method, envelope in responses.items():
        assert envelope["ok"] is True, (method, envelope)
        assert "result" in envelope and "error" not in envelope, envelope
    assert responses["auth.identifier"]["result"]["identifier"] == "cursor"
    assert responses["quota.identifier"]["result"]["identifier"] == "cursor"
    quota_description = responses["quota.describe"]["result"]
    assert quota_description["supported_providers"] == ["cursor"], quota_description
    assert not quota_description.get("supports_reset", False), quota_description
    quota = responses["quota.fetch"]["result"]
    buckets = [bucket for group in quota["groups"] for bucket in group["buckets"]]
    assert any(bucket["remainingFraction"] == 0.75 for bucket in buckets), quota
    assert responses["quota.reset"]["result"]["success"] is False
    for method in ("plugin.register", "plugin.reconfigure"):
        registration = responses[method]["result"]
        assert registration["schema_version"] == 6, registration
        assert registration["metadata"]["Name"] == "cpa-cursor", registration
        assert registration["capabilities"]["auth_provider"] is True, registration
        assert registration["capabilities"]["quota_provider"] is True, registration
    for method in ("plugin.quiesce", "plugin.shutdown"):
        assert responses[method]["result"] == {}, responses[method]
    assert "fixture-access" not in result.stdout
    print("native ABI smoke passed (registration, auth, quota fetch/reset, lifecycle)")


if __name__ == "__main__":
    main()
