using System.Collections;
using System.Threading;
using System.Threading.Tasks;
using NUnit.Framework;
using UnityEngine;
using UnityEngine.TestTools;

namespace GolemEngine.Unity.Tests
{
    public sealed class GolemMainThreadDispatcherTests
    {
        [UnityTest]
        public IEnumerator BackgroundCallbacksCannotRecreateDestroyedDispatcher()
        {
            GolemMainThreadDispatcher.Ensure();
            var mainThread = Thread.CurrentThread.ManagedThreadId;
            var callbackThread = 0;
            var post = Task.Run(() => GolemMainThreadDispatcher.Enqueue(() => callbackThread = Thread.CurrentThread.ManagedThreadId));
            while (!post.IsCompleted) yield return null;
            post.GetAwaiter().GetResult();
            yield return null;
            Assert.That(callbackThread, Is.EqualTo(mainThread));

            var dispatcher = Object.FindObjectOfType<GolemMainThreadDispatcher>();
            var staleCallback = false;
            GolemMainThreadDispatcher.Enqueue(() => staleCallback = true);
            Object.DestroyImmediate(dispatcher.gameObject);
            post = Task.Run(() => GolemMainThreadDispatcher.Enqueue(() => staleCallback = true));
            while (!post.IsCompleted) yield return null;
            post.GetAwaiter().GetResult();
            Assert.That(Object.FindObjectOfType<GolemMainThreadDispatcher>(), Is.Null);

            GolemMainThreadDispatcher.Ensure();
            yield return null;
            Assert.That(staleCallback, Is.False);
            LogAssert.NoUnexpectedReceived();
        }
    }
}
