using GolemEngine.Unity.Editor;
using NUnit.Framework;
using UnityEditor;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeSchedulerTests
    {
        private bool _previousAutoExport;
        private bool _previousAutoBake;

        [SetUp]
        public void SetUp()
        {
            var settings = GolemUnityEditorSettings.instance;
            _previousAutoExport = settings.AutoExportOnAssetChange;
            _previousAutoBake = settings.AutoBakeOnExport;
            // Known baseline so notify tests do not depend on ambient project settings.
            settings.AutoExportOnAssetChange = true;
            GolemUnityEditorSettings.Save();
            GolemScribeScheduler.ResetForTests();
        }

        [TearDown]
        public void TearDown()
        {
            // Always restore persisted settings so a failed AutoExport assertion cannot leak.
            var settings = GolemUnityEditorSettings.instance;
            settings.AutoExportOnAssetChange = _previousAutoExport;
            settings.AutoBakeOnExport = _previousAutoBake;
            GolemUnityEditorSettings.Save();
            GolemScribeScheduler.ResetForTests();
        }

        [Test]
        public void NotifyImported_CoalescesIntoSingleSchedule()
        {
            GolemScribeScheduler.NotifyImportedOrMoved(new[] { "Assets/A.prefab", "Assets/B.prefab" });
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.True);
            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.EqualTo(2));

            GolemScribeScheduler.NotifyImportedOrMoved(new[] { "Assets/C.prefab" });
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.True);
            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.EqualTo(3));
        }

        [Test]
        public void NotifyImported_IgnoresIrrelevantAssets()
        {
            GolemScribeScheduler.NotifyImportedOrMoved(new[] { "Assets/Textures/x.png" });
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.False);
            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.EqualTo(0));
        }

        [Test]
        public void NotifyImported_AcceptsScriptableObjectAssets()
        {
            GolemScribeScheduler.NotifyImportedOrMoved(new[] { "Assets/Data/Monster.asset" });
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.True);
            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.EqualTo(1));
        }

        [Test]
        public void RunSuppressed_BlocksPostprocessorNotifications()
        {
            GolemScribeScheduler.RunSuppressed(() =>
            {
                Assert.That(GolemScribeScheduler.IsSuppressed, Is.True);
                GolemScribeScheduler.NotifyImportedOrMoved(new[] { "Assets/A.prefab" });
                GolemScribeScheduler.NotifyDeleted(new[] { "Assets/B.prefab" });
            });

            Assert.That(GolemScribeScheduler.IsSuppressed, Is.False);
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.False);
            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.EqualTo(0));
        }

        [Test]
        public void NotifyImported_PreservedWhileExportRunning()
        {
            GolemScribeScheduler.SetRunningForTests(true);
            Assert.That(GolemScribeScheduler.IsSuppressed, Is.False);

            GolemScribeScheduler.NotifyImportedOrMoved(new[] { "Assets/A.prefab" });
            GolemScribeScheduler.NotifyDeleted(new[] { "Assets/B.prefab" });

            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.EqualTo(2));
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.False);
        }

        [Test]
        public void HandleScriptsReloaded_ClearsStaleSessionSuppression()
        {
            SessionState.SetBool("GolemScribe.SuppressAutoExport", true);
            Assert.That(GolemScribeScheduler.IsSuppressed, Is.True);

            GolemScribeScheduler.HandleScriptsReloaded();

            Assert.That(GolemScribeScheduler.IsSuppressed, Is.False);
            GolemScribeScheduler.NotifyImportedOrMoved(new[] { "Assets/A.prefab" });
            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.EqualTo(1));
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.True);
        }

        [Test]
        public void NotifyImported_RespectsAutoExportSetting()
        {
            var settings = GolemUnityEditorSettings.instance;
            settings.AutoExportOnAssetChange = false;
            GolemUnityEditorSettings.Save();
            GolemScribeScheduler.ResetForTests();

            GolemScribeScheduler.NotifyImportedOrMoved(new[] { "Assets/A.prefab" });
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.False);
            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.EqualTo(0));

            // Manual Export All still queues while auto-export is off.
            GolemScribeScheduler.RequestExportAll();
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.True);
        }

        [Test]
        public void ShouldAutoBake_DecouplesEntityAndCatalogErrors()
        {
            Assert.That(
                GolemScribeScheduler.ShouldAutoBake(
                    entityHasErrors: false,
                    entitySchemaBytesChanged: true,
                    catalogHasErrors: true,
                    catalogSchemaBytesChanged: true),
                Is.True);

            Assert.That(
                GolemScribeScheduler.ShouldAutoBake(
                    entityHasErrors: true,
                    entitySchemaBytesChanged: true,
                    catalogHasErrors: false,
                    catalogSchemaBytesChanged: true),
                Is.True);

            Assert.That(
                GolemScribeScheduler.ShouldAutoBake(
                    entityHasErrors: true,
                    entitySchemaBytesChanged: true,
                    catalogHasErrors: true,
                    catalogSchemaBytesChanged: true),
                Is.False);

            Assert.That(
                GolemScribeScheduler.ShouldAutoBake(
                    entityHasErrors: false,
                    entitySchemaBytesChanged: false,
                    catalogHasErrors: false,
                    catalogSchemaBytesChanged: false),
                Is.False);

            Assert.That(
                GolemScribeScheduler.ShouldAutoBake(
                    entityHasErrors: false,
                    entitySchemaBytesChanged: true,
                    catalogHasErrors: false,
                    catalogSchemaBytesChanged: true),
                Is.True);
        }
    }
}
