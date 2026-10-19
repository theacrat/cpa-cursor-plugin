import argparse
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

HOST_BASE = "5fc580b1d72d87a7bcfe5cfb6f479c1aea5625b3"


def git(directory: Path, *arguments: str) -> str:
    return subprocess.check_output(
        ["git", "-C", str(directory), *arguments], text=True,
    ).strip()


def snapshot(source: Path, target: Path, revision: str) -> None:
    archive = subprocess.check_output(["git", "-C", str(source), "archive", revision])
    target.mkdir()
    with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
        bundle.extractall(target, filter="data")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("host", type=Path)
    parser.add_argument("--plugin", type=Path)
    parser.add_argument("--expect-stateless", action="store_true")
    arguments = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    host_source = arguments.host.resolve(strict=True)
    plugin_source = (arguments.plugin or root).resolve(strict=True)
    host_revision = git(host_source, "rev-parse", "HEAD")
    plugin_revision = git(plugin_source, "rev-parse", "HEAD")
    subprocess.run(
        ["git", "-C", str(host_source), "merge-base", "--is-ancestor", HOST_BASE, host_revision],
        check=True,
    )
    scratch = root / ".scratch"
    scratch.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="live-native-host-", dir=scratch) as temporary:
        directory = Path(temporary)
        host = directory / "host"
        plugin = directory / "plugin"
        snapshot(host_source, host, host_revision)
        snapshot(plugin_source, plugin, plugin_revision)
        environment = os.environ.copy()
        environment.update({
            "CGO_ENABLED": "1", "TMPDIR": str(directory),
            "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local",
        })
        subprocess.run(["bash", "scripts/build.sh"], cwd=plugin, env=environment, check=True)
        operating_system = subprocess.check_output(["go", "env", "GOHOSTOS"], env=environment, text=True).strip()
        architecture = subprocess.check_output(["go", "env", "GOHOSTARCH"], env=environment, text=True).strip()
        extension = {"linux": "so", "darwin": "dylib"}.get(operating_system)
        if extension is None:
            raise SystemExit("native live-host fixture requires Linux or macOS")
        library = plugin / "dist" / f"{operating_system}-{architecture}" / f"cpa-cursor.{extension}"
        environment.update({
            "CPA_LIVE_PLUGIN_LIBRARY": str(library),
            "CPA_LIVE_FIXTURE_DIRECTORY": str(directory),
            "CPA_LIVE_EXPECT_STATELESS": "1" if arguments.expect_stateless else "0",
        })
        overlay = directory / "live-host-overlay.json"
        overlay.write_text(json.dumps({"Replace": {
            str(host / "internal/pluginhost/cursor_live_native_test.go"):
                str(plugin / "tests/host/live_loop_test.go.txt"),
        }}))
        print(f"live native fixture host={host_revision} plugin={plugin_revision}", flush=True)
        subprocess.run(
            ["go", "test", "-mod=readonly", "-overlay", str(overlay),
             "./internal/pluginhost", "-run", "^TestCursorLiveNativeToolLoop$",
             "-count=1", "-timeout", "90s", "-v"],
            cwd=host, env=environment, check=True,
        )


if __name__ == "__main__":
    main()
