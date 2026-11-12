"""Freeze selected database rows and bounded exceptional-pose captures for server-content coverage.

Selection follows the checked-in Simulator WorldDatabase_Final and explicit cave
template1 bindings. It does not substitute generated/manual databases or claim
that checked-in entity payloads describe a live deployed snapshot.
"""
import argparse
import base64
import copy
import json
from pathlib import Path
import sqlite3
import sys

sys.dont_write_bytecode = True
from stage_catalog import ALLOWED, CATALOGS, digest, save


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--audit", type=Path, required=True)
    parser.add_argument("--catalog", type=Path, required=True)
    parser.add_argument("--source-assets", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not args.output.resolve().is_relative_to(ALLOWED.resolve()) or args.output.exists():
        parser.error("Output must be a new D validation directory")
    args.output.mkdir(parents=True)
    audit = json.loads(args.audit.read_text(encoding="utf-8-sig"))
    catalog = {x["Key"]: x for x in json.loads(args.catalog.read_text())["Prefabs"]}
    report = {"version": 1, "auditSHA256": digest(args.audit.read_bytes()), "catalogSHA256": digest(args.catalog.read_bytes()), "databases": [], "rows": [], "qualified": False,
              "limitations": ["Manifest accounts for every selected checked-in row; it is not a fully instantiated or simulated world.", "Direct WorldEntities use EntityFactory/snapshot policy, not WorldStreamer decoration loading; payload bytes are retained without inventing live state.", "Exceptional pose inputs preserve nonuniform/tilted transforms; normal rows retain all original transforms in this manifest."]}
    variants = []
    for name in ("WorldDatabase_Final.bytes", "CaveDatabase1.bytes"):
        relative = "AssetsByType/Databases/" + name
        original = args.source_assets / relative
        data = original.read_bytes()
        if digest(data) != audit["databases"][relative]["sha256"]:
            raise ValueError("Authoritative database changed since audit")
        copied = args.output / name; copied.write_bytes(data)
        database = {"source": str(original.resolve()), "copied": str(copied.resolve()), "sha256": digest(data), "tables": {}}
        report["databases"].append(database)
        connection = sqlite3.connect(copied.resolve().as_uri() + "?mode=ro&immutable=1", uri=True)
        connection.row_factory = sqlite3.Row
        for table, category in (("WorldDecorations", "deco"), ("Harvestables", "harvest"), ("WorldEntities", "building")):
            rows = connection.execute('SELECT * FROM "' + table + '" ORDER BY ObjectID').fetchall()
            database["tables"][table] = len(rows)
            for raw in rows:
                row = dict(raw)
                payload = row.pop("Bytes")
                if isinstance(payload, str): payload = payload.encode("utf-8")
                payload = payload or b""
                entry = {"database": name, "table": table, "row": row, "payloadBase64": base64.b64encode(payload).decode(), "payloadSHA256": digest(payload)}
                if table == "WorldEntities" and row["EntityType"] != 9:
                    raise ValueError("Unclassified direct entity type")
                number = row["VariantId"] if table == "WorldEntities" else row["PrefabID"]
                key = category + "-" + str(number)
                source_row = next(x for x in audit["catalogs"][CATALOGS[category]] if x["id"] == number)
                if source_row["server"] is None:
                    if table != "WorldDecorations" or number not in (80, 81, 82):
                        raise ValueError("Unexpected null selected server root")
                    entry.update(root=None, status="Explicit WorldStreamer null serverPrefab skip")
                else:
                    if key not in catalog: raise ValueError("Selected row absent from catalog " + key)
                    entry.update(root=key, sourceGUID=catalog[key]["SourceGUID"], status="Selected server root; resolution/load/query results tracked separately")
                    scale = [row.get("Scale" + axis, 1) for axis in "XYZ"]
                    exceptional = max(scale) - min(scale) > .00001 or abs(row["RotX"]) > .00001 or abs(row["RotZ"]) > .00001 or scale[0] * scale[1] * scale[2] < 0
                    if exceptional:
                        item = copy.deepcopy(catalog[key]); item["Key"] = ("cave" if name.startswith("Cave") else "final") + "-" + str(row["ObjectID"])
                        item.update(ApplyPose=True, Position={axis.lower(): row["Pos" + axis] for axis in "XYZ"}, Euler={axis.lower(): row["Rot" + axis] for axis in "XYZ"}, Scale={axis.lower(): row.get("Scale" + axis, 1) for axis in "XYZ"}, States=[])
                        variants.append(item); entry["exceptionalPoseCapture"] = item["Key"]
                report["rows"].append(entry)
        connection.close()
        if digest(original.read_bytes()) != digest(data): raise ValueError("Original DB changed during capture")
    if len(variants) > 256: raise ValueError("Exceptional pose budget exceeded")
    save(args.output / "assembly-manifest.json", report)
    for i in range(0, len(variants), 16): save(args.output / ("batch-%02d.json" % (i // 16)), {"Prefabs": variants[i:i + 16]})
    save(args.output / "pose-coverage.json", {"exceptionalRows": len(variants), "batches": (len(variants) + 15) // 16, "totalRows": len(report["rows"]), "runtimeNullSkips": sum(x["root"] is None for x in report["rows"]), "qualified": False})
    print(json.dumps({"rows": len(report["rows"]), "exceptionalPoses": len(variants), "output": str(args.output)}))


if __name__ == "__main__":
    main()
