using System;
using System.Collections.Generic;
using System.Globalization;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Deterministic local primitive surfaces for explicitly selected navigation colliders.</summary>
    internal static class GolemNavigationPrimitiveGeometry
    {
        // Round sources retain a fixed 24-segment azimuth and eight steps per hemisphere.
        // This is a navigation surface approximation, not a replacement for native physics shapes.
        private const int Segments = 24;
        private const int HemisphereSteps = 8;

        /// <summary>Identifies canonical geometry independently of placement, source object and collider activity.</summary>
        internal static string Identity(Collider collider, int area)
        {
            string shape;
            if (collider is BoxCollider box)
            {
                Positive(box.size.x); Positive(box.size.y); Positive(box.size.z);
                shape = "box";
            }
            else if (collider is SphereCollider sphere)
            {
                Positive(sphere.radius);
                shape = "sphere/24/8";
            }
            else if (collider is CapsuleCollider capsule)
            {
                Positive(capsule.radius);
                if (float.IsNaN(capsule.height) || float.IsInfinity(capsule.height) || capsule.height < 0 || capsule.direction < 0 || capsule.direction > 2)
                    throw new InvalidOperationException("Invalid navigation capsule dimensions or direction.");
                shape = "capsule/24/8/" + capsule.radius.ToString("R", CultureInfo.InvariantCulture) + "/"
                    + Mathf.Max(capsule.height, capsule.radius * 2).ToString("R", CultureInfo.InvariantCulture);
            }
            else throw new InvalidOperationException("Explicit navigation colliders support Box, Sphere, Capsule and MeshCollider only; no bounds approximation is exported.");
            return "golem/navigation/primitive/v1/" + shape + "/area/" + area.ToString(CultureInfo.InvariantCulture);
        }

        /// <summary>Preserves the source's center, axis and full affine hierarchy without baking placement into shared vertices.</summary>
        internal static Matrix4x4 Transform(Collider collider)
        {
            if (collider is BoxCollider box)
                return collider.transform.localToWorldMatrix * Matrix4x4.TRS(box.center, Quaternion.identity, box.size);
            if (collider is SphereCollider sphere)
                return collider.transform.localToWorldMatrix * Matrix4x4.TRS(sphere.center, Quaternion.identity, Vector3.one * (sphere.radius * 2));
            var capsule = (CapsuleCollider)collider;
            var rotation = capsule.direction == 0 ? Quaternion.Euler(0, 0, -90)
                : capsule.direction == 2 ? Quaternion.Euler(90, 0, 0) : Quaternion.identity;
            return collider.transform.localToWorldMatrix * Matrix4x4.TRS(capsule.center, rotation, Vector3.one);
        }

        /// <summary>Captures a shared local surface with explicit triangle areas and nondegenerate outward-facing triangles.</summary>
        internal static NavigationAsset Capture(Collider collider, string id, int area)
        {
            if (collider is BoxCollider)
            {
                var vertices = new[]
                {
                    new NavigationVector(-.5,-.5,-.5), new NavigationVector(.5,-.5,-.5), new NavigationVector(.5,.5,-.5), new NavigationVector(-.5,.5,-.5),
                    new NavigationVector(-.5,-.5,.5), new NavigationVector(.5,-.5,.5), new NavigationVector(.5,.5,.5), new NavigationVector(-.5,.5,.5)
                };
                var indices = new uint[] { 0,2,1, 0,3,2, 4,5,6, 4,6,7, 0,1,5, 0,5,4, 3,7,6, 3,6,2, 0,4,7, 0,7,3, 1,2,6, 1,6,5 };
                var triangles = new NavigationTriangle[indices.Length / 3];
                for (var i = 0; i < triangles.Length; i++) triangles[i] = new NavigationTriangle
                    { Indices = new[] { indices[i * 3], indices[i * 3 + 1], indices[i * 3 + 2] }, Area = area };
                return new NavigationAsset { ID = id, Vertices = vertices, Triangles = triangles };
            }
            if (collider is SphereCollider) return Round(id, .5f, 1, area);
            var capsule = (CapsuleCollider)collider;
            return Round(id, capsule.radius, Mathf.Max(capsule.height, capsule.radius * 2), area);
        }

        /// <summary>Tessellates hemispheres and the optional cylinder without duplicate pole or equator triangles.</summary>
        private static NavigationAsset Round(string id, float radius, float height, int area)
        {
            var halfCylinder = Mathf.Max(0, height * .5f - radius);
            var vertices = new List<NavigationVector> { new NavigationVector(0, -halfCylinder - radius, 0) };
            var triangles = new List<NavigationTriangle>();
            var rings = 0;
            void Ring(float angle, float offset)
            {
                var y = Mathf.Sin(angle) * radius + offset;
                var extent = Mathf.Cos(angle) * radius;
                for (var segment = 0; segment < Segments; segment++)
                {
                    var yaw = segment * Mathf.PI * 2 / Segments;
                    vertices.Add(new NavigationVector(Mathf.Cos(yaw) * extent, y, Mathf.Sin(yaw) * extent));
                }
                rings++;
            }
            void Face(uint a, uint b, uint c) => triangles.Add(new NavigationTriangle { Indices = new[] { a, b, c }, Area = area });
            for (var step = 1; step <= HemisphereSteps; step++) Ring(-Mathf.PI * .5f + step * Mathf.PI / (2 * HemisphereSteps), -halfCylinder);
            if (halfCylinder > 0) Ring(0, halfCylinder);
            for (var step = 1; step < HemisphereSteps; step++) Ring(step * Mathf.PI / (2 * HemisphereSteps), halfCylinder);
            var top = (uint)vertices.Count;
            vertices.Add(new NavigationVector(0, halfCylinder + radius, 0));
            for (var segment = 0; segment < Segments; segment++)
            {
                var current = (uint)segment;
                var next = (uint)((segment + 1) % Segments);
                Face(0, 1 + current, 1 + next);
                for (var ring = 1; ring < rings; ring++)
                {
                    var lower = (uint)(1 + (ring - 1) * Segments);
                    var upper = lower + Segments;
                    Face(lower + current, upper + current, lower + next);
                    Face(lower + next, upper + current, upper + next);
                }
                var last = (uint)(1 + (rings - 1) * Segments);
                Face(last + current, top, last + next);
            }
            return new NavigationAsset { ID = id, Vertices = vertices.ToArray(), Triangles = triangles.ToArray() };
        }

        /// <summary>Rejects invalid authored dimensions before canonical buffer selection.</summary>
        private static void Positive(float value)
        {
            if (float.IsNaN(value) || float.IsInfinity(value) || value <= 0)
                throw new InvalidOperationException("Navigation primitive dimensions must be finite and positive.");
        }
    }
}
