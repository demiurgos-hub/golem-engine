using System;
using System.Linq;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeFieldInspectionTests
    {
        private class EntityBase : MonoBehaviour
        {
            [SerializeField, GolemVar(2)] private int BaseValue;
            [GolemVar(3), NonSerialized] public int Transient;
            [GolemVar(4)] public readonly int ReadonlyValue;
            [GolemVar(1)] public static int StaticIgnored;
        }
        [GolemEntity("Inherited")]
        private sealed class EntityDerived : EntityBase { [GolemVar(1)] public int Own; }
        [GolemEntity("Duplicate")]
        private sealed class DuplicateDerived : EntityBase { [GolemVar(2)] public int Own; }
        [GolemEntity("Names")]
        private sealed class NameCollision : MonoBehaviour
        {
            [GolemVar(1)] public int Value;
            [GolemVar(2)] public int value;
        }
        [GolemEntity("Sync")]
        private sealed class BadSync : MonoBehaviour { [GolemVar(1, (GolemSync)999)] public int Value; }

        private class CatalogBase : ScriptableObject
        {
            [GolemField(1), SerializeField] private string Id;
            [GolemField(2), NonSerialized] public int Transient;
            [GolemField(3)] public readonly int ReadonlyValue;
            [GolemField(1)] public static int StaticIgnored;
        }
        [GolemCatalog("Id")]
        private sealed class CatalogDerived : CatalogBase { [GolemField(1000)] public int Revision; }
        [GolemCatalog("Id")]
        private sealed class CatalogHidden : CatalogBase { [GolemField(4)] public string Id; }

        [Test]
        public void EntityWalkRetainsVisibilityAndTagOrderingAcrossInheritance()
        {
            var model = GolemEntitySchemaBuilder.Build(typeof(EntityDerived), 3, "schemas/entities");
            Assert.That(model.Errors, Is.Empty);
            Assert.That(model.Vars.Select(v => v.SnakeName), Is.EqualTo(new[] { "own", "base_value", "transient", "readonly_value" }));
            Assert.That(model.Vars.Select(v => v.Tag), Is.EqualTo(new[] { 1, 2, 3, 4 }));
            var duplicate = GolemEntitySchemaBuilder.Build(typeof(DuplicateDerived), 2, "entities");
            Assert.That(duplicate.Errors, Has.Some.Contain("fields 'Own' and 'BaseValue' share tag 2"));
            Assert.That(GolemEntitySchemaBuilder.Build(typeof(NameCollision), 2, "entities").Errors, Has.Some.Contain("both map to"));
            Assert.That(GolemEntitySchemaBuilder.Build(typeof(BadSync), 2, "entities").Errors, Has.Some.Contain("invalid GolemSync"));
        }

        [Test]
        public void CatalogWalkRetainsInheritedKeysAndDirectTags()
        {
            var model = GolemCatalogSchemaBuilder.Build(typeof(CatalogDerived), "types", "world");
            Assert.That(model.Errors, Is.Empty);
            Assert.That(model.Fields.Select(f => f.Tag), Is.EqualTo(new[] { 1, 2, 3, 1000 }));
            Assert.That(model.KeySnakeName, Is.EqualTo("id"));
            Assert.That(GolemCatalogSchemaBuilder.Build(typeof(CatalogHidden), "types", "world").Errors, Has.Some.Contain("both map to 'id'"));
        }
    }
}
