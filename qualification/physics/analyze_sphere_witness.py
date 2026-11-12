"""Independent analytic reference for frozen float32 sphere/triangle queries.

Uses double precision only in this offline reference, never in authoritative
body state. Faces, finite edge cylinders and vertex spheres are solved directly;
no Golem or Box3D casting code is called. Output belongs in the validation workspace.
"""

import argparse
import json
import math
from pathlib import Path
import struct


def f32(value):
    return struct.unpack("<f", struct.pack("<f", value))[0]


def add(a, b):
    return tuple(x + y for x, y in zip(a, b))


def sub(a, b):
    return tuple(x - y for x, y in zip(a, b))


def mul(a, scale):
    return tuple(x * scale for x in a)


def dot(a, b):
    return sum(x * y for x, y in zip(a, b))


def cross(a, b):
    return (a[1]*b[2]-a[2]*b[1], a[2]*b[0]-a[0]*b[2], a[0]*b[1]-a[1]*b[0])


def length(vector):
    return math.sqrt(dot(vector, vector))


def unit(vector):
    return mul(vector, 1 / length(vector))


def intersection(vertices, origin, direction, radius, maximum):
    best = None

    def emit(distance, point, normal, feature):
        nonlocal best
        if 0 <= distance <= maximum and (best is None or distance < best["Distance"]):
            best = dict(Distance=distance, Point=point, Normal=normal, Feature=feature)

    a, b, c = vertices
    normal = unit(cross(sub(b, a), sub(c, a)))
    denominator = dot(direction, normal)
    if denominator:
        for sign in (-1, 1):
            distance = (sign*radius-dot(sub(origin, a), normal))/denominator
            point = sub(add(origin, mul(direction, distance)), mul(normal, sign*radius))
            if all(dot(cross(sub(vertices[(i+1)%3], vertices[i]), sub(point, vertices[i])), normal) >= -1e-12 for i in range(3)):
                emit(distance, point, mul(normal, sign), "face")
    for i in range(3):
        a, b = vertices[i], vertices[(i+1)%3]
        edge = sub(b, a)
        edge_length = length(edge)
        axis = mul(edge, 1/edge_length)
        offset = sub(origin, a)
        os = sub(offset, mul(axis, dot(offset, axis)))
        ds = sub(direction, mul(axis, dot(direction, axis)))
        qa, qb, qc = dot(ds, ds), dot(os, ds), dot(os, os)-radius*radius
        discriminant = qb*qb-qa*qc
        if qa > 1e-20 and discriminant >= 0:
            distance = (-qb-math.sqrt(discriminant))/qa
            along = dot(add(offset, mul(direction, distance)), axis)
            if 0 <= along <= edge_length:
                point = add(a, mul(axis, along))
                emit(distance, point, unit(sub(add(origin, mul(direction, distance)), point)), "edge")
        qa, qb, qc = dot(direction, direction), dot(offset, direction), dot(offset, offset)-radius*radius
        discriminant = qb*qb-qa*qc
        if discriminant >= 0:
            distance = (-qb-math.sqrt(discriminant))/qa
            emit(distance, a, unit(sub(add(origin, mul(direction, distance)), a)), "vertex")
    return best


def vector(value):
    if isinstance(value, dict):
        value = [value[key] for key in ("X", "Y", "Z")]
    return tuple(f32(x) for x in value)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fixtures", required=True, type=Path)
    parser.add_argument("--observations", type=Path, help="Optional captured Go entries {Name, Hits: [{Distance, Point, Normal}]}.")
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    fixtures = json.loads(args.fixtures.read_text())
    observations = {} if args.observations is None else {entry["Name"]: entry["Hits"] for entry in json.loads(args.observations.read_text())}
    results = []
    for fixture in fixtures:
        movement = vector(fixture["Translation"])
        expected = intersection([vector(v) for v in fixture["Vertices"]], vector(fixture["Origin"]), unit(movement), f32(fixture["Radius"]), length(movement))
        if expected is None:
            raise ValueError(f"No analytic contact: {fixture['Name']}")
        result = dict(Name=fixture["Name"], Analytic=expected)
        recorded = fixture.get("Analytic")
        if recorded is not None:
            if abs(recorded["Distance"]-expected["Distance"]) > 1e-9 or length(sub(recorded["Point"], expected["Point"])) > 1e-8:
                raise ValueError(f"Frozen analytic result drifted: {fixture['Name']}")
        if fixture["Name"] in observations:
            got = min(observations[fixture["Name"]], key=lambda hit: abs(hit["Distance"]-expected["Distance"]))
            result["GolemError"] = dict(Distance=abs(f32(got["Distance"])-expected["Distance"]), Point=length(sub(vector(got["Point"]), expected["Point"])), Normal=length(sub(vector(got["Normal"]), expected["Normal"])))
            result["WithinDistanceAndPointTargets"] = result["GolemError"]["Distance"] <= .0001 and result["GolemError"]["Point"] <= .001
        results.append(result)
    args.output.write_text(json.dumps(dict(Method=__doc__, Results=results), indent=2)+"\n")


if __name__ == "__main__":
    main()
