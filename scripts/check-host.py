import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def run(command: list[str], directory: Path, environment: dict[str, str]) -> None:
    subprocess.run(command, cwd=directory, env=environment, check=True)


def main() -> None:
    root = Path(__file__).resolve().parent.parent
    scratch = root / ".scratch"
    scratch.mkdir(exist_ok=True)
    environment = os.environ.copy()
    environment["CGO_ENABLED"] = "0"
    module = json.loads(subprocess.check_output(
        ["go", "list", "-m", "-json", "github.com/router-for-me/CLIProxyAPI/v7"],
        cwd=root, env=environment, text=True,
    ))
    with tempfile.TemporaryDirectory(prefix="host-contract-", dir=scratch) as temporary:
        directory = Path(temporary)
        host = directory / "host"
        shutil.copytree(module["Dir"], host)
        for path, _, _ in os.walk(host):
            Path(path).chmod(0o700)
        environment["CPA_HOST_FIXTURE"] = str(directory / "auth.json")
        environment["CPA_THINKING_FIXTURE"] = str(directory / "thinking.json")
        replacements = {
            host / "sdk/cliproxy/auth/cursor_contract_test.go": root / "tests/host/refresh_test.go.txt",
            host / "sdk/cliproxy/cursor_contract_test.go": root / "tests/host/models_test.go.txt",
            host / "internal/translator/openai/claude/cursor_contract_test.go": root / "tests/host/thinking_test.go.txt",
        }
        overlay = directory / "host-overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(key): str(value) for key, value in replacements.items()}}))
        run(["go", "test", "-mod=mod", "-overlay", str(overlay), "./internal/translator/openai/claude", "-run", "^TestCursorNativeThinkingTranslation$", "-count=1"], host, environment)
        plugin_overlay = directory / "plugin-overlay.json"
        plugin_overlay.write_text(json.dumps({"Replace": {str(root / "internal/cursor/host_contract_test.go"): str(root / "tests/host/export_test.go.txt")}}))
        run(["go", "test", "-overlay", str(plugin_overlay), "./internal/cursor", "-run", "^TestExportHostFixture$", "-count=1"], root, environment)
        run(["go", "test", "-mod=mod", "-overlay", str(overlay), "./sdk/cliproxy/auth", "./sdk/cliproxy", "-run", "^TestCursor(PluginRefreshPolicy|PrefixKeepsNativeProvidersSeparate)$", "-count=1", "-timeout", "60s"], host, environment)


if __name__ == "__main__":
    main()
