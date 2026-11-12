"""Stage exact working-source snapshots and a private candidate Go module proxy.

This never tags, pushes, edits source manifests, or claims published resolution.
All generated content is confined to the explicitly supplied validation root.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import zipfile


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args])


def stage(repo, dest):
    names = sorted(set(git(repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard").decode().split("\0")) - {""})
    files = []
    for name in names:
        if name in ("go.work", "go.work.sum"):
            continue
        src = repo / name
        if not src.is_file():
            continue
        target = dest / name
        target.parent.mkdir(parents=True, exist_ok=True)
        data = src.read_bytes()
        target.write_bytes(data)
        files.append({"path": name, "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data)})
    digest = hashlib.sha256(json.dumps(files, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return {"origin": str(repo), "head": git(repo, "rev-parse", "HEAD").decode().strip(), "status": git(repo, "status", "--short").decode(), "sha256": digest, "files": files}


def module_proxy(source, module, version, proxy, repository_root):
    target = proxy / module / "@v"
    target.mkdir(parents=True, exist_ok=True)
    nested = [p.parent for p in source.rglob("go.mod") if p.parent != source]
    files = [p for p in source.rglob("*") if p.is_file() and not any(root == p.parent or root in p.parents for root in nested)]
    prefix = module + "@" + version + "/"
    zip_path = target / (version + ".zip")
    inherited_license = None
    with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for p in sorted(files):
            relative = p.relative_to(source).as_posix()
            if relative in ("go.work", "go.work.sum") or relative.startswith(".git/"):
                continue
            info = zipfile.ZipInfo(prefix + relative, (2000, 1, 1, 0, 0, 0))
            info.external_attr = 0o644 << 16
            archive.writestr(info, p.read_bytes(), compress_type=zipfile.ZIP_DEFLATED)
        if not (source / "LICENSE").is_file():
            ancestor = source.parent
            while ancestor == repository_root or repository_root in ancestor.parents:
                license_path = ancestor / "LICENSE"
                if license_path.is_file():
                    data = license_path.read_bytes()
                    info = zipfile.ZipInfo(prefix + "LICENSE", (2000, 1, 1, 0, 0, 0))
                    info.external_attr = 0o644 << 16
                    archive.writestr(info, data, compress_type=zipfile.ZIP_DEFLATED)
                    inherited_license = {"source": str(license_path), "sha256": hashlib.sha256(data).hexdigest()}
                    break
                ancestor = ancestor.parent
    shutil.copyfile(source / "go.mod", target / (version + ".mod"))
    (target / (version + ".info")).write_text(json.dumps({"Version": version, "Time": "2026-11-11T00:00:00Z"}))
    (target / "list").write_text(version + "\n")
    return {"module": module, "version": version, "zip_sha256": hashlib.sha256(zip_path.read_bytes()).hexdigest(), "inherited_license": inherited_license}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default="C:/Demiurgos/navmesh-validation/world-test")
    parser.add_argument("--library", default="C:/Demiurgos/navmesh-go")
    parser.add_argument("--engine", default="C:/Demiurgos/golem-engine")
    args = parser.parse_args()
    root = Path(args.root).resolve()
    for name in ("worlds", "workspaces", "bin", "scratch", "cache", "runs"):
        (root / name).mkdir(parents=True, exist_ok=True)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
    lane = root / "workspaces" / stamp
    lane.mkdir()
    manifest = {"created_utc": stamp, "qualification": "unpublished candidate archives; not published dependency qualification", "sources": {}, "modules": []}
    for name, repo in (("navmesh-go", Path(args.library)), ("golem-engine", Path(args.engine))):
        manifest["sources"][name] = stage(repo.resolve(), lane / name)
    for path, module, version in (("navmesh-go", "github.com/demiurgos-hub/navmesh-go", "v0.1.0"), ("golem-engine/golem/nav", "github.com/demiurgos-hub/golem-engine/golem/nav", "v0.4.0"), ("golem-engine/golem/nav/navmesh", "github.com/demiurgos-hub/golem-engine/golem/nav/navmesh", "v0.1.0"), ("golem-engine", "github.com/demiurgos-hub/golem-engine", "v0.4.0")):
        manifest["modules"].append(module_proxy(lane / path, module, version, lane / "proxy", lane / path.split("/")[0]))
    (lane / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    env = {"GOWORK": "off", "CGO_ENABLED": "0", "GOPROXY": (lane / "proxy").as_uri() + ",https://proxy.golang.org", "GONOSUMDB": "github.com/demiurgos-hub/navmesh-go,github.com/demiurgos-hub/golem-engine*", "GOMODCACHE": str(root / "cache" / stamp / "modules"), "GOCACHE": str(root / "cache" / "build"), "GOTMPDIR": str(root / "scratch" / stamp), "TEMP": str(root / "scratch" / stamp), "TMP": str(root / "scratch" / stamp)}
    Path(env["GOTMPDIR"]).mkdir(parents=True)
    (lane / "environment.json").write_text(json.dumps(env, indent=2) + "\n")
    print(lane)


if __name__ == "__main__":
    main()
