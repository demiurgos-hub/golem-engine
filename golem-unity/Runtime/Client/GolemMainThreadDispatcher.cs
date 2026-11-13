using System;
using System.Threading;
using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Runs queued Golem callbacks on Unity's main thread.</summary>
    public sealed class GolemMainThreadDispatcher : MonoBehaviour
    {
        private static GolemDispatchQueue _activeQueue;
        private static GolemMainThreadDispatcher _instance;
        private GolemDispatchQueue _queue;

        [RuntimeInitializeOnLoadMethod(RuntimeInitializeLoadType.SubsystemRegistration)]
        private static void Reset()
        {
            Interlocked.Exchange(ref _activeQueue, null)?.Close();
            _instance = null;
        }

        [RuntimeInitializeOnLoadMethod(RuntimeInitializeLoadType.BeforeSceneLoad)]
        private static void Initialize() => Ensure();

        /// <summary>Creates the dispatcher when needed. Call only on Unity's main thread.</summary>
        public static void Ensure()
        {
            if (_instance != null)
            {
                return;
            }

            var go = new GameObject("GolemMainThreadDispatcher");
            if (Application.isPlaying)
            {
                DontDestroyOnLoad(go);
            }
            go.AddComponent<GolemMainThreadDispatcher>();
        }

        /// <summary>Queues a callback without accessing Unity objects; drops work outside the dispatcher's active lifetime.</summary>
        public static void Enqueue(Action action)
        {
            Volatile.Read(ref _activeQueue)?.Enqueue(action);
        }

        private void OnEnable()
        {
            _instance = this;
            _queue = new GolemDispatchQueue();
            Interlocked.Exchange(ref _activeQueue, _queue)?.Close();
        }

        private void OnDisable()
        {
            Interlocked.CompareExchange(ref _activeQueue, null, _queue);
            _queue?.Close();
        }

        private void OnApplicationQuit() => OnDisable();

        private void OnDestroy()
        {
            OnDisable();
            if (ReferenceEquals(_instance, this)) _instance = null;
        }

        private void Update()
        {
            _queue?.Drain();
        }
    }
}
