"""Exclude one explicitly authorized nested prefab from a copied qualification input.

This narrow Unity-YAML filter is not an exporter fallback. Exact input hashes,
instance/transform IDs and source GUIDs are mandatory. Other prefab variants are
read-only and must not retain references to the removed objects. Outputs are new
files inside the physics validation workspace; original assets are never written.
"""

import argparse
import hashlib
import json
from pathlib import Path
import re


HEADER = re.compile(r"(?m)^--- !u!(\d+) &(-?\d+)( stripped)?\r?$")
POINTER = re.compile(r"\{\s*fileID:\s*(-?\d+)(?:,\s*guid:\s*([0-9a-f]{32}),\s*type:\s*\d+)?\s*\}")


def digest(data):
    return hashlib.sha256(data).hexdigest()


def pointer(text, field):
    matches = re.findall(r"(?m)^ {2,}" + re.escape(field) + r":\s*(\{[^}]+\})", text)
    value = POINTER.fullmatch(matches[0]) if len(matches) == 1 else None
    if value is None:
        raise ValueError("missing or unsupported pointer: " + field)
    return value[1], value[2]


def exclude_instance(data, spec, variants, *, _remaining_instances=False):
    if digest(data) != spec["sourceSHA256"].lower():
        raise ValueError("source hash mismatch")
    guid, asset_guid = spec["excludedSourceGUID"], spec["sourceAssetGUID"]
    if not all(re.fullmatch(r"[0-9a-f]{32}", value) for value in (guid, asset_guid)):
        raise ValueError("invalid source GUID")
    instance, transform, parent = (str(spec[key]) for key in ("prefabInstanceFileID", "strippedTransformFileID", "parentTransformFileID"))
    if len({instance, transform, parent}) != 3 or not all(re.fullmatch(r"[1-9][0-9]{0,18}", value) for value in (instance, transform, parent)):
        raise ValueError("invalid or duplicate object IDs")
    text = data.decode("utf-8")
    headers = list(HEADER.finditer(text))
    records = {}
    for index, match in enumerate(headers):
        if match[2] in records:
            raise ValueError("duplicate Unity object ID")
        end = headers[index + 1].start() if index + 1 < len(headers) else len(text)
        records[match[2]] = (match, text[match.start():end])
    if not all(key in records for key in (instance, transform, parent)):
        raise ValueError("declared object missing")
    instance_header, instance_text = records[instance]
    transform_header, transform_text = records[transform]
    parent_header, parent_text = records[parent]
    if instance_header[1] != "1001" or pointer(instance_text, "m_SourcePrefab") != ("100100000", guid) or pointer(instance_text, "m_TransformParent") != (parent, None):
        raise ValueError("declared prefab instance does not match")
    if transform_header[1] != "4" or not transform_header[3] or pointer(transform_text, "m_PrefabInstance") != (instance, None) or pointer(transform_text, "m_CorrespondingSourceObject")[1] != guid:
        raise ValueError("declared stripped transform does not match")
    if parent_header[1] != "4" or parent_header[3]:
        raise ValueError("unsupported parent transform")
    children = re.search(r"(?m)^  m_Children:\r?\n((?:  -[^\r\n]*\r?\n)+)", parent_text)
    child = re.compile(r"(?m)^  - \{fileID: " + transform + r"\}\r?\n")
    if children is None or len(child.findall(children[1])) != 1:
        raise ValueError("expected exactly one parent child reference")
    remaining = child.sub("", children[1])
    newline = "\r\n" if "\r\n" in children[0] else "\n"
    replacement = "  m_Children:" + newline + remaining if remaining else "  m_Children: []" + newline
    updated_parent = parent_text[:children.start()] + replacement + parent_text[children.end():]
    output = text[:headers[0].start()] + "".join(updated_parent if key == parent else record for key, (_, record) in records.items() if key not in (instance, transform))
    removed = {instance, transform}
    if (guid in output and not _remaining_instances) or any(match[1] in removed and match[2] in (None, asset_guid) for match in POINTER.finditer(output)):
        raise ValueError("unexpected references to excluded source remain")
    variant_records = []
    for name, variant_data, expected_hash in variants:
        if digest(variant_data) != expected_hash.lower():
            raise ValueError("variant hash mismatch: " + name)
        variant = variant_data.decode("utf-8")
        if guid in variant or any(match[1] in removed and match[2] == asset_guid for match in POINTER.finditer(variant)):
            raise ValueError("variant retains excluded object reference: " + name)
        variant_records.append({"path": name, "sha256": digest(variant_data), "unchanged": True, "removedObjectReferences": 0})
    result = output.encode("utf-8")
    return result, {"sourceSHA256": digest(data), "filteredSHA256": digest(result), "removedFileIDs": [instance, transform], "parentTransformFileID": parent, "variants": variant_records}


