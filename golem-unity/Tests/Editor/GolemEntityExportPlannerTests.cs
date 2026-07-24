using System.Collections.Generic;
using GolemEngine.Unity.Editor;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemEntityExportPlannerTests
    {
        [Test]
        public void ApplyEntityCandidate_DropsBothSidesOfDuplicateEntityNames()
        {
            var desired = new List<GolemScribeArtifactRecord>();
            var registry = new Dictionary<string, GameObject>();
            var sources = new Dictionary<string, string>();
            var conflicts = new HashSet<string>();
            var errors = new List<string>();
            var a = new GameObject("a");
            var b = new GameObject("b");
            try
            {
                var first = new GolemScribeArtifactRecord
                {
                    Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                    Entity = "Player",
                    Path = "schemas/entities/player.yaml",
                    SourceGuid = "11111111111111111111111111111111",
                    PendingContent = "x"
                };
                var second = new GolemScribeArtifactRecord
                {
                    Kind = GolemScribeConstants.ArtifactKindEntitySchema,
                    Entity = "Player",
                    Path = "schemas/entities/player.yaml",
                    SourceGuid = "22222222222222222222222222222222",
                    PendingContent = "y"
                };

                GolemEntityExporter.ApplyEntityCandidate(
                    "Player", "Assets/A.prefab", first, a, sources, conflicts, desired, registry, errors);
                GolemEntityExporter.ApplyEntityCandidate(
                    "Player", "Assets/B.prefab", second, b, sources, conflicts, desired, registry, errors);

                Assert.That(desired, Is.Empty);
                Assert.That(registry, Is.Empty);
                Assert.That(conflicts, Does.Contain("Player"));
                Assert.That(errors, Has.Some.Contain("multiple prefabs"));
            }
            finally
            {
                Object.DestroyImmediate(a);
                Object.DestroyImmediate(b);
            }
        }
    }
}
