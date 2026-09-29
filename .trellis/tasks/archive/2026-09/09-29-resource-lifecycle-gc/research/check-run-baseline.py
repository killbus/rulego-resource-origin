"""Extract an immutable baseline and run independent tests, preserving failures."""
from __future__ import annotations

import hashlib
import io
import json
import os
from pathlib import Path
import platform
import subprocess
import tarfile


ROOT = Path(__file__).resolve().parents[4]
EVIDENCE = Path(__file__).resolve().parent
BASELINE = "b22d4b8a08a8a46d3e01b5ced7a154642f710e5c"
SNAPSHOT = EVIDENCE / "check-baseline-snapshot"


def main() -> None:
    if SNAPSHOT.exists():
        raise RuntimeError(f"Refusing to overwrite previous snapshot: {SNAPSHOT}")
    archive = subprocess.run(
        ["git", "archive", "--format=tar", BASELINE],
        cwd=ROOT, check=True, capture_output=True,
    ).stdout
    SNAPSHOT.mkdir()
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:") as bundle:
        for member in bundle.getmembers():
            destination = (SNAPSHOT / member.name).resolve()
            if not destination.is_relative_to(SNAPSHOT.resolve()):
                raise RuntimeError(f"Unexpected archive path: {member.name}")
        bundle.extractall(SNAPSHOT, filter="data")
    independent = (ROOT / "lifecycle_independent_test.go").read_bytes()
    (SNAPSHOT / "lifecycle_independent_test.go").write_bytes(independent)
    env = os.environ.copy()
    # Keep path traversal and build-cache writes inside the authorized snapshot.
    # Do not patch production to work around sandbox path permissions.
    for variable, dirname in [("TEMP", ".test-temp"), ("TMP", ".test-temp"), ("GOCACHE", ".go-cache")]:
        location = SNAPSHOT / dirname
        location.mkdir(exist_ok=True)
        env[variable] = str(location)
    command = ["go", "test", "-count=1", "-v", "-run", "^TestIndependent", "./..."]
    metadata = {
        "baseline": BASELINE,
        "snapshot": str(SNAPSHOT),
        "command": command,
        "platform": platform.platform(),
        "go_version": subprocess.run(["go", "version"], capture_output=True, text=True, check=True).stdout.strip(),
        "go_env": subprocess.run(["go", "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "GOMODCACHE", "GOCACHE"], env=env, capture_output=True, text=True, check=True).stdout,
        "environment_overrides": {key: env[key] for key in ["TEMP", "TMP", "GOCACHE"]},
        "independent_test_sha256": hashlib.sha256(independent).hexdigest(),
        "archive_sha256": hashlib.sha256(archive).hexdigest(),
    }
    (EVIDENCE / "check-baseline-environment.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
    print(f"Executing {command!r} in immutable snapshot {SNAPSHOT}", flush=True)
    result = subprocess.run(command, cwd=SNAPSHOT, env=env, capture_output=True, text=True)
    (EVIDENCE / "check-baseline-unit.log").write_text(result.stdout + result.stderr, encoding="utf-8")
    metadata["unit_exit_code"] = result.returncode
    (EVIDENCE / "check-baseline-environment.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
    print(result.stdout + result.stderr, flush=True)
    print(f"Independent baseline exit code: {result.returncode}", flush=True)


if __name__ == "__main__":
    main()
