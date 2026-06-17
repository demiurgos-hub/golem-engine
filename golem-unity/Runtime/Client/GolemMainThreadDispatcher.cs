using System;
using System.Collections.Concurrent;
using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Runs queued Golem callbacks on Unity's main thread.</summary>
    public sealed class GolemMainThreadDispatcher : MonoBehaviour
    {
        private static readonly ConcurrentQueue<Action> Queue = new ConcurrentQueue<Action>();
        private static GolemMainThreadDispatcher _instance;

        public static void Ensure()
        {
            if (_instance != null)
            {
                return;
            }

            var go = new GameObject("GolemMainThreadDispatcher");
            DontDestroyOnLoad(go);
            _instance = go.AddComponent<GolemMainThreadDispatcher>();
        }

        public static void Enqueue(Action action)
        {
            if (action == null)
            {
                return;
            }
            Ensure();
            Queue.Enqueue(action);
        }

        private void Update()
        {
            while (Queue.TryDequeue(out var action))
            {
                action();
            }
        }
    }
}
