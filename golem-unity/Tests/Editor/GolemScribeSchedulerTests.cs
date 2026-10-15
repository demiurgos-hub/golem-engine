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

        [Test]
        public void MovedNotificationsKeepDirtyAndDeletedSetsDistinct()
        {
            var callback = typeof(GolemScribePostprocessor).GetMethod("OnPostprocessAllAssets",
                System.Reflection.BindingFlags.Static | System.Reflection.BindingFlags.NonPublic);
            callback.Invoke(null, new object[]
            {
                new[] { "Assets/A.prefab", "assets/a.PREFAB", "Assets/ignored.png", "Assets/Source.cs" },
                new[] { "Assets/A.prefab" }, new[] { "Assets/New.asset" }, new[] { "Assets/Old.asset" }
            });
            Assert.That(GolemScribeScheduler.IsDirtyForTests("Assets/A.prefab"), Is.True);
            Assert.That(GolemScribeScheduler.IsDeletedForTests("Assets/A.prefab"), Is.True);
            Assert.That(GolemScribeScheduler.IsDirtyForTests("Assets/New.asset"), Is.True);
            Assert.That(GolemScribeScheduler.IsDeletedForTests("Assets/Old.asset"), Is.True);
            Assert.That(GolemScribeScheduler.IsDirtyForTests("Assets/Old.asset"), Is.False);
            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.EqualTo(5));
        }

        [Test]
        public void DeferredExportRetainsExternalNotificationsAndRunsFollowUpWithoutReentrancy()
        {
            var settings = GolemUnityEditorSettings.instance;
            var previousRoot = settings.ProjectRoot;
            var root = System.IO.Path.Combine(System.IO.Path.GetTempPath(), "scribe-scheduler-" + System.Guid.NewGuid().ToString("N"));
            System.IO.Directory.CreateDirectory(root);
            System.IO.File.WriteAllText(System.IO.Path.Combine(root, "golem.yaml"), "simulation:\n  dimensions: 2\n");
            var notified = false;
            try
            {
                settings.ProjectRoot = root;
                settings.AutoBakeOnExport = false;
                GolemScribeArtifacts.AfterWriteHookForTests = _ =>
                {
                    if (notified) return;
                    notified = true;
                    Assert.That(GolemScribeScheduler.IsRunningForTests, Is.True);
                    Assert.That(GolemScribeScheduler.ExportAllImmediate(false).Errors, Has.Some.Contain("already running"));
                    GolemScribeScheduler.NotifyImportedOrMoved(new[] { "Assets/External.prefab" });
                    GolemScribeScheduler.NotifyDeleted(new[] { "Assets/Old.prefab" });
                };
                GolemScribeScheduler.RequestExportAll();
                GolemScribeScheduler.RunDeferredForTests();
                Assert.That(notified, Is.True);
                Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.True);
                Assert.That(GolemScribeScheduler.IsDirtyForTests("Assets/External.prefab"), Is.True);
                Assert.That(GolemScribeScheduler.IsDeletedForTests("Assets/Old.prefab"), Is.True);
                GolemScribeScheduler.RunDeferredForTests();
                Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.False);
                Assert.That(GolemScribeScheduler.IsRunningForTests, Is.False);
                Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.Zero);
            }
            finally
            {
                GolemScribeArtifacts.AfterWriteHookForTests = null;
                settings.ProjectRoot = previousRoot;
                GolemScribeScheduler.ResetForTests();
                System.IO.Directory.Delete(root, true);
            }
        }

        [Test]
        public void NestedSuppressionRestoresStateAfterExceptionAndReloadRetainsPendingWork()
        {
            GolemScribeScheduler.RunSuppressed(() =>
            {
                Assert.Throws<System.InvalidOperationException>(() => GolemScribeScheduler.RunSuppressed(
                    () => throw new System.InvalidOperationException("test")));
                Assert.That(GolemScribeScheduler.IsSuppressed, Is.True);
                GolemScribeScheduler.NotifyDeleted(new[] { "Assets/Ignore.cs" });
            });
            Assert.That(GolemScribeScheduler.IsSuppressed, Is.False);
            Assert.That(GolemScribeScheduler.DirtyPathCountForTests, Is.Zero);
            SessionState.SetBool("GolemScribe.PendingReconcile", true);
            GolemScribeScheduler.HandleScriptsReloaded();
            Assert.That(GolemScribeScheduler.IsScheduledForTests, Is.True);
        }
    }
}
