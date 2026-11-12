"""Copy an audited server-prefab catalog into an isolated Unity qualification project.

The catalog list is an input manifest, not a game discovery mechanism. This script
preserves prefab YAML/removals and original GUID metadata; it never imports game
scripts or changes the source project. Resolution and state assertions happen in
GolemPhysicsCatalogFixture, in batches of at most sixteen roots.
"""

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

sys.dont_write_bytecode = True
from filter_prefab import HEADER, POINTER

ALLOWED = Path("D:/Demiurgos/tests/physics-validation")
CATALOGS = {"deco": "list_WorldDecoTypes.asset", "harvest": "list_HarvestableTypes.asset", "building": "list_Buildings.asset", "blueprint": "list_Blueprints.asset"}
PRIORITY = [("deco", 95), ("deco", 77), ("harvest", 2), ("harvest", 32), ("building", 19), ("blueprint", 13), ("building", 24), ("building", 16)]
SECOND = [("deco", x) for x in (36, 53, 86, 96)] + [("harvest", x) for x in (38, 14, 20, 26)] + [("building", 18), ("blueprint", 12)]


def digest(data):
    return hashlib.sha256(data).hexdigest()


def save(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("x", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, ensure_ascii=False)
        stream.write("\n")


def records(text):
    headers = list(HEADER.finditer(text))
    return {h[2]: (int(h[1]), text[h.start():headers[i + 1].start() if i + 1 < len(headers) else len(text)]) for i, h in enumerate(headers)}


def identity(guid, file_id):
    return {"GUID": guid, "FileID": str(file_id)}


def dependencies(text):
    """Retain direct geometry references and serialized object-reference overrides."""
    found = set()
    for kind, block in records(text).values():
        if kind not in (1001, 64, 65, 135, 136, 143, 154):
            continue
        for match in re.finditer(r"(?m)^\s*m_(?:SourcePrefab|Mesh|Material|TerrainData):\s*(\{[^}]+\})", block):
            ptr = POINTER.fullmatch(match[1])
            if ptr and ptr[2] and not ptr[2].startswith("00000000"):
                found.add(ptr[2])
    # Overrides can replace a mesh/material/TerrainData without a direct field.
    for match in re.finditer(r"(?m)^\s+propertyPath: (m_Mesh|m_Material|m_TerrainData)\r?\n\s+value:[^\r\n]*\r?\n\s+objectReference: (\{[^}]+\})", text):
        ptr = POINTER.fullmatch(match[2])
        if ptr and ptr[2] and not ptr[2].startswith("00000000"):
            found.add(ptr[2])
    return found


def harvest_states(text, guid):
    blocks = [block for _, block in records(text).values() if "  fullStages:" in block and "  emptyStage:" in block]
    if not blocks:
        return []
    if len(blocks) != 1:
        raise ValueError("Ambiguous harvest state declaration")
    block = blocks[0]
    full = block.split("  fullStages:", 1)[1].split("  emptyStage:", 1)[0]
    empty = block.split("  emptyStage:", 1)[1]
    groups = re.findall(r"(?m)^  - isEmptyStage:.*?(?=^  - isEmptyStage:|\Z)", full, re.S)
    groups.append(empty)
    ids = [re.findall(r"(?m)^    - \{fileID: (-?\d+)\}", group) for group in groups]
    if not ids or not any(ids):
        raise ValueError("Unsupported harvest stage declaration")
    union = sorted({value for group in ids for value in group})
    return [{"Name": "empty" if i == len(ids) - 1 else "full-" + str(i), "Toggles": [{"Object": identity(guid, value), "Active": value in group} for value in union]} for i, group in enumerate(ids)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--audit", type=Path, required=True)
    parser.add_argument("--source-assets", type=Path, required=True)
    parser.add_argument("--project", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--scope", choices=("priority", "all"), default="priority")
    parser.add_argument("--exclusions", type=Path)
    parser.add_argument("--resume-staging", action="store_true", help="Verify identical copied bytes and finish an interrupted staging pass; never overwrite changed inputs.")
    args = parser.parse_args()
    assets, project, output = (x.resolve() for x in (args.source_assets, args.project, args.output))
    if not project.is_relative_to(ALLOWED.resolve()) or not output.is_relative_to(ALLOWED.resolve()) or output.exists():
        parser.error("Project/output must be isolated D validation paths; output must be new.")
    if not (project / "ProjectSettings/ProjectVersion.txt").exists():
        parser.error("Prepare the engine-owned Unity project first.")
    staged = project / "Assets/ResolvedCatalog"
    if staged.exists() and not args.resume_staging:
        parser.error("Use a fresh project/staged catalog; preserve prior inputs.")
    audit = json.loads(args.audit.read_text(encoding="utf-8-sig"))
    index = {}
    for meta in assets.rglob("*.meta"):
        match = re.search(r"(?m)^guid: ([0-9a-f]{32})\r?$", meta.read_text(encoding="utf-8-sig", errors="replace")[:512])
        if match:
            if match[1] in index:
                raise ValueError("Duplicate source GUID: " + match[1])
            index[match[1]] = meta.with_suffix("")
    reverse = {path: guid for guid, path in index.items()}
    selected = list(PRIORITY)
    if args.scope == "all":
        selected += [x for x in SECOND if x not in selected]
        selected += [(key, row["id"]) for key, name in CATALOGS.items() for row in audit["catalogs"][name] if (row.get("server") or row.get("otherPrefabs", {}).get("serverBlueprintPrefab")) and (key, row["id"]) not in selected]
    entries, pending, root_paths = [], [], {}
    for key, number in selected:
        row = next(x for x in audit["catalogs"][CATALOGS[key]] if x["id"] == number)
        relative = row.get("server") or row.get("otherPrefabs", {}).get("serverBlueprintPrefab")
        if not relative:
            raise ValueError("Selected server root is null: " + str((key, number)))
        path = (assets / relative).resolve()
        if not path.is_relative_to(assets):
            raise ValueError("Source escapes Assets")
        name = key + "-" + str(number)
        entry = {"Key": name, "Asset": "Assets/ResolvedCatalog/" + path.relative_to(assets).as_posix(), "SourceGUID": reverse[path], "States": [], "Absent": []}
        text = path.read_text(encoding="utf-8-sig")
        if key == "harvest":
            entry["States"] = harvest_states(text, reverse[path])
        # Only root variant removal declarations: nested instances can legitimately
        # include another copy of the same source component elsewhere in the root.
        for _, block in records(text).values():
            if "  m_SourcePrefab:" not in block or "    m_TransformParent: {fileID: 0}" not in block:
                continue
            for match in re.finditer(r"(?m)^    m_Removed(?:Components|GameObjects):\r?\n((?:    -[^\n]*\n)+)", block):
                entry["Absent"] += [identity(p[2], p[1]) for p in POINTER.finditer(match[1]) if p[2]]
        entries.append(entry); pending.append(path); root_paths[name] = path
    filtered, exclusions = {}, []
    if args.exclusions:
        from filter_prefab import exclude_instances
        for group in json.loads(args.exclusions.read_text(encoding="utf-8-sig")):
            path = (assets / group["source"]).resolve()
            value, record = exclude_instances(path.read_bytes(), group["instances"], [])
            filtered[path] = value; exclusions.append(dict(source=str(path), authority=group["authority"], result=record))
    captured, visited, unresolved = [], set(), []
    while pending:
        path = pending.pop()
        if path in visited:
            continue
        visited.add(path)
        if len(visited) > 2048:
            raise ValueError("Catalog closure exceeds 2048 assets")
        if not path.is_relative_to(assets) or not path.is_file():
            raise ValueError("Missing/invalid required asset: " + str(path))
        raw = path.read_bytes(); data = filtered.get(path, raw)
        destination = staged / path.relative_to(assets)
        destination.parent.mkdir(parents=True, exist_ok=True)
        if destination.exists() and destination.read_bytes() != data:
            raise ValueError("Previously copied input differs: " + str(destination))
        if not destination.exists():
            destination.write_bytes(data)
        meta = path.with_name(path.name + ".meta"); metadata = meta.read_bytes()
        target_meta = destination.with_name(destination.name + ".meta")
        if target_meta.exists() and target_meta.read_bytes() != metadata:
            raise ValueError("Previously copied GUID metadata differs: " + str(target_meta))
        if not target_meta.exists():
            target_meta.write_bytes(metadata)
        captured.append({"source": str(path), "sha256": digest(raw), "copied": str(destination), "copiedSHA256": digest(data), "metaSHA256": digest(metadata), "filtered": path in filtered})
        if path.suffix.lower() == ".prefab":
            for guid in dependencies(data.decode("utf-8-sig")):
                if guid not in index:
                    unresolved.append({"source": str(path), "guid": guid, "status": "Missing dependency; Unity must resolve variant removals. Any retained geometry/source remains an explicit exporter failure."})
                    continue
                pending.append(index[guid])
    # Gate recipes refer to exact source GameObjects, not a display-name match.
    for entry in entries:
        if entry["Key"] == "building-19":
            masters = [p for p in visited if p.name == "blueprint_WoodPalisadeGate_Master.prefab"]
            if len(masters) != 1:
                raise ValueError("Gate source is ambiguous")
            guid = reverse[masters[0]]
            ids = ["5684304293000102801", "5277600045605098551"]
            entry["States"] += [{"Name": name, "Toggles": [{"Object": identity(guid, value), "Active": j == i} for j, value in enumerate(ids)]} for i, name in enumerate(("gate-open", "gate-closed"))]
        if entry["Key"] == "deco-77":
            entry.update(ApplyPose=True, Position={"x": 33.0699997, "y": 51.7200012, "z": -29.32299995}, Euler={"x": 0, "y": 41.27083969, "z": 0}, Scale={"x": 1.3818600, "y": 1.099769115, "z": 1.333107948})
    for record in captured:
        if digest(Path(record["source"]).read_bytes()) != record["sha256"]:
            raise ValueError("Original changed while staging: " + record["source"])
    settings = []
    for name in ("DynamicsManager.asset", "TagManager.asset"):
        source = assets.parent / "ProjectSettings" / name; target = project / "ProjectSettings" / name
        raw = source.read_bytes(); target.write_bytes(raw)
        settings.append({"source": str(source), "sha256": digest(raw), "copied": str(target)})
    output.mkdir(parents=True)
    save(output / "inputs.json", {"audit": str(args.audit.resolve()), "auditSHA256": digest(args.audit.read_bytes()), "sourceAssets": str(assets), "scope": args.scope, "assets": captured, "projectSettings": settings, "exclusions": exclusions, "unresolvedDependencies": unresolved, "originalHashesUnchanged": True, "gameScriptsImported": False, "qualified": False})
    batches = [entries[:8]] + [entries[i:i + 16] for i in range(8, len(entries), 16)]
    for i, batch in enumerate(batches):
        save(output / ("batch-%02d.json" % i), {"Prefabs": batch})
    save(output / "catalog.json", {"Prefabs": entries})
    print(json.dumps({"roots": len(entries), "assets": len(captured), "batches": len(batches), "output": str(output)}))


if __name__ == "__main__":
    main()
