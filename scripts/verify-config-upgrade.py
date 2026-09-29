#!/usr/bin/env python3
"""Run actual historical config readers/writers against disposable fixtures."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
probe = repo / "scripts/fixtures/config_compat_probe_test.go.txt"
with tempfile.TemporaryDirectory(prefix="reasonix-config-compat-") as temp:
    base = Path(temp)
    home, project = base / "home", base / "project"
    home.mkdir()
    project.mkdir()
    (home / "config.toml").write_text("# user source preserved\n[permissions]\nallow=['Bash=echo trusted']\n", encoding="utf-8")
    (home / "project-grants.json").write_text('{"version":1,"workspaces":{}}\n', encoding="utf-8")
    (project / "reasonix.toml").write_text("# legacy source preserved\n[permissions]\nallow=['Bash=echo old-declaration']\n[model_roles]\nanswerer='unchanged'\n[future_setting]\nkeep='unknown value'\n", encoding="utf-8")
    env = {**os.environ, "REASONIX_COMPAT_HOME": str(home), "REASONIX_COMPAT_ROOT": str(project)}

    def run(checkout, *, current=False, write=False):
        overlay = base / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(checkout / "internal/config/upgrade_compat_probe_test.go"): str(probe)}}))
        options = ["-overlay", str(overlay)] if current else []
        if not current:
            (checkout / "internal/config/upgrade_compat_probe_test.go").write_bytes(probe.read_bytes())
        result = subprocess.run(["go", "test", *options, "./internal/config", "-run", "^TestUpgradeCompatibilityProbe$", "-count=1", "-v"], cwd=checkout,
                       env={**env, "REASONIX_COMPAT_CURRENT": str(int(current)), "REASONIX_COMPAT_WRITE": str(int(write))}, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        print(result.stdout, end="", flush=True)
        result.check_returncode()
        if "--- PASS: TestUpgradeCompatibilityProbe" not in result.stdout:
            raise RuntimeError("compatibility probe was not executed")

    run(repo, current=True)
    for ref in ("v1.38.3", "v1.39.5"):
        checkout = base / ref
        checkout.mkdir()
        archive = subprocess.check_output(["git", "archive", ref], cwd=repo)
        subprocess.run(["tar", "-xf", "-", "-C", str(checkout)], input=archive, check=True)
        print(f"VERIFY {ref}: old reader, old writer, current reader, downgrade reader", flush=True)
        run(checkout)
        run(checkout, write=True)
        run(repo, current=True)
        run(checkout)
    print("PASS actual v1.38.3 and v1.39.5 read/write/upgrade/downgrade; current read preserves bytes and mtimes", flush=True)
