using System.Collections.Generic;
using GolemEngine.Unity.Editor;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeYamlAndTagTests
    {
        [GolemEntity("Player", Global = true)]
        private sealed class PlayerEntity : MonoBehaviour
        {
            [GolemVar(1, GolemSync.Tick)]
            public int health;

            [GolemVar(2, GolemSync.Once)]
            [SerializeField] private string displayName;

            public float ignored;
        }

        [GolemEntity("Bad")]
        private sealed class UnsupportedEntity : MonoBehaviour
        {
            [GolemVar(1)]
            public int[] values;
        }

        [GolemEntity("Tagged")]
        private sealed class RevisionCollision2D : MonoBehaviour
        {
            [GolemVar(997)]
            public int bad;
        }

        [GolemEntity("Tagged3")]
        private sealed class RevisionCollision3D : MonoBehaviour
        {
            [GolemVar(996)]
            public int bad;
        }

        [GolemEntity("DupTags")]
        private sealed class DuplicateTagEntity : MonoBehaviour
        {
            [GolemVar(1)]
            public int a;

            [GolemVar(1)]
            public int b;
        }

        [GolemEntity("badName")]
        private sealed class InvalidNameEntity : MonoBehaviour
        {
            [GolemVar(1)]
            public int health;
        }

        [GolemEntity("ZeroTag")]
        private sealed class ZeroTagEntity : MonoBehaviour
        {
            [GolemVar(0)]
            public int health;
        }

        [GolemEntity("PrivateField")]
        private sealed class PrivateUnserializedEntity : MonoBehaviour
        {
            [GolemVar(1)]
            private int health;
        }

        [Test]
        public void BuildYaml_IsDeterministicAndIgnoresFieldValues()
        {
            var model = GolemEntitySchemaBuilder.Build(typeof(PlayerEntity), 2, "schemas/entities/");
            Assert.That(model.Errors, Is.Empty);
            Assert.That(model.RelativePath, Is.EqualTo("schemas/entities/player.yaml"));

            var yaml = GolemEntitySchemaBuilder.BuildYaml(model);
            var expected = GolemYamlWriter.BuildDocument(new List<string>
            {
                "entity: Player",
                "global: true",
                "vars:",
                "  health:",
                "    tag: 1",
                "    type: int32",
                "    sync: tick",
                "  display_name:",
                "    tag: 2",
                "    type: string",
                "    sync: once"
            });
            Assert.That(yaml, Is.EqualTo(expected));
            Assert.That(GolemYamlWriter.IsScribeOwned(yaml), Is.True);
        }

        [Test]
        public void EntityTagOffset_MatchesSchemaLoader()
        {
            Assert.That(GolemEntitySchemaBuilder.EntityTagOffset(2), Is.EqualTo(3));
            Assert.That(GolemEntitySchemaBuilder.EntityTagOffset(3), Is.EqualTo(4));
            Assert.That(GolemEntitySchemaBuilder.ReservedRevisionUserTag(2), Is.EqualTo(997));
            Assert.That(GolemEntitySchemaBuilder.ReservedRevisionUserTag(3), Is.EqualTo(996));
        }

        [Test]
        public void Build_RejectsRevisionWireTagCollision()
        {
            var twoD = GolemEntitySchemaBuilder.Build(typeof(RevisionCollision2D), 2, "schemas/entities/");
            Assert.That(twoD.Errors, Has.Some.Contain("reserved revision proto field 1000"));

            var threeD = GolemEntitySchemaBuilder.Build(typeof(RevisionCollision3D), 3, "schemas/entities/");
            Assert.That(threeD.Errors, Has.Some.Contain("reserved revision proto field 1000"));
        }

        [Test]
        public void Build_RejectsUnsupportedFieldTypes()
        {
            var model = GolemEntitySchemaBuilder.Build(typeof(UnsupportedEntity), 2, "schemas/entities/");
            Assert.That(model.Errors, Has.Some.Contain("unsupported type"));
        }

        [Test]
        public void Build_RejectsDuplicateTagsInvalidNamesAndPrivateFields()
        {
            var dups = GolemEntitySchemaBuilder.Build(typeof(DuplicateTagEntity), 2, "schemas/entities/");
            Assert.That(dups.Errors, Has.Some.Contain("share tag"));

            var badName = GolemEntitySchemaBuilder.Build(typeof(InvalidNameEntity), 2, "schemas/entities/");
            Assert.That(badName.Errors, Has.Some.Contain("PascalCase"));

            var zero = GolemEntitySchemaBuilder.Build(typeof(ZeroTagEntity), 2, "schemas/entities/");
            Assert.That(zero.Errors, Has.Some.Contain("tag is required"));

            var priv = GolemEntitySchemaBuilder.Build(typeof(PrivateUnserializedEntity), 2, "schemas/entities/");
            Assert.That(priv.Errors, Has.Some.Contain("public or [SerializeField]"));
        }

        [Test]
        public void ScalarTypeMap_MatchesPlanContract()
        {
            Assert.That(GolemScribeTypes.TryGetSchemaType(typeof(int), out var t1), Is.True);
            Assert.That(t1, Is.EqualTo("int32"));
            Assert.That(GolemScribeTypes.TryGetSchemaType(typeof(uint), out var t2), Is.True);
            Assert.That(t2, Is.EqualTo("uint32"));
            Assert.That(GolemScribeTypes.TryGetSchemaType(typeof(long), out var t3), Is.True);
            Assert.That(t3, Is.EqualTo("int64"));
            Assert.That(GolemScribeTypes.TryGetSchemaType(typeof(ulong), out var t4), Is.True);
            Assert.That(t4, Is.EqualTo("uint64"));
            Assert.That(GolemScribeTypes.TryGetSchemaType(typeof(float), out var t5), Is.True);
            Assert.That(t5, Is.EqualTo("float"));
            Assert.That(GolemScribeTypes.TryGetSchemaType(typeof(double), out var t6), Is.True);
            Assert.That(t6, Is.EqualTo("double"));
            Assert.That(GolemScribeTypes.TryGetSchemaType(typeof(bool), out var t7), Is.True);
            Assert.That(t7, Is.EqualTo("bool"));
            Assert.That(GolemScribeTypes.TryGetSchemaType(typeof(string), out var t8), Is.True);
            Assert.That(t8, Is.EqualTo("string"));
            Assert.That(GolemScribeTypes.TryGetSchemaType(typeof(decimal), out _), Is.False);
        }
    }
}
