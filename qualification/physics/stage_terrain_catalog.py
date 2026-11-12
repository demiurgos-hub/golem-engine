"""Stage the ten explicitly registered server terrain roots without changing original assets."""
import argparse
import json
from pathlib import Path
import re
import sys

sys.dont_write_bytecode = True
from stage_catalog import ALLOWED, dependencies, digest, save


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--closure-audit", type=Path, required=True)
    parser.add_argument("--guid-index", type=Path, required=True)
    parser.add_argument("--source-assets", type=Path, required=True)
    parser.add_argument("--project", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists() or any(not p.resolve().is_relative_to(ALLOWED.resolve()) for p in (args.output, args.project)):
        parser.error("Project/output must be D validation paths; output must be new")
    audit = json.loads(args.closure_audit.read_text())
    index = json.loads(args.guid_index.read_text())
    roots = audit["serverTerrains"]
    if len(roots) > 16: raise ValueError("Terrain root budget exceeded")
    pending = [x["guid"] for x in roots]; seen = set(); records = []
    while pending:
        guid = pending.pop()
        if guid in seen: continue
        seen.add(guid)
        if len(seen) > 128 or guid not in index: raise ValueError("Missing/oversized terrain dependency closure")
        relative = Path(index[guid]); source = args.source_assets / relative; target = args.project / "Assets/ResolvedCatalog" / relative
        if not source.resolve().is_relative_to(args.source_assets.resolve()): raise ValueError("Source path escapes Assets")
        raw = source.read_bytes(); meta = source.with_name(source.name + ".meta").read_bytes()
        if re.search(rb"(?m)^guid: ([0-9a-f]{32})", meta)[1].decode() != guid: raise ValueError("GUID index no longer matches source")
        target.parent.mkdir(parents=True, exist_ok=True)
        if target.exists() and target.read_bytes() != raw: raise ValueError("Existing staged terrain differs")
        target_meta = target.with_name(target.name + ".meta")
        if target_meta.exists() and target_meta.read_bytes() != meta: raise ValueError("Existing staged metadata differs")
        if not target.exists(): target.write_bytes(raw)
        if not target_meta.exists(): target_meta.write_bytes(meta)
        records.append({"source": str(source.resolve()), "copied": str(target.resolve()), "sha256": digest(raw), "metaSHA256": digest(meta)})
        if source.suffix == ".prefab": pending += sorted(dependencies(raw.decode("utf-8-sig")))
    entries = [{"Key": "terrain-%02d" % i, "Asset": "Assets/ResolvedCatalog/" + x["path"], "SourceGUID": x["guid"], "States": [], "Absent": []} for i, x in enumerate(roots)]
    args.output.mkdir(parents=True)
    save(args.output / "inputs.json", {"closureAuditSHA256": digest(args.closure_audit.read_bytes()), "assets": records, "registeredTerrains": roots, "qualified": False})
    # Two complete heightfields per process bound retained reference memory.
    for i in range(0, len(entries), 2): save(args.output / ("batch-%02d.json" % (i // 2)), {"Prefabs": entries[i:i + 2]})
    print(json.dumps({"roots": len(entries), "assets": len(records), "batches": (len(entries) + 1) // 2}))


if __name__ == "__main__": main()
