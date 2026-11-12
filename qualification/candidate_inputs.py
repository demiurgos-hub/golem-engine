"""Verify immutable candidates and isolate Go dependency resolution per run."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def write_new(path, value):
    with Path(path).open("x", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2)
        stream.write("\n")


def file_state(path):
    path = Path(path)
    return {"path": str(path), "exists": path.is_file(), "sha256": digest(path) if path.is_file() else None, "bytes": path.stat().st_size if path.is_file() else 0}


def verify(lane):
    lane = Path(lane).resolve()
    manifest = json.loads((lane / "manifest.json").read_text())
    errors, counts = [], {}
    for name, source in manifest["sources"].items():
        directory = lane / name
        expected = {item["path"]: item for item in source["files"]}
        counts[name] = len(expected)
        for relative, item in expected.items():
            path = directory / relative
            if not path.is_file():
                errors.append({"path": str(path), "reason": "missing manifested source"})
            elif path.stat().st_size != item["bytes"] or digest(path) != item["sha256"]:
                errors.append({"path": str(path), "reason": "manifested source bytes changed", "expected_sha256": item["sha256"], "actual_sha256": digest(path)})
        for path in directory.rglob("*"):
            if path.is_file() and path.relative_to(directory).as_posix() not in expected:
                errors.append({"path": str(path), "reason": "unmanifested source-tree file"})
    for module in manifest.get("modules", []):
        archive = lane / "proxy" / module["module"] / "@v" / (module["version"] + ".zip")
        if not archive.is_file() or digest(archive) != module["zip_sha256"]:
            errors.append({"path": str(archive), "reason": "candidate module archive changed or missing"})
    return {"lane": str(lane), "manifest_sha256": digest(lane / "manifest.json"), "verified_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(), "source_file_counts": counts, "scope": "Every original source file must match; extra source-tree files are rejected. Candidate proxy archive hashes are also checked.", "errors": errors, "pass": not errors}


def prepare(lane, scratch, modules):
    lane, scratch = Path(lane).resolve(), Path(scratch).resolve()
    scratch.mkdir(parents=True, exist_ok=False)
    results = {}
    for name, relative in modules:
        if not name or Path(name).name != name or name in results:
            raise ValueError("module names must be unique file basenames")
        source = (lane / relative).resolve()
        if not source.is_relative_to(lane):
            raise ValueError("module source must stay in the candidate")
        modfile, sumfile = scratch / (name + ".mod"), scratch / (name + ".sum")
        modfile.write_bytes((source / "go.mod").read_bytes())
        if (source / "go.sum").is_file():
            sumfile.write_bytes((source / "go.sum").read_bytes())
        results[name] = {"source_directory": str(source), "source_modfile": file_state(source / "go.mod"), "source_sumfile": file_state(source / "go.sum"), "modfile": file_state(modfile), "sumfile": file_state(sumfile)}
    return {"lane": str(lane), "scratch": str(scratch), "modules": results, "qualification": "Unpublished candidate dependency resolution; external modfile/sum updates are allowed, source updates are not."}


def record(before):
    return {"lane": before["lane"], "scratch": before["scratch"], "modules": {name: {"modfile": file_state(item["modfile"]["path"]), "sumfile": file_state(item["sumfile"]["path"])} for name, item in before["modules"].items()}}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("verify", "prepare", "record"))
    parser.add_argument("--lane", type=Path)
    parser.add_argument("--scratch", type=Path)
    parser.add_argument("--module", action="append", help="name=module/path/relative/to/lane")
    parser.add_argument("--input", type=Path)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    if args.action == "verify":
        if not args.lane:
            parser.error("verify requires --lane")
        result = verify(args.lane)
    elif args.action == "prepare":
        if not args.lane or not args.scratch:
            parser.error("prepare requires --lane and --scratch")
        modules = [value.split("=", 1) for value in (args.module or ["worldgen=navmesh-go", "stress=golem-engine/examples/navigation-stress"])]
        if any(len(value) != 2 for value in modules):
            parser.error("--module requires name=relative/path")
        result = prepare(args.lane, args.scratch, modules)
    else:
        if not args.input:
            parser.error("record requires --input")
        result = record(json.loads(args.input.read_text()))
    result["helper_sha256"] = digest(__file__)
    write_new(args.out, result)
    print(args.out)
    return 0 if result.get("pass", True) else 1


if __name__ == "__main__":
    raise SystemExit(main())
