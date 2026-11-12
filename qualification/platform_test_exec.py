"""Retain and execute the exact package binary supplied by `go test -exec`.

This is a companion to platform_matrix.py, not a test selector. The original
working directory, arguments, environment and exit code are preserved.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import time


def canonical(path):
    resolved = str(Path(path).resolve())
    # Windows may return the same resolved NTFS path with an extended-length
    # prefix on only one side of a comparison. Normalize after resolving links.
    if os.name == "nt":
        if resolved.startswith("\\\\?\\UNC\\"):
            resolved = "\\\\" + resolved[8:]
        elif resolved.startswith("\\\\?\\") and len(resolved) > 5 and resolved[5] == ":":
            resolved = resolved[4:]
    return Path(resolved)


def confined(path, root):
    path, root = canonical(path), canonical(root)
    if path != root and root not in path.parents:
        raise ValueError(f"path escapes validation workspace: {path}")
    return path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--validation-root", required=True)
    parser.add_argument("--bin-root", required=True)
    parser.add_argument("--source-root", required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("go test must supply a test binary")
    source = confined(Path.cwd(), args.source_root)
    binary_root = confined(args.bin_root, args.validation_root)
    relative = source.relative_to(canonical(args.source_root))
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
    destination = confined(binary_root / relative / f"{stamp}-{os.getpid()}", binary_root)
    destination.mkdir(parents=True, exist_ok=False)
    original = Path(command[0]).resolve()
    retained = destination / original.name
    shutil.copy2(original, retained)
    digest = hashlib.sha256(retained.read_bytes()).hexdigest()
    actual = [str(retained), *command[1:]]
    evidence = {
        "started_utc": stamp,
        "cwd": str(source),
        "original_argv": command,
        "executed_argv": actual,
        "binary_sha256": digest,
        "status": "running",
    }
    record = destination / "execution.json"
    record.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
    start = time.monotonic()
    try:
        result = subprocess.run(actual, check=False)
        evidence.update(status="passed" if result.returncode == 0 else "failed", exit_code=result.returncode)
        return result.returncode
    except BaseException as error:
        evidence.update(status="interrupted", error=repr(error))
        raise
    finally:
        evidence["seconds"] = time.monotonic() - start
        record.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    raise SystemExit(main())
