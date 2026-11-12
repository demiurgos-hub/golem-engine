using System;
using NUnit.Framework;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemPhysicsCatalogFixtureTests
    {
        [Test]
        public void CatalogHelpersRejectOutputsOutsideValidationWorkspaceBeforeReadingInputs()
        {
            var output = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT");
            var input = Environment.GetEnvironmentVariable("GOLEM_PHYSICS_CATALOG_INPUT");
            try
            {
                Environment.SetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT", "C:/Fractured/forbidden-fixture-output");
                Environment.SetEnvironmentVariable("GOLEM_PHYSICS_CATALOG_INPUT", null);
                StringAssert.Contains("D physics validation", Assert.Throws<InvalidOperationException>(() => GolemPhysicsCatalogFixture.Generate()).Message);
                StringAssert.Contains("D physics validation", Assert.Throws<InvalidOperationException>(() => GolemPhysicsCatalogFixture.AuditResolved()).Message);
                StringAssert.Contains("D physics validation", Assert.Throws<InvalidOperationException>(() => GolemPhysicsCatalogFixture.PrepareReadableCopies()).Message);
            }
            finally
            {
                Environment.SetEnvironmentVariable("GOLEM_PHYSICS_FIXTURE_OUTPUT", output);
                Environment.SetEnvironmentVariable("GOLEM_PHYSICS_CATALOG_INPUT", input);
            }
        }
    }
}
