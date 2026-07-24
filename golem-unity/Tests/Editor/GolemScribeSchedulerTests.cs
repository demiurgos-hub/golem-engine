using GolemEngine.Unity.Editor;
using NUnit.Framework;
using UnityEditor;

namespace GolemEngine.Unity.Editor.Tests
{
    public sealed class GolemScribeSchedulerTests
    {
        [SetUp]
        public void SetUp()
        {
            GolemScribeScheduler.ResetForTests();
        }

        [TearDown]
        public void TearDown()
        {
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
    }
}
