"""Inspect private candidate archives; this does not qualify published versions."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import sys
import zipfile

sys.dont_write_bytecode = True
from candidate_inputs import verify, write_new


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--lane", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--root", type=Path, default=Path("C:/Demiurgos/navmesh-validation/world-test"))
    args = parser.parse_args()
    lane, output, root = args.lane.resolve(), args.out.resolve(), args.root.resolve()
    if not lane.is_relative_to(root / "workspaces") or not output.is_relative_to(root / "runs") or output.exists():
        parser.error("use a staged candidate and new evidence path inside the validation workspace")
    manifest = json.loads((lane / "manifest.json").read_text())
    before = verify(lane)
    records = []
    for module in manifest["modules"]:
        directory = lane / "proxy" / module["module"] / "@v"
        archive_path = directory / (module["version"] + ".zip")
        prefix = module["module"] + "@" + module["version"] + "/"
        fixtures, errors = [], []
        with zipfile.ZipFile(archive_path) as archive:
            names = archive.namelist()
            for required in ("LICENSE", "go.mod"):
                if prefix + required not in names:
                    errors.append("missing " + required)
            for name in names:
                relative = name.removeprefix(prefix)
                if relative in ("go.work", "go.work.sum"):
                    errors.append("workspace file included: " + relative)
                if "/testdata/" in "/" + relative or relative.startswith("qualification/configs/"):
                    data = archive.read(name)
                    pointer = data.startswith(b"version https://git-lfs.github.com/spec/v1")
                    fixtures.append({"path": relative, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(), "lfs_pointer": pointer})
                    if pointer:
                        errors.append("required fixture is an LFS pointer: " + relative)
            if prefix + "go.mod" in names:
                archived_mod = archive.read(prefix + "go.mod")
                if archived_mod != (directory / (module["version"] + ".mod")).read_bytes():
                    errors.append("proxy module metadata differs from archived go.mod")
        records.append({"module": module["module"], "version": module["version"], "archive": str(archive_path),
                        "sha256": digest(archive_path), "file_count": len(names), "fixtures": fixtures, "errors": errors,
                        "proxy_files": [{"path": str(path), "sha256": digest(path), "bytes": path.stat().st_size}
                                        for path in sorted(directory.iterdir()) if path.is_file()]})
    after = verify(lane)
    passed = before["pass"] and after["pass"] and all(not record["errors"] for record in records)
    result = {"utc": datetime.datetime.now(datetime.timezone.utc).isoformat(), "candidate": str(lane),
              "manifest_sha256": digest(lane / "manifest.json"), "harness": str(Path(__file__).resolve()),
              "harness_sha256": digest(Path(__file__)), "arguments": sys.argv[1:], "modules": records,
              "source_before": before, "source_after": after, "pass": passed,
              "scope": "Private candidate archive contents and metadata. Published checksums and published consumer resolution remain pending. GOWORK=off builds and tests are separate platform-matrix evidence."}
    output.parent.mkdir(parents=True, exist_ok=True)
    write_new(output, result)
    print(output)
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
