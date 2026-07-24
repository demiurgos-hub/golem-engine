using System.Collections.Generic;
using GolemEngine.Unity.Editor;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemCatalogSchemaTests
    {
        [GolemCatalog("Id")]
        private sealed class MonsterDefinition : ScriptableObject
        {
            [GolemField(1)]
            public string Id;

            [GolemField(2)]
            public int Health;

            [GolemAssetRef(3)]
            public GameObject Prefab;

            [GolemField(4)]
            public bool Boss;

            public float ignored;
        }

        [GolemCatalog("Id")]
        private sealed class DirectTagCatalog : ScriptableObject
        {
            [GolemField(997)]
            public string Id;

            [GolemField(1000)]
            public int Value;
        }

        [GolemCatalog("Id")]
        private sealed class DupTagCatalog : ScriptableObject
        {
            [GolemField(1)]
            public string Id;

            [GolemField(1)]
            public int Other;
        }

        [GolemCatalog("Missing")]
        private sealed class MissingKeyCatalog : ScriptableObject
        {
            [GolemField(1)]
            public string Id;
        }

        [GolemCatalog("Id")]
        private sealed class UnsupportedCatalog : ScriptableObject
        {
            [GolemField(1)]
            public string Id;

            [GolemField(2)]
            public int[] Values;
        }

        [GolemCatalog("Id")]
        private sealed class BothAttrsCatalog : ScriptableObject
        {
            [GolemField(1)]
            [GolemAssetRef(1)]
            public string Id;
        }

        [GolemCatalog("Id")]
        private sealed class ItemsFieldCatalog : ScriptableObject
        {
            [GolemField(1)]
            public string Id;

            [GolemField(2)]
            public int Items;
        }

        [GolemCatalog("Id")]
        private sealed class NumericKeyCatalog : ScriptableObject
        {
            [GolemField(1)]
            public int Id;

            [GolemField(2)]
            public string Name;
        }

        [GolemCatalog("Id")]
        private sealed class PoisonKeyCatalog : ScriptableObject
        {
            [GolemField(1)]
            public string Id;

            [GolemAssetRef(2)]
            public GameObject Prefab;
        }

        [Test]
        public void BuildTypeYaml_UsesDirectTagsAndSnakeCaseFields()
        {
            var model = GolemCatalogSchemaBuilder.Build(
                typeof(MonsterDefinition), "schemas/types/", "schemas/world/");
            Assert.That(model.Errors, Is.Empty);
            Assert.That(model.KeySnakeName, Is.EqualTo("id"));
            Assert.That(model.TypesRelativePath, Is.EqualTo("schemas/types/monster_definition.yaml"));
            Assert.That(model.WorldRelativePath, Is.EqualTo("schemas/world/monster_definition.yaml"));
            Assert.That(model.CatalogDataRelativePath, Is.EqualTo("catalogs/monster_definition.golem.yaml"));

            var yaml = GolemCatalogSchemaBuilder.BuildTypeYaml(model);
            var expected = GolemYamlWriter.BuildDocument(new List<string>
            {
                "type: MonsterDefinition",
                "fields:",
                "  id:",
                "    tag: 1",
                "    type: string",
                "  health:",
                "    tag: 2",
                "    type: int32",
                "  prefab:",
                "    tag: 3",
                "    type: string",
                "  boss:",
                "    tag: 4",
                "    type: bool"
            });
            Assert.That(yaml, Is.EqualTo(expected));
        }

        [Test]
        public void BuildWorldYaml_DeclaresCatalogSourceAndSnakeCaseKey()
        {
            var model = GolemCatalogSchemaBuilder.Build(
                typeof(MonsterDefinition), "schemas/types/", "schemas/world/");
            Assert.That(model.Errors, Is.Empty);

            var yaml = GolemCatalogSchemaBuilder.BuildWorldYaml(model);
            var expected = GolemYamlWriter.BuildDocument(new List<string>
            {
                "world: MonsterDefinition",
                "source:",
                "  format: catalog",
                "  file: catalogs/monster_definition.golem.yaml",
                "  type: MonsterDefinition",
                "  key: id"
            });
            Assert.That(yaml, Is.EqualTo(expected));
        }

        [Test]
        public void BuildRows_RejectsDuplicateAndMissingKeys_SortsDeterministically()
        {
            var model = GolemCatalogSchemaBuilder.Build(
                typeof(MonsterDefinition), "schemas/types/", "schemas/world/");
            Assert.That(model.Errors, Is.Empty);

            var a = ScriptableObject.CreateInstance<MonsterDefinition>();
            var b = ScriptableObject.CreateInstance<MonsterDefinition>();
            var c = ScriptableObject.CreateInstance<MonsterDefinition>();
            var empty = ScriptableObject.CreateInstance<MonsterDefinition>();
            try
            {
                a.Id = "orc";
                a.Health = 40;
                a.Boss = false;
                b.Id = "goblin";
                b.Health = 10;
                b.Boss = true;
                c.Id = "goblin";
                c.Health = 11;
                empty.Id = "";
                empty.Health = 1;

                GolemCatalogSchemaBuilder.BuildRows(
                    model, new ScriptableObject[] { a, b }, out var rows, out var errors);
                Assert.That(errors, Is.Empty);
                Assert.That(rows, Has.Count.EqualTo(2));
                Assert.That(rows[0].SortKey, Is.EqualTo("goblin"));
                Assert.That(rows[1].SortKey, Is.EqualTo("orc"));

                var dataYaml = GolemCatalogSchemaBuilder.BuildCatalogDataYaml(rows);
                Assert.That(dataYaml, Does.Contain("- id: goblin"));
                Assert.That(dataYaml, Does.Contain("prefab: \"\""));

                GolemCatalogSchemaBuilder.BuildRows(
                    model, new ScriptableObject[] { b, c }, out _, out var dupErrors);
                Assert.That(dupErrors, Has.Some.Contain("duplicate key"));

                GolemCatalogSchemaBuilder.BuildRows(
                    model, new ScriptableObject[] { empty }, out _, out var missingErrors);
                Assert.That(missingErrors, Has.Some.Contain("missing or empty"));
            }
            finally
            {
                Object.DestroyImmediate(a);
                Object.DestroyImmediate(b);
                Object.DestroyImmediate(c);
                Object.DestroyImmediate(empty);
            }
        }

        [Test]
        public void BuildCatalogDataYaml_IsSortedSequenceOfMaps()
        {
            var rows = new List<GolemCatalogRowModel>
            {
                new GolemCatalogRowModel
                {
                    SortKey = "orc",
                    Values =
                    {
                        ("id", "orc"),
                        ("health", "40"),
                        ("prefab", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
                        ("boss", "false")
                    }
                },
                new GolemCatalogRowModel
                {
                    SortKey = "goblin",
                    Values =
                    {
                        ("id", "goblin"),
                        ("health", "10"),
                        ("prefab", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
                        ("boss", "true")
                    }
                }
            };
            rows.Sort((a, b) => string.Compare(a.SortKey, b.SortKey, System.StringComparison.Ordinal));

            var yaml = GolemCatalogSchemaBuilder.BuildCatalogDataYaml(rows);
            var expected = GolemYamlWriter.BuildDocument(new List<string>
            {
                "- id: goblin",
                "  health: 10",
                "  prefab: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                "  boss: true",
                "- id: orc",
                "  health: 40",
                "  prefab: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                "  boss: false"
            });
            Assert.That(yaml, Is.EqualTo(expected));
            Assert.That(GolemYamlWriter.IsScribeOwned(yaml), Is.True);
        }

        [Test]
        public void BuildCatalogDataYaml_EmptyCatalogIsEmptySequence()
        {
            var yaml = GolemCatalogSchemaBuilder.BuildCatalogDataYaml(new List<GolemCatalogRowModel>());
            Assert.That(yaml, Is.EqualTo(GolemYamlWriter.BuildDocument(new[] { "[]" })));
        }

        [Test]
        public void Build_AllowsRevisionLikeTagsWithoutEntityOffset()
        {
            var model = GolemCatalogSchemaBuilder.Build(
                typeof(DirectTagCatalog), "schemas/types/", "schemas/world/");
            Assert.That(model.Errors, Is.Empty);
            Assert.That(model.Fields, Has.Some.Matches<GolemCatalogFieldModel>(f => f.Tag == 997));
            Assert.That(model.Fields, Has.Some.Matches<GolemCatalogFieldModel>(f => f.Tag == 1000));
        }

        [Test]
        public void Build_RejectsDuplicateTagsMissingKeyAndUnsupportedTypes()
        {
            var dup = GolemCatalogSchemaBuilder.Build(typeof(DupTagCatalog), "schemas/types/", "schemas/world/");
            Assert.That(dup.Errors, Has.Some.Contain("share tag"));

            var missing = GolemCatalogSchemaBuilder.Build(typeof(MissingKeyCatalog), "schemas/types/", "schemas/world/");
            Assert.That(missing.Errors, Has.Some.Contain("key field"));

            var unsupported = GolemCatalogSchemaBuilder.Build(typeof(UnsupportedCatalog), "schemas/types/", "schemas/world/");
            Assert.That(unsupported.Errors, Has.Some.Contain("unsupported type"));

            var both = GolemCatalogSchemaBuilder.Build(typeof(BothAttrsCatalog), "schemas/types/", "schemas/world/");
            Assert.That(both.Errors, Has.Some.Contain("both [GolemField] and [GolemAssetRef]"));
        }

        [Test]
        public void AssetRef_SchemaTypeIsStringAndGuidScalarIsOpaque()
        {
            var model = GolemCatalogSchemaBuilder.Build(
                typeof(MonsterDefinition), "schemas/types/", "schemas/world/");
            var prefab = model.Fields.Find(f => f.FieldName == "Prefab");
            Assert.That(prefab, Is.Not.Null);
            Assert.That(prefab.IsAssetRef, Is.True);
            Assert.That(prefab.SchemaType, Is.EqualTo("string"));

            var guid = "0123456789abcdef0123456789abcdef";
            Assert.That(GolemYamlWriter.FormatScalar(guid), Is.EqualTo(guid));
        }

        [Test]
        public void StableSourceGuid_IsDeterministic32Hex()
        {
            var a = GolemCatalogSchemaBuilder.StableSourceGuid(typeof(MonsterDefinition));
            var b = GolemCatalogSchemaBuilder.StableSourceGuid(typeof(MonsterDefinition));
            Assert.That(a, Is.EqualTo(b));
            Assert.That(a, Has.Length.EqualTo(32));
            Assert.That(a, Does.Match("^[0-9a-f]{32}$"));
        }

        [Test]
        public void Build_AllowsItemsFieldName_SeparateFromWorldCollection()
        {
            var model = GolemCatalogSchemaBuilder.Build(
                typeof(ItemsFieldCatalog), "schemas/types/", "schemas/world/");
            Assert.That(model.Errors, Is.Empty);
            Assert.That(model.Fields, Has.Some.Matches<GolemCatalogFieldModel>(f => f.SnakeName == "items"));
            Assert.That(GolemCatalogSchemaBuilder.IsValidCustomTypeFieldName("items"), Is.True);
            Assert.That(GolemCatalogSchemaBuilder.IsValidCustomTypeFieldName("_bad"), Is.False);
            Assert.That(GolemCatalogSchemaBuilder.IsValidCustomTypeFieldName(""), Is.False);
        }

        [Test]
        public void BuildRows_InvalidRowDoesNotPoisonLaterValidKey()
        {
            var model = GolemCatalogSchemaBuilder.Build(
                typeof(PoisonKeyCatalog), "schemas/types/", "schemas/world/");
            Assert.That(model.Errors, Is.Empty);

            var bad = ScriptableObject.CreateInstance<PoisonKeyCatalog>();
            var good = ScriptableObject.CreateInstance<PoisonKeyCatalog>();
            GameObject sceneObject = null;
            try
            {
                sceneObject = new GameObject("not-an-asset");
                bad.Id = "same";
                bad.Prefab = sceneObject; // fails asset-ref resolution
                good.Id = "same";
                good.Prefab = null; // empty GUID is valid

                GolemCatalogSchemaBuilder.BuildRows(
                    model, new ScriptableObject[] { bad, good }, out var rows, out var errors);

                Assert.That(errors, Has.Some.Contain("asset reference"));
                Assert.That(errors, Has.None.Contain("duplicate key"));
                Assert.That(rows, Has.Count.EqualTo(1));
                Assert.That(rows[0].SortKey, Is.EqualTo("same"));
            }
            finally
            {
                if (sceneObject != null)
                {
                    Object.DestroyImmediate(sceneObject);
                }
                Object.DestroyImmediate(bad);
                Object.DestroyImmediate(good);
            }
        }

        [Test]
        public void BuildRows_NumericKeysSortNumerically()
        {
            var model = GolemCatalogSchemaBuilder.Build(
                typeof(NumericKeyCatalog), "schemas/types/", "schemas/world/");
            Assert.That(model.Errors, Is.Empty);

            var a = ScriptableObject.CreateInstance<NumericKeyCatalog>();
            var b = ScriptableObject.CreateInstance<NumericKeyCatalog>();
            var c = ScriptableObject.CreateInstance<NumericKeyCatalog>();
            try
            {
                a.Id = 10;
                a.Name = "ten";
                b.Id = 2;
                b.Name = "two";
                c.Id = 10;
                c.Name = "dup";

                GolemCatalogSchemaBuilder.BuildRows(
                    model, new ScriptableObject[] { a, b }, out var rows, out var errors);
                Assert.That(errors, Is.Empty);
                Assert.That(rows[0].SortKey, Is.EqualTo("2"));
                Assert.That(rows[1].SortKey, Is.EqualTo("10"));
                Assert.That(
                    GolemCatalogSchemaBuilder.CompareCatalogKeys("2", "10", "int32"),
                    Is.LessThan(0));

                GolemCatalogSchemaBuilder.BuildRows(
                    model, new ScriptableObject[] { a, c }, out _, out var dupErrors);
                Assert.That(dupErrors, Has.Some.Contain("duplicate key"));
            }
            finally
            {
                Object.DestroyImmediate(a);
                Object.DestroyImmediate(b);
                Object.DestroyImmediate(c);
            }
        }

        [Test]
        public void Build_ValidatesCatalogPathsAreProjectRelative()
        {
            var root = System.IO.Path.Combine(
                System.IO.Path.GetTempPath(), "golem-catalog-path-" + System.IO.Path.GetRandomFileName());
            System.IO.Directory.CreateDirectory(root);
            try
            {
                var ok = GolemCatalogSchemaBuilder.Build(
                    typeof(MonsterDefinition), "schemas/types/", "schemas/world/", root);
                Assert.That(ok.Errors, Is.Empty);
                Assert.That(ok.CatalogDataRelativePath, Is.EqualTo("catalogs/monster_definition.golem.yaml"));

                var bad = GolemCatalogSchemaBuilder.Build(
                    typeof(MonsterDefinition), "../escape/types/", "schemas/world/", root);
                Assert.That(bad.Errors, Has.Some.Contain("project-relative/contained"));
            }
            finally
            {
                System.IO.Directory.Delete(root, true);
            }
        }
    }
}
