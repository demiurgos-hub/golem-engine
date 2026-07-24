using System;
using System.Collections.Generic;
using System.IO;
using GolemEngine.Unity.Editor;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemFootprintExportTests
    {
        private readonly List<UnityEngine.Object> _owned = new List<UnityEngine.Object>();

        [TearDown]
        public void TearDown()
        {
            foreach (var obj in _owned)
            {
                if (obj != null)
                {
                    UnityEngine.Object.DestroyImmediate(obj);
                }
            }

            _owned.Clear();
            GolemScribeArtifacts.AfterWriteHookForTests = null;
        }

        [Test]
        public void BuildYaml_MatchesSharedGoGolden2D_ByteForByte()
        {
            var wall = new GolemFootprintModel
            {
                Guid = "0123456789abcdef0123456789abcdef",
                Name = "Wall",
                AssetPath = "Assets/Prefabs/Environment/Wall.prefab",
                Alias = "wall"
            };
            wall.Shapes.Add(new GolemFootprintShapeModel
            {
                Type = "aabb", W = 2f, H = 1f, OffsetX = 0f, OffsetY = 0.5f, Trigger = false, Layer = "Default"
            });
            wall.Shapes.Add(new GolemFootprintShapeModel
            {
                Type = "circle", R = 0.25f, OffsetX = 1f, OffsetY = 0f, Trigger = true, Layer = "Trigger"
            });

            var cap = new GolemFootprintModel
            {
                Guid = "fedcba9876543210fedcba9876543210",
                Name = "Wall",
                AssetPath = "Assets/Prefabs/Environment/WallCap.prefab"
            };
            cap.Shapes.Add(new GolemFootprintShapeModel
            {
                Type = "aabb", W = 1f, H = 1f, OffsetX = -0.5f, OffsetY = 0f, Trigger = false, Layer = "Default"
            });

            // Reverse input order; emission must sort by GUID.
            var yaml = GolemFootprintYamlBuilder.BuildYaml(2, new[] { cap, wall });
            Assert.That(yaml, Is.EqualTo(ReadSharedGolden("golden_footprints.golem.yaml")));
        }

        [Test]
        public void BuildYaml_MatchesSharedGoGolden3D_ByteForByte()
        {
            var crate = new GolemFootprintModel
            {
                Guid = "aabbccddeeff00112233445566778899",
                Name = "Crate",
                AssetPath = "Assets/Prefabs/Props/Crate.prefab",
                Alias = "crate"
            };
            crate.Shapes.Add(new GolemFootprintShapeModel
            {
                Type = "aabb", W = 2f, H = 1f, D = 3f,
                OffsetX = 0f, OffsetY = 0.5f, OffsetZ = 0f,
                Trigger = false, Layer = "Default"
            });
            crate.Shapes.Add(new GolemFootprintShapeModel
            {
                Type = "sphere", R = 0.5f,
                OffsetX = 0f, OffsetY = 1f, OffsetZ = 0.25f,
                Trigger = true, Layer = "Trigger"
            });

            var yaml = GolemFootprintYamlBuilder.BuildYaml(3, new[] { crate });
            Assert.That(yaml, Is.EqualTo(ReadSharedGolden("golden_footprints_3d.golem.yaml")));
        }

        [Test]
        public void Convert_NestedOffsetAndInactiveChild_EnabledColliderIncluded()
        {
            var root = CreateRoot("Root");
            var active = CreateChild(root, "Active", new Vector3(1f, 0f, 0f));
            var box = active.AddComponent<BoxCollider2D>();
            box.size = new Vector2(2f, 1f);
            box.offset = new Vector2(0f, 0.5f);

            var inactive = CreateChild(root, "Inactive", new Vector3(0f, 2f, 0f));
            inactive.SetActive(false);
            var circle = inactive.AddComponent<CircleCollider2D>();
            circle.radius = 0.25f;
            circle.isTrigger = true;
            Assert.That(circle.enabled, Is.True);

            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 2, ~0, out var shapes, out var errors),
                Is.True,
                string.Join("; ", errors));
            Assert.That(shapes, Has.Count.EqualTo(2));
            Assert.That(shapes[0].Type, Is.EqualTo("aabb"));
            Assert.That(shapes[0].OffsetX, Is.EqualTo(1f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[0].OffsetY, Is.EqualTo(0.5f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[0].W, Is.EqualTo(2f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[0].H, Is.EqualTo(1f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[1].Type, Is.EqualTo("circle"));
            Assert.That(shapes[1].OffsetX, Is.EqualTo(0f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[1].OffsetY, Is.EqualTo(2f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[1].Trigger, Is.True);
        }

        [Test]
        public void Convert_DisabledColliderComponent_Skipped()
        {
            var root = CreateRoot("Root");
            var enabledBox = root.AddComponent<BoxCollider2D>();
            enabledBox.size = Vector2.one;

            var disabledChild = CreateChild(root, "Disabled", new Vector3(3f, 0f, 0f));
            var disabledCircle = disabledChild.AddComponent<CircleCollider2D>();
            disabledCircle.radius = 1f;
            disabledCircle.enabled = false;

            // Disabled unsupported collider must not fail export.
            var disabledPoly = CreateChild(root, "DisabledPoly", Vector3.zero);
            var poly = disabledPoly.AddComponent<PolygonCollider2D>();
            poly.SetPath(0, new[] { Vector2.zero, Vector2.right, Vector2.up });
            poly.enabled = false;

            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 2, ~0, out var shapes, out var errors),
                Is.True,
                string.Join("; ", errors));
            Assert.That(shapes, Has.Count.EqualTo(1));
            Assert.That(shapes[0].Type, Is.EqualTo("aabb"));
        }

        [Test]
        public void Convert_LayerMask_IgnoresOutsideLayers_RejectsUnsupportedInside()
        {
            var root = CreateRoot("Root");
            var included = CreateChild(root, "Included", Vector3.zero);
            included.layer = 0; // Default
            included.AddComponent<BoxCollider2D>().size = new Vector2(1f, 1f);

            var excluded = CreateChild(root, "Excluded", Vector3.zero);
            excluded.layer = 2; // Ignore Raycast (still named)
            excluded.AddComponent<PolygonCollider2D>()
                .SetPath(0, new[] { Vector2.zero, Vector2.right, Vector2.up });

            var onlyDefault = 1 << 0;
            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 2, onlyDefault, out var shapes, out var errors),
                Is.True,
                string.Join("; ", errors));
            Assert.That(shapes, Has.Count.EqualTo(1));

            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 2, ~0, out _, out var rejectErrors),
                Is.False);
            Assert.That(rejectErrors, Has.Some.Contain("unsupported"));
        }

        [Test]
        public void Convert_DimensionFilter_RejectsWrongDimensionCollider()
        {
            var root = CreateRoot("Root");
            root.AddComponent<BoxCollider>().size = Vector3.one;

            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 2, ~0, out _, out var errors),
                Is.False);
            Assert.That(errors, Has.Some.Contain("dimensions=2"));
        }

        [Test]
        public void Convert_QuarterTurnAndNonUniformBoxScale_Supported()
        {
            var root = CreateRoot("Root");
            var child = CreateChild(root, "Box", Vector3.zero);
            child.transform.localRotation = Quaternion.Euler(0f, 0f, 90f);
            child.transform.localScale = new Vector3(2f, 3f, 1f);
            var box = child.AddComponent<BoxCollider2D>();
            box.size = new Vector2(1f, 1f);

            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 2, ~0, out var shapes, out var errors),
                Is.True,
                string.Join("; ", errors));
            Assert.That(shapes, Has.Count.EqualTo(1));
            // Local (1,1) with scale (2,3) then 90° Z => root full extents (3,2)
            Assert.That(shapes[0].W, Is.EqualTo(3f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[0].H, Is.EqualTo(2f).Within(GolemFootprintConverter.Tolerance));
        }

        [Test]
        public void Convert_CircleNonUniformScale_Rejected()
        {
            var root = CreateRoot("Root");
            var child = CreateChild(root, "Circle", Vector3.zero);
            child.transform.localScale = new Vector3(2f, 3f, 1f);
            child.AddComponent<CircleCollider2D>().radius = 1f;

            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 2, ~0, out _, out var errors),
                Is.False);
            Assert.That(errors, Has.Some.Contain("uniform XY"));
        }

        [Test]
        public void Convert_ArbitraryRotation_Rejected()
        {
            var root = CreateRoot("Root");
            var child = CreateChild(root, "Box", Vector3.zero);
            child.transform.localRotation = Quaternion.Euler(0f, 0f, 45f);
            child.AddComponent<BoxCollider2D>().size = Vector2.one;

            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 2, ~0, out _, out var errors),
                Is.False);
            Assert.That(errors, Has.Some.Contain("quarter turn"));
        }

        [Test]
        public void Convert_3D_YawQuarterTurnAndSphereUniformScale()
        {
            var root = CreateRoot("Root3D");
            var boxChild = CreateChild(root, "Box", new Vector3(0f, 0.5f, 0f));
            boxChild.transform.localRotation = Quaternion.Euler(0f, 90f, 0f);
            var box = boxChild.AddComponent<BoxCollider>();
            box.size = new Vector3(2f, 1f, 3f);
            box.center = Vector3.zero;

            var sphereChild = CreateChild(root, "Sphere", new Vector3(0f, 1f, 0.25f));
            sphereChild.transform.localScale = new Vector3(2f, 2f, 2f);
            var sphere = sphereChild.AddComponent<SphereCollider>();
            sphere.radius = 0.25f;
            sphere.isTrigger = true;

            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 3, ~0, out var shapes, out var errors),
                Is.True,
                string.Join("; ", errors));
            Assert.That(shapes, Has.Count.EqualTo(2));
            Assert.That(shapes[0].Type, Is.EqualTo("aabb"));
            // size (2,1,3) yaw 90: X/Z swap => (3,1,2)
            Assert.That(shapes[0].W, Is.EqualTo(3f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[0].H, Is.EqualTo(1f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[0].D, Is.EqualTo(2f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[1].Type, Is.EqualTo("sphere"));
            Assert.That(shapes[1].R, Is.EqualTo(0.5f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[1].OffsetY, Is.EqualTo(1f).Within(GolemFootprintConverter.Tolerance));
            Assert.That(shapes[1].OffsetZ, Is.EqualTo(0.25f).Within(GolemFootprintConverter.Tolerance));
        }

        [Test]
        public void Convert_3D_PitchRejected()
        {
            var root = CreateRoot("Root3D");
            var child = CreateChild(root, "Box", Vector3.zero);
            child.transform.localRotation = Quaternion.Euler(45f, 0f, 0f);
            child.AddComponent<BoxCollider>().size = Vector3.one;

            Assert.That(
                GolemFootprintConverter.TryConvert(root.transform, 3, ~0, out _, out var errors),
                Is.False);
            Assert.That(errors, Has.Some.Contain("yaw-only"));
        }

        [Test]
        public void ApplyFootprintCandidate_DuplicateAlias_DropsBoth()
        {
            var models = new List<GolemFootprintModel>();
            var aliases = new Dictionary<string, string>();
            var conflicts = new HashSet<string>();
            var errors = new List<string>();

            GolemFootprintExporter.ApplyFootprintCandidate(
                new GolemFootprintModel { Guid = "a", Alias = "wall" },
                aliases, conflicts, models, errors);
            GolemFootprintExporter.ApplyFootprintCandidate(
                new GolemFootprintModel { Guid = "b", Alias = "wall" },
                aliases, conflicts, models, errors);

            Assert.That(models, Is.Empty);
            Assert.That(conflicts, Does.Contain("wall"));
            Assert.That(errors, Has.Some.Contain("multiple prefabs"));
        }

        [Test]
        public void ReconcileKind_DeletesFootprintFileWhenNoMarkedPrefabs_PreservesOtherKinds()
        {
            var root = CreateTempRoot();
            try
            {
                var footPath = "footprints.golem.yaml";
                var footYaml = GolemFootprintYamlBuilder.BuildYaml(2, new[]
                {
                    new GolemFootprintModel
                    {
                        Guid = "0123456789abcdef0123456789abcdef",
                        Name = "Wall",
                        AssetPath = "Assets/Wall.prefab"
                    }
                });
                WriteOwned(root, footPath, footYaml);

                var entityPath = "schemas/entities/player.yaml";
                var entityYaml = GolemYamlWriter.BuildDocument(new[] { "entity: Player", "vars:", "  {}" });
                WriteOwned(root, entityPath, entityYaml);

                var previous = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindFootprint,
                        SourceGuid = GolemScribeConstants.FootprintAggregateSourceGuid,
                        Path = footPath,
                        Hash = GolemScribeManifest.ComputeContentHash(footYaml)
                    },
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                        SourceGuid = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
                        Entity = "Player",
                        Path = entityPath,
                        Hash = GolemScribeManifest.ComputeContentHash(entityYaml)
                    }
                };

                var result = GolemScribeArtifacts.ReconcileKind(
                    root,
                    GolemScribeConstants.ArtifactKindFootprint,
                    previous,
                    new List<GolemScribeArtifactRecord>());

                Assert.That(result.Errors, Is.Empty);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, footPath)), Is.False);
                Assert.That(File.Exists(GolemScribeArtifacts.ToAbsolutePath(root, entityPath)), Is.True);
                Assert.That(result.FootprintBytesChanged, Is.True);
                Assert.That(result.EntitySchemaBytesChanged, Is.False);
                Assert.That(result.ManifestRecords, Has.Count.EqualTo(1));
                Assert.That(result.ManifestRecords[0].Kind, Is.EqualTo(GolemScribeConstants.ArtifactKindEntitySchema));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ReconcileKind_UnchangedFootprintDoesNotRewrite()
        {
            var root = CreateTempRoot();
            try
            {
                var footPath = "footprints.golem.yaml";
                var yaml = GolemFootprintYamlBuilder.BuildYaml(2, new[]
                {
                    new GolemFootprintModel
                    {
                        Guid = "0123456789abcdef0123456789abcdef",
                        Name = "Wall",
                        AssetPath = "Assets/Wall.prefab"
                    }
                });
                WriteOwned(root, footPath, yaml);
                var absolute = GolemScribeArtifacts.ToAbsolutePath(root, footPath);
                var beforeWriteTime = File.GetLastWriteTimeUtc(absolute);

                var desired = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindFootprint,
                        SourceGuid = GolemScribeConstants.FootprintAggregateSourceGuid,
                        Path = footPath,
                        PendingContent = yaml,
                        Hash = GolemScribeManifest.ComputeContentHash(yaml)
                    }
                };
                var previous = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindFootprint,
                        SourceGuid = GolemScribeConstants.FootprintAggregateSourceGuid,
                        Path = footPath,
                        Hash = GolemScribeManifest.ComputeContentHash(yaml)
                    }
                };

                System.Threading.Thread.Sleep(20);
                var result = GolemScribeArtifacts.ReconcileKind(
                    root, GolemScribeConstants.ArtifactKindFootprint, previous, desired);

                Assert.That(result.Errors, Is.Empty);
                Assert.That(result.FootprintBytesChanged, Is.False);
                Assert.That(File.GetLastWriteTimeUtc(absolute), Is.EqualTo(beforeWriteTime));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        [Test]
        public void ShouldAutoBake_IgnoresFootprintByteChanges()
        {
            // Footprint exporter never feeds ShouldAutoBake; only entity/catalog schema flags matter.
            Assert.That(
                GolemScribeScheduler.ShouldAutoBake(
                    entityHasErrors: false,
                    entitySchemaBytesChanged: false,
                    catalogHasErrors: false,
                    catalogSchemaBytesChanged: false),
                Is.False);
        }

        [Test]
        public void TryParse_RoundTripsGolden()
        {
            var golden = ReadSharedGolden("golden_footprints.golem.yaml");
            Assert.That(GolemFootprintYamlBuilder.TryParse(golden, out var dims, out var footprints), Is.True);
            Assert.That(dims, Is.EqualTo(2));
            Assert.That(footprints, Has.Count.EqualTo(2));
            Assert.That(GolemFootprintYamlBuilder.BuildYaml(dims, footprints), Is.EqualTo(golden));
        }

        [Test]
        public void BuildYaml_EmptyFootprints_RoundTrips()
        {
            var yaml = GolemFootprintYamlBuilder.BuildYaml(2, Array.Empty<GolemFootprintModel>());
            Assert.That(yaml, Does.Contain("footprints: {}"));
            Assert.That(GolemFootprintYamlBuilder.TryParse(yaml, out var dims, out var footprints), Is.True);
            Assert.That(dims, Is.EqualTo(2));
            Assert.That(footprints, Is.Empty);
            Assert.That(GolemFootprintYamlBuilder.BuildYaml(dims, footprints), Is.EqualTo(yaml));

            // Older two-line empty map still parses and normalizes to the single-line form.
            var legacy = GolemYamlWriter.BuildDocument(new[]
            {
                "version: 1",
                "dimensions: 3",
                "footprints:",
                "  {}"
            });
            Assert.That(GolemFootprintYamlBuilder.TryParse(legacy, out var dims3, out var empty3), Is.True);
            Assert.That(dims3, Is.EqualTo(3));
            Assert.That(empty3, Is.Empty);
            Assert.That(
                GolemFootprintYamlBuilder.BuildYaml(dims3, empty3),
                Is.EqualTo(GolemFootprintYamlBuilder.BuildYaml(3, Array.Empty<GolemFootprintModel>())));
        }

        [Test]
        public void TryGetFootprintMarker_RequiresExactlyOneOnRoot()
        {
            var root = CreateRoot("Root");
            Assert.That(
                GolemFootprintExporter.TryGetFootprintMarker(root, "Assets/Root.prefab", out var none, out var noneError),
                Is.True);
            Assert.That(none, Is.Null);
            Assert.That(noneError, Is.Null);

            var marker = root.AddComponent<GolemFootprint>();
            Assert.That(
                GolemFootprintExporter.TryGetFootprintMarker(root, "Assets/Root.prefab", out var found, out var okError),
                Is.True);
            Assert.That(found, Is.SameAs(marker));
            Assert.That(okError, Is.Null);

            var child = CreateChild(root, "Nested", Vector3.zero);
            child.AddComponent<GolemFootprint>();
            Assert.That(
                GolemFootprintExporter.TryGetFootprintMarker(root, "Assets/Root.prefab", out _, out var nestedError),
                Is.False);
            Assert.That(nestedError, Does.Contain("nested child"));
        }

        [Test]
        public void TryGetFootprintMarker_NestedOnlyMarker_Rejected()
        {
            var root = CreateRoot("Root");
            var child = CreateChild(root, "Nested", Vector3.zero);
            child.SetActive(false);
            child.AddComponent<GolemFootprint>();

            Assert.That(
                GolemFootprintExporter.TryGetFootprintMarker(root, "Assets/Root.prefab", out var marker, out var error),
                Is.False);
            Assert.That(marker, Is.Null);
            Assert.That(error, Does.Contain("nested child"));
        }

        [Test]
        public void TryFindPriorFootprintRecord_MatchesUnusualRelativePaths()
        {
            var root = CreateTempRoot();
            try
            {
                var previous = new List<GolemScribeArtifactRecord>
                {
                    new GolemScribeArtifactRecord
                    {
                        Kind = GolemScribeConstants.ArtifactKindFootprint,
                        SourceGuid = GolemScribeConstants.FootprintAggregateSourceGuid,
                        Path = "./out//footprints.golem.yaml",
                        Hash = "abc"
                    }
                };

                Assert.That(
                    GolemFootprintExporter.TryFindPriorFootprintRecord(
                        previous,
                        root,
                        "out/footprints.golem.yaml",
                        out var record,
                        out var normalized,
                        out var error),
                    Is.True,
                    error);
                Assert.That(record, Is.Not.Null);
                Assert.That(normalized, Is.EqualTo("out/footprints.golem.yaml"));
                Assert.That(
                    GolemFootprintExporter.TryFindPriorFootprintRecord(
                        previous,
                        root,
                        ".//out/./footprints.golem.yaml",
                        out _,
                        out var normalizedAgain,
                        out _),
                    Is.True);
                Assert.That(normalizedAgain, Is.EqualTo("out/footprints.golem.yaml"));
            }
            finally
            {
                DeleteTempRoot(root);
            }
        }

        private GameObject CreateRoot(string name)
        {
            var go = new GameObject(name);
            _owned.Add(go);
            return go;
        }

        private GameObject CreateChild(GameObject parent, string name, Vector3 localPosition)
        {
            var go = new GameObject(name);
            go.transform.SetParent(parent.transform, false);
            go.transform.localPosition = localPosition;
            go.transform.localRotation = Quaternion.identity;
            go.transform.localScale = Vector3.one;
            _owned.Add(go);
            return go;
        }

        private static string ReadSharedGolden(string fileName)
        {
            Assert.That(TryFindSharedGolden(fileName, out var path), Is.True, "shared golden not found: " + fileName);
            return File.ReadAllText(path).Replace("\r\n", "\n");
        }

        private static bool TryFindSharedGolden(string fileName, out string absolutePath)
        {
            absolutePath = null;
            var start = Path.GetFullPath(Application.dataPath);
            var dir = new DirectoryInfo(start);
            for (var i = 0; i < 12 && dir != null; i++)
            {
                var candidate = Path.Combine(dir.FullName, "golem", "footprint", "testdata", fileName);
                if (File.Exists(candidate))
                {
                    absolutePath = candidate;
                    return true;
                }

                // Packaged UPM layout: package sits under golem-engine/artifacts/upm/...
                var alt = Path.Combine(dir.FullName, "golem-engine", "golem", "footprint", "testdata", fileName);
                if (File.Exists(alt))
                {
                    absolutePath = alt;
                    return true;
                }

                dir = dir.Parent;
            }

            return false;
        }

        private static void WriteOwned(string root, string relative, string content)
        {
            var absolute = GolemScribeArtifacts.ToAbsolutePath(root, relative);
            Directory.CreateDirectory(Path.GetDirectoryName(absolute));
            File.WriteAllText(absolute, content);
        }

        private static string CreateTempRoot()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-scribe-footprint-" + Path.GetRandomFileName());
            Directory.CreateDirectory(root);
            return root;
        }

        private static void DeleteTempRoot(string root)
        {
            if (Directory.Exists(root))
            {
                Directory.Delete(root, true);
            }
        }
    }
}
