"""Verify frozen copied content, original read-only assets, importer metadata and exact VFX exclusions."""
import argparse
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
from stage_catalog import ALLOWED, digest, save
from filter_prefab import POINTER


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inputs", type=Path, required=True)
    parser.add_argument("--exclusions", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists() or not args.output.resolve().is_relative_to(ALLOWED.resolve()): parser.error("Output must be new in D validation workspace")
    inputs = json.loads(args.inputs.read_text())
    exclusions = json.loads(args.exclusions.read_text())
    all_specs = [spec for group in exclusions for spec in group["instances"]]
    errors = []
    for item in inputs["assets"]:
        source, copied = Path(item["source"]), Path(item["copied"])
        if digest(source.read_bytes()) != item["sha256"] or digest(source.with_name(source.name + ".meta").read_bytes()) != item["metaSHA256"]: errors.append("Original changed: " + str(source))
        if digest(copied.read_bytes()) != item["copiedSHA256"]: errors.append("Copied content changed: " + str(copied))
        if digest(copied.with_name(copied.name + ".meta").read_bytes()) != item["metaSHA256"]: errors.append("Copied importer metadata differs: " + str(copied))
        if copied.suffix != ".prefab": continue
        text = copied.read_text(encoding="utf-8-sig")
        for spec in all_specs:
            if spec["excludedSourceGUID"] in text: errors.append("Excluded VFX source remains: " + str(copied))
            ids = {str(spec["prefabInstanceFileID"]), str(spec["strippedTransformFileID"])}
            if any(m[1] in ids and m[2] == spec["sourceAssetGUID"] for m in POINTER.finditer(text)): errors.append("Dangling removed source identity: " + str(copied))
    result = {"inputsSHA256": digest(args.inputs.read_bytes()), "exclusionsSHA256": digest(args.exclusions.read_bytes()), "assets": len(inputs["assets"]), "exactExcludedInstances": len(all_specs), "errors": errors, "passed": not errors, "qualified": False}
    save(args.output, result)
    print(json.dumps(result))
    if errors: raise SystemExit(1)


if __name__ == "__main__": main()
