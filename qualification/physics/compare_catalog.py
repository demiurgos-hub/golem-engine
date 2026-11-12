"""Run the built Go catalog gate against preserved Unity batches without stopping at a content failure."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--capture", type=Path, action="append", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    allowed = Path("D:/Demiurgos/tests/physics-validation").resolve()
    if not args.output.resolve().is_relative_to(allowed) or args.output.exists():
        parser.error("Output must be a new D validation directory")
    args.output.mkdir(parents=True)
    report = {"version": 1, "binary": str(args.binary.resolve()), "binarySHA256": sha(args.binary), "scope": "Actual copied Unity geometry; loader identity/activation and unchanged strict query tolerances; no dynamics/capacity qualification", "batches": [], "qualified": False}
    for capture in args.capture:
        for manifest in sorted(capture.glob("batch-*/catalog-result.json")):
            name = capture.name + "-" + manifest.parent.name
            output = args.output / name
            env = dict(os.environ, GOLEM_PHYSICS_CATALOG_DIR=str(manifest.parent.resolve()), GOLEM_PHYSICS_CATALOG_REPORT_DIR=str(output.resolve()))
            with (args.output / (name + ".log")).open("xb") as log:
                result = subprocess.run([str(args.binary.resolve()), "-test.run=^TestUnityResolvedCatalog$", "-test.count=1", "-test.timeout=5m"], env=env, stdout=log, stderr=subprocess.STDOUT)
            item = {"input": str(manifest), "inputSHA256": sha(manifest), "exitCode": result.returncode, "output": str(output)}
            if (output / "catalog-go.json").exists():
                values = json.loads((output / "catalog-go.json").read_text())
                item.update(states=len(values["Results"]), loaded=sum(x["Loaded"] for x in values["Results"]), identityActivationPassed=sum(x["IdentityActivationPassed"] for x in values["Results"]), queryPassed=sum(x["QueryPassed"] for x in values["Results"]))
            report["batches"].append(item)
            print(json.dumps(item), flush=True)
    with (args.output / "manifest.json").open("x", encoding="utf-8") as stream:
        json.dump(report, stream, indent=2)


if __name__ == "__main__":
    main()
