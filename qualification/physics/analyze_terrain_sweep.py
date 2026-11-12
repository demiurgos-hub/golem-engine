"""Classify one frozen sphere/terrain discrepancy against independent analytic geometry.

The reference uses exact float32 source/query inputs and double precision only
for offline analytic face/edge/vertex intersections. It applies front-face query
policy explicitly, checks every swept-AABB candidate, and never changes runtime
tolerances or treats a Unity disagreement as an automatic backend failure.
"""
import argparse
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
from analyze_sphere_witness import vector, f32, intersection, add, sub, mul, dot, cross, length, unit
from stage_catalog import ALLOWED, digest, save


def query_vector(value):
    return vector({key.upper(): number for key, number in value.items()})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--reference", type=Path, required=True)
    parser.add_argument("--go-report", type=Path, required=True)
    parser.add_argument("--query", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists() or not args.output.resolve().is_relative_to(ALLOWED.resolve()): parser.error("Output must be new in D validation workspace")
    source = json.loads(args.source.read_text(encoding="utf-8-sig")); reference = json.loads(args.reference.read_text(encoding="utf-8-sig")); report = json.loads(args.go_report.read_text())
    if len(source["Bodies"]) != 1 or len(source["Colliders"]) != 1 or len(source["Assets"]) != 1: raise ValueError("This bounded reference requires one terrain/body")
    query = next(x for x in reference["Queries"] if x["Name"] == args.query)
    if query["Type"] != "sphere": raise ValueError("Sphere query required")
    got = next(x for x in report["Observations"] if x["Name"] == args.query)["Actual"]
    body = source["Bodies"][0]
    if body["Rotation"] != dict(X=0, Y=0, Z=0, W=1): raise ValueError("Rotated source requires a separate transformed-reference implementation")
    position = vector(body["Position"])
    origin = tuple(f32(a-b) for a, b in zip(query_vector(query["From"]), position))
    direction = query_vector(query["Direction"]); maximum, radius = f32(query["Distance"]), f32(query["Radius"])
    if length(direction) != 1: raise ValueError("Expected axis-aligned unit direction")
    end = add(origin, mul(direction, maximum))
    low = [min(origin[k], end[k])-radius for k in range(3)]; high = [max(origin[k], end[k])+radius for k in range(3)]
    asset = source["Assets"][0]; vertices = [vector(x) for x in asset["Vertices"]]; indices = asset["Indices"]
    candidates, contacts, backs = [], [], []
    for ordinal in range(len(indices)//3):
        points = [vertices[x] for x in indices[ordinal*3:ordinal*3+3]]
        if any(max(p[k] for p in points) < low[k] or min(p[k] for p in points) > high[k] for k in range(3)): continue
        candidates.append(ordinal)
        hit = intersection(points, origin, direction, radius, maximum)
        if hit is None: continue
        normal = unit(cross(sub(points[1], points[0]), sub(points[2], points[0])))
        entry = dict(Triangle=ordinal, Vertices=points, WindingNormal=normal, WitnessFacing=dot(normal, hit["Normal"]), **hit)
        (contacts if entry["WitnessFacing"] >= 0 else backs).append(entry)
    contacts.sort(key=lambda x: (x["Distance"], x["Triangle"]))
    backs.sort(key=lambda x: (x["Distance"], x["Triangle"]))
    if not contacts: raise ValueError("No front-face analytic contact")
    best = contacts[0]; worldpoint = add(best["Point"], position)
    observations = []
    for origin_name, hits in (("Golem", got), ("Unity", query["Hits"])):
        for hit in hits:
            observations.append({"Source": origin_name, "Hit": hit, "DistanceError": abs(f32(hit["Distance"])-best["Distance"]), "PointError": length(sub(query_vector(hit["Point"]), worldpoint)), "NormalError": length(sub(query_vector(hit["Normal"]), best["Normal"]))})
    save(args.output, {"Method": __doc__, "SourceSHA256": digest(args.source.read_bytes()), "ReferenceSHA256": digest(args.reference.read_bytes()), "GoReportSHA256": digest(args.go_report.read_bytes()), "Query": query, "Body": body, "Candidates": candidates, "FirstFrontContacts": contacts[:12], "BackFaceContactsExcluded": backs[:12], "AnalyticWorldPoint": worldpoint, "Observations": observations, "Qualified": False})
    print(json.dumps({"Candidates": len(candidates), "FrontContacts": len(contacts), "BackContacts": len(backs), "EarliestTriangle": best["Triangle"], "EarliestDistance": best["Distance"], "Errors": [{key: value for key, value in entry.items() if key != "Hit"} for entry in observations]}))


if __name__ == "__main__": main()