def exclude_instances(data, specs, variants):
    """Remove an exact authorized set of instances, then reject every dangling reference."""
    if not specs or len(specs) > 32:
        raise ValueError("expected one to thirty-two exact exclusions")
    original_hash = digest(data)
    if any(spec["sourceSHA256"].lower() != original_hash for spec in specs):
        raise ValueError("source hash mismatch")
    if len({spec["sourceAssetGUID"] for spec in specs}) != 1:
        raise ValueError("exclusions must belong to one source asset")
    ids = [str(spec[key]) for spec in specs for key in ("prefabInstanceFileID", "strippedTransformFileID")]
    if len(set(ids)) != len(ids):
        raise ValueError("duplicate excluded object")
    result, records = data, []
    for spec in specs:
        current = dict(spec, sourceSHA256=digest(result))
        result, record = exclude_instance(result, current, [], _remaining_instances=True)
        records.append(record)
    source_guid = specs[0]["sourceAssetGUID"]
    excluded_guids = {spec["excludedSourceGUID"] for spec in specs}
    def check(text, external):
        return any(guid in text for guid in excluded_guids) or any(m[1] in ids and (m[2] == source_guid or (not external and m[2] is None)) for m in POINTER.finditer(text))
    if check(result.decode("utf-8"), False):
        raise ValueError("unexpected references to excluded source remain")
    checked = []
    for name, raw, expected in variants:
        if digest(raw) != expected.lower() or check(raw.decode("utf-8"), True):
            raise ValueError("variant hash/reference check failed: " + name)
        checked.append({"path": name, "sha256": digest(raw), "unchanged": True})
    return result, {"sourceSHA256": original_hash, "filteredSHA256": digest(result), "instances": records, "variants": checked}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    args = parser.parse_args()
    spec = json.loads(args.manifest.read_text(encoding="utf-8-sig"))
    if not spec.get("authority"):
        parser.error("explicit exclusion authority is required")
    allowed = Path("D:/Demiurgos/tests/physics-validation").resolve(strict=True)
    for path in (args.output, args.evidence):
        if not path.resolve().is_relative_to(allowed) or path.exists():
            parser.error("outputs must be new files in the validation workspace")
    if args.output.resolve() == args.evidence.resolve():
        parser.error("output and evidence must differ")
    source = Path(spec["sourcePath"])
    variants = [(item["path"], Path(item["path"]).read_bytes(), item["sha256"]) for item in spec["variants"]]
    result, record = exclude_instance(source.read_bytes(), spec, variants)
    record.update(authority=spec["authority"], manifest=str(args.manifest.resolve()), manifestSHA256=digest(args.manifest.read_bytes()), sourcePath=str(source.resolve()), outputPath=str(args.output.resolve()), scope="Explicitly filtered server-only qualification fixture; not an unchanged full-prefab capture", exporterMissingSourceGuard="unchanged")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("xb") as stream:
        stream.write(result)
    with args.evidence.open("x", encoding="utf-8") as stream:
        json.dump(record, stream, indent=2)
        stream.write("\n")


if __name__ == "__main__":
    main()
