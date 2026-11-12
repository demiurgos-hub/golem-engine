"""Record verified source identity across staged qualification candidates.

This attributes existing checks to exact bytes; it does not run tests or turn
fixture, runner, tooling, or documentation changes into a runtime qualification.
Unknown source paths and dependency metadata are conservatively production.
"""
import argparse
from collections import Counter
import datetime
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
from candidate_inputs import digest, verify, write_new


PRODUCTION = {"core-library", "runtime", "shared-nav", "adapter", "dependency-metadata", "other-production"}


def category(repository, path):
    name = Path(path).name
    if Path(path).suffix in (".md", ".mdc") or name.startswith(("LICENSE", "NOTICE")):
        return "docs"
    if path.startswith(("qualification/worldgen/", "qualification/configs/")) or "/testdata/" in "/" + path:
        return "fixtures"
    if path.startswith("examples/navigation-stress/"):
        return "runner"
    if path.startswith("qualification/"):
        return "tooling"
    if name.endswith(("_test.go", "_test.py")):
        return "tests"
    if name in ("go.mod", "go.sum", "go.work", "go.work.sum", "package.json", "package-lock.json"):
        return "dependency-metadata"
    if repository == "navmesh-go":
        return "core-library"
    if repository == "golem-engine":
        if path.startswith("golem/nav/navmesh/"):
            return "adapter"
        if path.startswith("golem/nav/"):
            return "shared-nav"
        if path.startswith("golem/"):
            return "runtime"
    return "other-production"


def source_files(manifest):
    return {(repository, entry["path"]): entry["sha256"]
            for repository, source in manifest["sources"].items()
            for entry in source["files"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--stage", type=Path, action="append", required=True, help="repeat in chronological order")
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--root", type=Path, default=Path("C:/Demiurgos/navmesh-validation/world-test"))
    args = parser.parse_args()
    root, output = args.root.resolve(), args.out.resolve()
    stages = [stage.resolve() for stage in args.stage]
    if len(stages) < 2 or len(set(stages)) != len(stages):
        parser.error("provide at least two different stages")
    if any(not stage.is_relative_to(root / "workspaces") for stage in stages) or not output.is_relative_to(root / "runs") or output.exists():
        parser.error("use staged candidates and a new evidence path inside the validation workspace")
    records, files = [], []
    for stage in stages:
        integrity = verify(stage)
        records.append({"stage": str(stage), "integrity_before": integrity})
        files.append(source_files(json.loads((stage / "manifest.json").read_text())))
    transitions = []
    for index in range(1, len(stages)):
        old, new = files[index - 1], files[index]
        changes = []
        for repository, path in sorted(old.keys() | new.keys()):
            before, after = old.get((repository, path)), new.get((repository, path))
            if before != after:
                group = category(repository, path)
                changes.append({"repository": repository, "path": path, "category": group,
                                "test_file": path.endswith(("_test.go", "_test.py")),
                                "change": "added" if before is None else "removed" if after is None else "modified",
                                "before_sha256": before, "after_sha256": after})
        production = [change for change in changes if change["category"] in PRODUCTION]
        transitions.append({"from": str(stages[index - 1]), "to": str(stages[index]),
                            "category_counts": dict(sorted(Counter(change["category"] for change in changes).items())),
                            "production_unchanged": not production, "production_changes": production, "changes": changes})
    for record, stage in zip(records, stages):
        record["integrity_after"] = verify(stage)
    passed = all(record["integrity_before"]["pass"] and record["integrity_after"]["pass"] for record in records)
    result = {"utc": datetime.datetime.now(datetime.timezone.utc).isoformat(), "arguments": sys.argv[1:],
              "harness": str(Path(__file__).resolve()), "harness_sha256": digest(__file__),
              "verifier_sha256": digest(Path(__file__).with_name("candidate_inputs.py")), "stages": records,
              "transitions": transitions, "production_unchanged_throughout": all(item["production_unchanged"] for item in transitions),
              "pass": passed, "scope": __doc__}
    output.parent.mkdir(parents=True, exist_ok=True)
    write_new(output, result)
    print(output)
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
