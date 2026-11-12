"""Summarize resolved capture coverage and exact physical signatures, retaining per-root identity evidence.

Only capture-local identity labels are canonicalized. Geometry coordinates,
topology, poses, activation, collider roles, filters, materials, joint fields,
character policy and metadata are included without rounding or simplification.
The structural leaf order is retained, so ambiguous equivalence under-deduplicates.
"""
import argparse
import copy
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
from stage_catalog import ALLOWED, digest, save


def signature(source, resolved):
    result = copy.deepcopy(source)
    result.pop("WorldID", None); result.pop("Revision", None); result.pop("Generator", None)
    colliders = {x["ID"]: x for x in result["Colliders"]}
    order = [x["ID"] for x in resolved["Leaves"]]
    if set(order) != set(colliders) or len(order) != len(colliders): raise ValueError("Resolved identity set differs from portable source")
    result["Colliders"] = [colliders[key] for key in order]
    identities = {}
    def register(value, prefix):
        if value and value not in identities: identities[value] = prefix + str(sum(x.startswith(prefix) for x in identities.values()))
    for item in result["Colliders"]:
        register(item["ID"], "collider:"); register(item["BodyID"], "body:"); register(item.get("ParentID"), "parent:"); register(item.get("SourceID"), "source:"); register(item.get("AssetID"), "asset:"); register(item.get("MaterialID"), "material:")
    for category, prefix in (("Bodies", "body:"), ("Assets", "asset:"), ("Materials", "material:"), ("Joints", "joint:")):
        for item in result[category]: register(item["ID"], prefix)
    for item in result["Assets"]: register(item.get("SourceID"), "geometry-source:")
    # Nonempty application ownership is intentionally preserved, not canonicalized.
    identity_fields = {"ID", "BodyID", "ConnectedBodyID", "ParentID", "SourceID", "AssetID", "MaterialID"}
    for category in ("Bodies", "Assets", "Materials", "Joints", "Colliders"):
        for item in result[category]:
            for field in identity_fields & item.keys():
                if item[field]: item[field] = identities[item[field]]
        if category != "Colliders": result[category].sort(key=lambda item: item["ID"])
    data = json.dumps(result, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()
    return digest(data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--capture", type=Path, action="append", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not args.output.resolve().is_relative_to(ALLOWED.resolve()) or args.output.exists(): parser.error("Output must be new in D validation workspace")
    report = {"version": 1, "method": __doc__, "roots": [], "signatures": {}, "qualified": False}
    for capture in args.capture:
        for path in sorted(capture.glob("batch-*/catalog-result.json")):
            source_report = json.loads(path.read_text(encoding="utf-8-sig"))
            for root in source_report["Results"]:
                record = {"Key": root["Key"], "Asset": root["Asset"], "Result": str(path), "ResultSHA256": digest(path.read_bytes()), "Error": root.get("Error"), "States": []}
                report["roots"].append(record)
                for state in root["Captures"]:
                    item = {"State": state["State"], "Error": state.get("Error"), "Bodies": state["Bodies"], "Colliders": state["Colliders"], "ActiveColliders": state["ActiveColliders"], "Queries": state["Queries"]}
                    record["States"].append(item)
                    if state.get("Error"): continue
                    folder = path.parent / root["Key"] / state["State"]
                    source_path = folder / "unity-physics.source.json"
                    source = json.loads(source_path.read_text(encoding="utf-8-sig"))
                    key = signature(source, state)
                    item.update(PhysicalSignature=key, Source=str(source_path), SourceSHA256=digest(source_path.read_bytes()), IdentityEvidenceSHA256=digest((folder / "resolved.json").read_bytes()))
                    report["signatures"].setdefault(key, []).append({"Key": root["Key"], "State": state["State"], "Source": str(source_path)})
    report["summary"] = {"roots": len(report["roots"]), "states": sum(len(x["States"]) for x in report["roots"]), "failedStates": sum(bool(s["Error"]) for x in report["roots"] for s in x["States"]), "fullPhysicalSignatures": len(report["signatures"])}
    save(args.output, report)
    print(json.dumps(report["summary"]))


if __name__ == "__main__": main()
