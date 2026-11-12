using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemPhysicsBindingTests
    {
        [GolemEntity("PhysicsActor")]
        private sealed class Bound : MonoBehaviour
        {
            [GolemVar(1, Physics = "orientation_x")] public float qx;
            [GolemVar(2, Physics = "orientation_y")] public float qy;
            [GolemVar(3, Physics = "orientation_z")] public float qz;
            [GolemVar(4, Physics = "orientation_w")] public float qw;
            [GolemVar(5, Physics = "active")] public bool running;
        }
        [GolemEntity("InvalidActor")]
        private sealed class Invalid : MonoBehaviour
        {
            [GolemVar(1, GolemSync.Once, Physics = "orientation_x")] public double x;
            [GolemVar(2, Physics = "orientation_x")] public float duplicate;
        }
        [Test]
        public void ExplicitBindingsExportWithoutRenamingOrImplicitFields()
        {
            var model = GolemEntitySchemaBuilder.Build(typeof(Bound), 3, "schemas/entities");
            Assert.That(model.Errors, Is.Empty);
            var yaml = GolemEntitySchemaBuilder.BuildYaml(model);
            Assert.That(yaml, Does.Contain("  qx:\n    tag: 1\n    type: float\n    sync: tick\n    physics: orientation_x"));
            Assert.That(model.Vars.Count, Is.EqualTo(5));
            Assert.That(GolemEntitySchemaBuilder.Build(typeof(Bound), 2, "schemas/entities").Errors, Is.Not.Empty);
        }
        [Test]
        public void InvalidBindingsReportTypeCadenceDuplicateAndPartialGroup()
        {
            var errors = string.Join("\n", GolemEntitySchemaBuilder.Build(typeof(Invalid), 3, "schemas/entities").Errors);
            Assert.That(errors, Does.Contain("sync: tick"));
            Assert.That(errors, Does.Contain("Duplicate physics"));
            Assert.That(errors, Does.Contain("every component"));
        }
    }
}
