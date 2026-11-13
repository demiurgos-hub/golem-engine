using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemContentDocumentTests
    {
        internal static GolemContentType[] SampleTypes()
        {
            var stats = new GolemContentType { Name = "Stats" };
            stats.Fields.Add(new GolemContentField("speed", "float32"));
            stats.Fields.Add(new GolemContentField("armor", "int32").WithDefault(0));
            var profile = new GolemContentType { Name = "MovementProfile", Definition = true };
            profile.Fields.Add(new GolemContentField("clearance", "float32"));
            var flags = new GolemContentType { Name = "Traits", Kind = "flags" };
            flags.Values.AddRange(new[] { "Hostile", "Grounded" });
            var monster = new GolemContentType { Name = "Monster", Definition = true, Entity = "Monster" };
            monster.Fields.AddRange(new[]
            {
                new GolemContentField("health", "int32"), new GolemContentField("stats", "Stats"),
                new GolemContentField("movement", "ref:MovementProfile"), new GolemContentField("traits", "Traits"),
                new GolemContentField("tags", "[]string"), new GolemContentField("note", "string", true)
            });
            return new[] { monster, stats, profile, flags };
        }

        internal static GolemContentDefinition[] SampleDefinitions()
        {
            var profile = new GolemContentDefinition
            {
                Id = "demo/MovementProfile/ground", Type = "MovementProfile",
                Source = new GolemContentSource { Guid = new string('a', 32), LocalId = "11400000", Path = "Assets/Content/ground.asset" },
                Values = GolemContentDocument.Object("clearance", 0.5f)
            };
            var scout = new GolemContentDefinition
            {
                Id = "demo/Monster/scout", Type = "Monster", Factory = "monster",
                Source = new GolemContentSource { Guid = new string('b', 32), LocalId = "100100000", Path = "Assets/Content/scout.prefab" },
                Values = GolemContentDocument.Object("health", 80, "stats", GolemContentDocument.Object("speed", 4.5f),
                    "movement", new GolemContentReference(profile.Source.Key, "MovementProfile"), "traits", new[] { "Hostile", "Grounded" }, "tags", new[] { "small" })
            };
            var guard = new GolemContentDefinition
            {
                Id = "demo/Monster/guard", Type = "Monster", Factory = "monster",
                Source = new GolemContentSource { Guid = new string('c', 32), LocalId = "100100000", Path = "Assets/Content/guard.prefab" },
                Values = GolemContentDocument.Object("health", 160, "stats", GolemContentDocument.Object("speed", 3f, "armor", 5),
                    "movement", new GolemContentReference(profile.Source.Key, "MovementProfile"), "traits", new[] { "Grounded" }, "tags", Array.Empty<string>(), "note", null)
            };
            return new[] { scout, guard, profile };
        }

        [Test]
        public void SampleIsDeterministicAndMatchesSharedGoFixtures()
        {
            var prepared = GolemContentDocument.Prepare(SampleTypes(), SampleDefinitions());
            var reversed = GolemContentDocument.Prepare(SampleTypes().Reverse(), SampleDefinitions().Reverse());
            Assert.That(reversed.SchemaJson, Is.EqualTo(prepared.SchemaJson));
            Assert.That(reversed.BundleJson, Is.EqualTo(prepared.BundleJson));
            Assert.That(prepared.EntityTypes, Is.EqualTo(new[] { "Monster" }));
            Assert.That(prepared.BundleJson, Does.Contain("\"armor\":0"));
            Assert.That(prepared.BundleJson, Does.Contain("\"note\":null"));
            Assert.That(prepared.BundleJson, Does.Not.Contain("schema_hash"));
            Assert.That(GolemContentDocument.IsOwned(prepared.BundleJson), Is.True);
            var writeFixtures = Environment.GetEnvironmentVariable("GOLEM_WRITE_CONTENT_FIXTURES");
            if (!string.IsNullOrEmpty(writeFixtures))
            {
                Directory.CreateDirectory(writeFixtures);
                File.WriteAllText(Path.Combine(writeFixtures, "content-schema.json"), prepared.SchemaJson);
                File.WriteAllText(Path.Combine(writeFixtures, "content-source.json"), prepared.BundleJson);
            }
            var fixtureRoot = string.IsNullOrEmpty(writeFixtures) ? Path.Combine(TestContext.CurrentContext.TestDirectory, "ContentFixtures") : writeFixtures;
#if UNITY_EDITOR
            fixtureRoot = Path.Combine(UnityEditor.PackageManager.PackageInfo.FindForAssembly(typeof(GolemContentDocument).Assembly).resolvedPath, "Tests/Fixtures");
#endif
            Assert.That(File.ReadAllText(Path.Combine(fixtureRoot, "content-schema.json")), Is.EqualTo(prepared.SchemaJson));
            Assert.That(File.ReadAllText(Path.Combine(fixtureRoot, "content-source.json")), Is.EqualTo(prepared.BundleJson));
        }

        [Test]
        public void MissingReferenceDoesNotExpandTheSelectedSet()
        {
            var rows = SampleDefinitions();
            var error = Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(SampleTypes(), rows.Take(2)));
            Assert.That(error.Message, Does.Contain("guard.prefab").And.Contain("movement").And.Contain("outside explicit roots"));
        }

        [Test]
        public void DuplicateIdentityAndWrongTargetTypeFail()
        {
            var rows = SampleDefinitions(); rows[1].Id = rows[0].Id;
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(SampleTypes(), rows)).Message, Does.Contain("duplicate definition identity"));
            rows = SampleDefinitions(); rows[0].Values["movement"] = rows[1].Id;
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(SampleTypes(), rows)).Message, Does.Contain("reference expects MovementProfile"));
        }

        [TestCase("health", "80")]
        [TestCase("health", 1.0)]
        [TestCase("health", 1.5)]
        [TestCase("health", 2147483648L)]
        [TestCase("health", null)]
        [TestCase("unexpected", 1)]
        public void MalformedFieldsFailWithSourceAndField(string key, object value)
        {
            var rows = SampleDefinitions(); rows[0].Values[key] = value;
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(SampleTypes(), rows)).Message,
                Does.Contain("scout.prefab").And.Contain(key));
        }

        [Test]
        public void ExplicitEmptyAndNullDoNotTakeDefaults()
        {
            var types = SampleTypes(); types[0].Fields.First(f => f.Name == "note").WithDefault("default");
            types[0].Fields.First(f => f.Name == "tags").WithDefault(new[] { "default" });
            var prepared = GolemContentDocument.Prepare(types, SampleDefinitions());
            Assert.That(prepared.BundleJson, Does.Contain("\"note\":null").And.Contain("\"note\":\"default\"").And.Contain("\"tags\":[]"));
        }

        [Test]
        public void ArchetypesRequireBoundedFactoryKeysBeforeExport()
        {
            var rows = SampleDefinitions(); rows[0].Factory = null;
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(SampleTypes(), rows)).Message,
                Does.Contain("scout.prefab").And.Contain("archetype requires a factory key"));
            rows[0].Factory = new string('\u00e9', 257);
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(SampleTypes(), rows)).Message,
                Does.Contain("factory key exceeds 512 bytes"));
            var import = "{\"version\":1,\"definitions\":[{\"id\":\"demo/Monster/imported\",\"type\":\"Monster\",\"values\":{}}]}";
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(SampleTypes(), SampleDefinitions(),
                new[] { new GolemContentImport { Hash = new string('d', 64), BundleJson = import } })).Message,
                Does.Contain("demo/Monster/imported").And.Contain("archetype requires a factory key"));
        }

        [Test]
        public void ImportedJsonPreservesWideIntegersAndFiniteFloat64Extremes()
        {
            var parsed = (IDictionary<string, object>)GolemContentJson.Parse(
                "{\"minimum\":-9223372036854775808,\"maximum\":18446744073709551615,\"tiny\":1e-100,\"huge\":1e100}");
            Assert.That(parsed["minimum"], Is.EqualTo((decimal)long.MinValue));
            Assert.That(parsed["maximum"], Is.EqualTo((decimal)ulong.MaxValue));
            Assert.That(parsed["tiny"], Is.TypeOf<double>().And.EqualTo(1e-100));
            Assert.That(parsed["huge"], Is.TypeOf<double>().And.EqualTo(1e100));
            var roundtrip = (IDictionary<string, object>)GolemContentJson.Parse(GolemContentJson.Write(parsed));
            Assert.That(roundtrip["tiny"], Is.EqualTo(1e-100));
            Assert.That(roundtrip["maximum"], Is.EqualTo((decimal)ulong.MaxValue));
        }

        [Test]
        public void OptionalReferenceNullDoesNotPermitNullListElements()
        {
            var types = SampleTypes();
            var field = types[0].Fields.First(f => f.Name == "movement"); field.Optional = true;
            var rows = SampleDefinitions(); rows[0].Values["movement"] = null;
            Assert.That(GolemContentDocument.Prepare(types, rows).BundleJson, Does.Contain("\"movement\":null"));
            field.Type = "[]ref:MovementProfile";
            rows[0].Values["movement"] = new object[] { null };
            rows[1].Values["movement"] = Array.Empty<object>();
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(types, rows)).Message,
                Does.Contain("movement[0]").And.Contain("required value is null"));
        }

        [Test]
        public void ReferenceDefaultsResolveOnlyWhenAppliedToASelectedDefinition()
        {
            var types = SampleTypes();
            var field = types[0].Fields.First(f => f.Name == "movement");
            field.WithDefault("demo/MovementProfile/unused");
            var rows = SampleDefinitions();
            Assert.DoesNotThrow(() => GolemContentDocument.Prepare(types, rows));
            rows[0].Values.Remove("movement");
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(types, rows)).Message,
                Does.Contain("scout.prefab").And.Contain("missing referenced definition demo/MovementProfile/unused"));
            field.Default = "demo/Monster/guard";
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(types, SampleDefinitions())).Message,
                Does.Contain("Monster.movement.default").And.Contain("reference expects MovementProfile"));
        }

        [Test]
        public void TypedCyclesAreFiniteButInlineCyclesFail()
        {
            var type = new GolemContentType { Name = "Node", Definition = true };
            type.Fields.Add(new GolemContentField("next", "ref:Node"));
            var rows = new[]
            {
                new GolemContentDefinition { Id = "demo/Node/a", Type = "Node", Values = GolemContentDocument.Object("next", "demo/Node/b") },
                new GolemContentDefinition { Id = "demo/Node/b", Type = "Node", Values = GolemContentDocument.Object("next", "demo/Node/a") }
            };
            Assert.DoesNotThrow(() => GolemContentDocument.Prepare(new[] { type }, rows));
            type.Definition = false;
            type.Fields[0].Type = "[]Node";
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(new[] { type }, rows)).Message, Does.Contain("inline record cycle"));
        }

        [Test]
        public void SubassetsKeepIndependentLocalIds()
        {
            var rows = SampleDefinitions(); rows[1].Source.Guid = rows[0].Source.Guid; rows[1].Source.LocalId = "100100001";
            Assert.DoesNotThrow(() => GolemContentDocument.Prepare(SampleTypes(), rows));
            rows[1].Source.LocalId = rows[0].Source.LocalId;
            Assert.That(Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(SampleTypes(), rows)).Message, Does.Contain("source is mapped more than once"));
        }

        [Test]
        public void SharedRecordDependenciesAreValidatedWithoutExpandingEveryPath()
        {
            var types = new List<GolemContentType> { new GolemContentType { Name = "Layer0" } };
            for (var i = 1; i < 25; i++)
            {
                var type = new GolemContentType { Name = "Layer" + i };
                type.Fields.Add(new GolemContentField("left", "Layer" + (i - 1)));
                type.Fields.Add(new GolemContentField("right", "Layer" + (i - 1)));
                types.Add(type);
            }
            Assert.DoesNotThrow(() => GolemContentDocument.Prepare(types, Array.Empty<GolemContentDefinition>()));
        }

        [Test]
        public void ImportsAreExplicitAndDoNotFollowTheirImports()
        {
            var rows = SampleDefinitions();
            var imported = GolemContentDocument.Prepare(SampleTypes(), new[] { rows[2] });
            var prepared = GolemContentDocument.Prepare(SampleTypes(), rows.Take(2), new[] { new GolemContentImport { Hash = new string('d', 64), BundleJson = imported.BundleJson } });
            Assert.That(prepared.BundleJson, Does.Contain("\"imports\":[{\"hash\":\"" + new string('d', 64)));
            Assert.That(prepared.BundleJson, Does.Not.Contain("\"clearance\""));
        }

        [TestCase("{\"definitions\":[],\"definitions\":[],\"version\":1}")]
        [TestCase("{\"definitions\":[],\"version\":01}")]
        [TestCase("{\"definitions\":[],\"version\":1} trailing")]
        public void MalformedImportedJsonIsRejected(string json)
        {
            Assert.Throws<InvalidOperationException>(() => GolemContentDocument.Prepare(SampleTypes(), SampleDefinitions(),
                new[] { new GolemContentImport { Hash = new string('d', 64), BundleJson = json } }));
        }

        [Test]
        public void SelectedContentTransactionPreservesOtherSetsAndRollsBack()
        {
            var root = Path.Combine(Path.GetTempPath(), "golem-content-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(root);
            try
            {
                var prepared = GolemContentDocument.Prepare(SampleTypes(), SampleDefinitions());
                var records = new[] { Record("one", "one.schema.json", prepared.SchemaJson), Record("one", "one.bundle.json", prepared.BundleJson) };
                Assert.That(GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindContentSource, "one", Array.Empty<GolemScribeArtifactRecord>(), records).Errors, Is.Empty);
                var previous = GolemScribeManifest.Load(root);
                var second = new[] { Record("two", "two.bundle.json", prepared.BundleJson) };
                Assert.That(GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindContentSource, "two", previous, second).Errors, Is.Empty);
                var baseline = File.ReadAllText(GolemScribeManifest.ManifestPath(root));
                records[0].PendingContent = prepared.SchemaJson + " ";
                GolemScribeArtifacts.AfterWriteHookForTests = _ => throw new IOException("injected failure");
                var failed = GolemScribeArtifacts.ReconcileSource(root, GolemScribeConstants.ArtifactKindContentSource, "one", GolemScribeManifest.Load(root), records);
                Assert.That(failed.Errors, Is.Not.Empty);
                Assert.That(File.ReadAllText(Path.Combine(root, "one.schema.json")), Is.EqualTo(prepared.SchemaJson));
                Assert.That(File.ReadAllText(GolemScribeManifest.ManifestPath(root)), Is.EqualTo(baseline));
                Assert.That(File.Exists(Path.Combine(root, "two.bundle.json")), Is.True);
            }
            finally { GolemScribeArtifacts.AfterWriteHookForTests = null; Directory.Delete(root, true); }
        }

        private static GolemScribeArtifactRecord Record(string source, string path, string content) => new GolemScribeArtifactRecord
        { Kind = GolemScribeConstants.ArtifactKindContentSource, SourceGuid = source, Entity = "", Path = path, PendingContent = content, Hash = GolemScribeManifest.ComputeContentHash(content) };
    }
}
