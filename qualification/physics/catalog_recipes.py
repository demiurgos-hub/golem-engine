"""Derive explicit blueprint physics-state recipes from copied serialized declarations.

This does not execute or reimplement the game. It records the declared stage
object/collider targets and applies the documented visibility/layer state table
as a qualification fixture. Unknown layouts/overrides fail instead of guessing.
"""
import argparse
import json
from pathlib import Path
import re
import sys

sys.dont_write_bytecode = True
from stage_catalog import records, identity, save, digest, ALLOWED
from filter_prefab import POINTER


def stage_groups(block):
    mesh_text = block.split("  meshes:", 1)[1].split("  interactableWorldObject:", 1)[0]
    groups = []
    for match in re.finditer(r"(?m)^  - meshRenderers:.*?(?=^  - meshRenderers:|\Z)", mesh_text, re.S):
        text = match[0]
        def field(name):
            found = re.search(r"(?m)^    " + name + r":(?: \[\])?\r?\n((?:    -[^\n]*\n)*)", text)
            if not found:
                raise ValueError("Unsupported stage field " + name)
            return [x[1] for x in POINTER.finditer(found[1]) if x[1] != "0"]
        visibility = int(re.search(r"(?m)^    visibility: (\d+)", text)[1])
        if visibility not in range(5):
            raise ValueError("Unknown visibility")
        groups.append({"transforms": field("transforms"), "colliders": field("colliders"), "visibility": visibility})
    return groups


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inputs", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not args.output.resolve().is_relative_to(ALLOWED.resolve()) or args.output.exists():
        parser.error("Output must be a new D validation directory")
    inputs = json.loads((args.inputs / "inputs.json").read_text(encoding="utf-8"))
    catalog = json.loads((args.inputs / "catalog.json").read_text(encoding="utf-8"))
    source = Path(inputs["sourceAssets"])
    by_guid = {}
    for item in inputs["assets"]:
        path = Path(item["copied"])
        if path.suffix == ".prefab":
            guid = re.search(r"(?m)^guid: (\w+)", path.with_name(path.name + ".meta").read_text())[1]
            by_guid[guid] = path
    policies = []
    for entry in catalog["Prefabs"]:
        if not entry["Key"].startswith("blueprint-"):
            continue
        entry["States"] = [state for state in entry["States"] if not state["Name"].startswith("gate-")]
        seen, pending, found = set(), [entry["SourceGUID"]], []
        while pending:
            guid = pending.pop()
            if guid in seen:
                continue
            seen.add(guid)
            if guid not in by_guid:
                raise ValueError("Missing recipe source " + guid)
            text = by_guid[guid].read_text(encoding="utf-8-sig")
            if re.search(r"propertyPath: (?:meshes|stages|requiredStages)(?:\.|\r?$)", text, re.M):
                raise ValueError("Stage override requires explicit resolved recipe: " + entry["Key"])
            for file_id, (kind, block) in records(text).items():
                if "Scripts::Fractured.Building.BlueprintBuilderStage\n" in block or "Scripts::Fractured.Building.BlueprintBuilderStage\r\n" in block:
                    found.append((guid, file_id, stage_groups(block)))
                if kind == 1001:
                    pointer = re.search(r"m_SourcePrefab: (\{[^}]+\})", block)
                    if pointer:
                        match = POINTER.fullmatch(pointer[1]); pending.append(match[2])
        if not found:
            policies.append({"Key": entry["Key"], "Status": "No authored BlueprintBuilderStage found; authored capture retained"})
            continue
        # All-locked/all-active/all-completed are explicit policy probes. They do
        # not assert that impossible dependency combinations occur in gameplay.
        for name, active, complete in (("stages-locked", False, False), ("stages-active", True, False), ("stages-completed", False, True)):
            toggles, layers = {}, {}
            for guid, file_id, groups in found:
                for group in groups:
                    visible = (group["visibility"] in (0, 2) or (not complete and group["visibility"] == 1) or (complete and group["visibility"] == 3)) and (active or complete)
                    for target in group["transforms"]:
                        key = (guid, target)
                        if key in toggles and toggles[key] != visible:
                            raise ValueError("Conflicting stage visibility target")
                        toggles[key] = visible
                    for target in group["colliders"]:
                        layers[(guid, target)] = 22 if active or complete else 0
            entry["States"].append({"Name": name, "Toggles": [{"Object": identity(*key), "Active": value} for key, value in sorted(toggles.items())], "Layers": [{"Object": identity(*key), "Value": value} for key, value in sorted(layers.items())]})
        policies.append({"Key": entry["Key"], "Stages": [{"GUID": guid, "FileID": file_id, "Groups": groups} for guid, file_id, groups in found], "Status": "Explicit physics visibility/layer policy probes; nav modifier behavior and live ingredient dependencies require separate host coverage"})
    args.output.mkdir(parents=True)
    save(args.output / "catalog.json", catalog)
    entries = catalog["Prefabs"]
    batches = [entries[:8]] + [entries[i:i + 16] for i in range(8, len(entries), 16)]
    for i, batch in enumerate(batches):
        save(args.output / ("batch-%02d.json" % i), {"Prefabs": batch})
    save(args.output / "recipe-evidence.json", {"InputManifestSHA256": digest((args.inputs / "inputs.json").read_bytes()), "CatalogSHA256": digest((args.inputs / "catalog.json").read_bytes()), "PolicySource": "Scripts/Building/BlueprintBuilderStage.cs EvaluateMeshVisibility/SetColliderLayer/ApplyDamageLayerState; Default=0, DamageableServer=22 from copied TagManager.asset", "Recipes": policies, "Qualified": False})
    print(json.dumps({"blueprints": len(policies), "states": sum(len(x["States"]) for x in entries), "output": str(args.output)}))


if __name__ == "__main__":
    main()
