using System;
using System.IO;
using System.Linq;
using System.Runtime.CompilerServices;
using NUnit.Framework;
using UnityEngine;

namespace GolemEngine.Unity.Editor.Tests
{
    internal sealed class GolemPhysicsDebugTests
    {
        [Test]
        public void ActualGoCapturePreservesGeometryIdentitiesAndAcceptedObservations()
        {
            var capture = GolemPhysicsDebug.ReadFile(FixturePath());
            Assert.That(capture.Version, Is.EqualTo(1)); Assert.That(capture.Status, Is.EqualTo("accepted"));
            Assert.That(capture.AcceptedFrame, Is.EqualTo(1)); Assert.That(capture.FrameAccepted, Is.True);
            Assert.That(capture.Bodies.Length, Is.EqualTo(3)); Assert.That(capture.Geometry.Length, Is.EqualTo(4));
            Assert.That(capture.Bodies.All(b => b.Uncommitted == null), Is.True);
            var box = capture.Geometry.Single(g => g.Kind == 4);
            Assert.That(box.Vertices.Length, Is.EqualTo(8)); Assert.That(box.Indices.Length, Is.EqualTo(36));
            var collider = capture.Colliders.Single(c => c.Handle.Key == box.Collider.Key);
            var body = capture.Bodies.Single(b => b.State.Handle.Key == collider.Body.Key);
            Assert.That(collider.ID, Is.EqualTo("compound/collider"));
            Assert.That(body.State.Pose.Matrix.MultiplyPoint3x4(box.Vertices[0].Vector).x, Is.InRange(1.69f, 2.31f));
            Assert.That(capture.Contacts.Length, Is.EqualTo(4)); Assert.That(capture.Paths.Length, Is.EqualTo(1));
            Assert.That(capture.Paths[0].Positions.Length, Is.GreaterThan(2));
            Assert.That(capture.Queries.Length, Is.EqualTo(1)); Assert.That(capture.Queries[0].Hits.Length, Is.EqualTo(2));
            Assert.That(capture.Queries[0].Hits[0].Content, Is.EqualTo("ball/collider"));
            Assert.That(capture.Queries[0].Hits[0].Distance, Is.EqualTo(.7955389f).Within(.0001f));
            Assert.That(capture.Queries[0].Hits[1].Point.Y, Is.Zero); Assert.That(capture.Targets.Length, Is.EqualTo(1));
        }

        [Test]
        public void MalformedOversizedAndMislabeledDocumentsAreRejected()
        {
            var json = File.ReadAllText(FixturePath());
            foreach (var invalid in new[] { "{}", json.Replace("\"Version\": 1", "\"Version\": 2"), json + " {}", "{\"Unknown\":1," + json.Substring(1), "{\"Version\":1," + json.Substring(1), new string(' ', GolemPhysicsDebug.MaxBytes + 1) })
                Assert.Throws<InvalidDataException>(() => GolemPhysicsDebug.Read(invalid));
            var capture = GolemPhysicsDebug.Read(json);
            capture.Geometry.First(g => g.Indices.Length != 0).Indices[0] = uint.MaxValue;
            Assert.Throws<InvalidDataException>(() => GolemPhysicsDebug.Validate(capture));
            capture = GolemPhysicsDebug.Read(json); capture.Bodies[0].Uncommitted = capture.Bodies[0].State; capture.Bodies[0].HasUncommitted = true;
            Assert.Throws<InvalidDataException>(() => GolemPhysicsDebug.Validate(capture));
            capture.Status = "faulted"; capture.FrameAccepted = false; GolemPhysicsDebug.Validate(capture);
            capture.Bodies[0].Uncommitted.Pose.Position.X = float.NaN;
            Assert.Throws<InvalidDataException>(() => GolemPhysicsDebug.Validate(capture));
        }

        private static string FixturePath([CallerFilePath] string sourceFile = "")
        {
            var copied = Path.Combine(TestContext.CurrentContext.TestDirectory, "physics-debug.json");
            return File.Exists(copied) ? copied : Path.GetFullPath(Path.Combine(Path.GetDirectoryName(sourceFile), "../Fixtures/physics-debug.json"));
        }
    }
}
