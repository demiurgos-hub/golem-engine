"""Index immutable qualification evidence without changing its recorded results."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path("C:/Demiurgos/navmesh-validation/world-test"))
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, action="append", required=True)
    parser.add_argument("--exclusion", action="append", default=[])
    args = parser.parse_args()
    root, output = args.root.resolve(), args.output.resolve()
    if not output.is_relative_to(root) or output.exists():
        parser.error("output must be a new file below the validation root")
    record = {
        "created_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "scope": "Evidence index; individual records retain their pass/fail/unknown outcomes and exact candidate identities.",
        "publication": "Not performed. Candidate proxies do not establish published dependency resolution.",
        "exclusions": args.exclusion,
        "evidence": [],
    }
    for source in args.evidence:
        source = source.resolve()
        if not source.is_relative_to(root) or not source.exists():
            parser.error("evidence must exist below the validation root: " + str(source))
        files = [source] if source.is_file() else sorted(p for p in source.rglob("*") if p.is_file())
        entry = {"path": str(source), "files": []}
        for path in files:
            # Old qualification lanes may keep caches next to logs. Their
            # contents are reproducible intermediates, not result evidence.
            relative = path.relative_to(source) if source.is_dir() else Path(path.name)
            if any(part in {"temp", "build-cache", "module-cache", "__pycache__"} for part in relative.parts):
                continue
            entry["files"].append({"path": str(path), "sha256": digest(path), "bytes": path.stat().st_size})
        record["evidence"].append(entry)
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("x", encoding="utf-8") as stream:
        json.dump(record, stream, indent=2)
        stream.write("\n")
    print(output)


if __name__ == "__main__":
    main()
