using System;
using System.Collections.Concurrent;
using System.Threading;

namespace GolemEngine.Unity
{
    // Managed-only boundary: producers never inspect or create Unity objects.
    internal sealed class GolemDispatchQueue
    {
        private readonly ConcurrentQueue<Action> _actions = new ConcurrentQueue<Action>();
        private int _closed;

        internal void Enqueue(Action action)
        {
            if (action != null && Volatile.Read(ref _closed) == 0) _actions.Enqueue(action);
        }

        internal void Drain()
        {
            while (Volatile.Read(ref _closed) == 0 && _actions.TryDequeue(out var action)) action();
        }

        internal void Close()
        {
            Interlocked.Exchange(ref _closed, 1);
            while (_actions.TryDequeue(out _)) { }
        }
    }
}
