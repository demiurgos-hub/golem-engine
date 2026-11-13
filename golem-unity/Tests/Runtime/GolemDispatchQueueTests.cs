using System.Collections.Concurrent;
using System.Threading;
using System.Threading.Tasks;
using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed class GolemDispatchQueueTests
    {
        [Test]
        public void WorkerPostsRunOnlyOnTheDrainingThread()
        {
            var queue = new GolemDispatchQueue();
            var threads = new ConcurrentQueue<int>();
            Task.Run(() => queue.Enqueue(() => threads.Enqueue(Thread.CurrentThread.ManagedThreadId))).GetAwaiter().GetResult();
            Assert.That(threads, Is.Empty);
            queue.Drain();
            Assert.That(threads.ToArray(), Is.EqualTo(new[] { Thread.CurrentThread.ManagedThreadId }));
        }

        [Test]
        public void CloseDropsQueuedAndLateCallbacksWithoutResurrection()
        {
            var queue = new GolemDispatchQueue();
            var calls = 0;
            queue.Enqueue(() => calls++);
            queue.Close();
            Task.Run(() => queue.Enqueue(() => calls++)).GetAwaiter().GetResult();
            queue.Drain();
            Assert.That(calls, Is.Zero);
            var replacement = new GolemDispatchQueue();
            replacement.Enqueue(() => calls++);
            replacement.Drain();
            Assert.That(calls, Is.EqualTo(1));
        }

        [Test]
        public void TeardownInsideCallbackStopsTheCurrentDrain()
        {
            var queue = new GolemDispatchQueue();
            var calls = 0;
            queue.Enqueue(() => { calls++; queue.Close(); });
            queue.Enqueue(() => calls++);
            queue.Drain();
            Assert.That(calls, Is.EqualTo(1));
        }
    }
}
