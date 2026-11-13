using System;
using System.Threading;
using System.Threading.Tasks;

namespace GolemEngine.Unity
{
    // The CONNECT close capsule and the game's stream can arrive in either order.
    // Publish one result, preferring actual session metadata over a lane EOF.
    internal sealed class GolemWebTransportCloseState
    {
        private readonly Task<GolemDisconnectInfo> _peerClose;
        private readonly Action<GolemDisconnectInfo> _publish;
        private readonly string _url;
        private readonly int _closeGraceMilliseconds;
        private int _reported;

        internal GolemWebTransportCloseState(Task<GolemDisconnectInfo> peerClose,
            Action<GolemDisconnectInfo> publish, string url, int closeGraceMilliseconds = 1000)
        {
            _peerClose = peerClose ?? throw new ArgumentNullException(nameof(peerClose));
            _publish = publish ?? throw new ArgumentNullException(nameof(publish));
            _url = url;
            _closeGraceMilliseconds = closeGraceMilliseconds;
        }

        internal bool Reported => Volatile.Read(ref _reported) != 0;

        internal static GolemDisconnectInfo PeerClose(uint code, string reason, string url)
        {
            return new GolemDisconnectInfo(true,
                code: code <= int.MaxValue ? (int?)code : null,
                reason: reason, transport: "webtransport", url: url,
                phase: GolemDisconnectPhase.Close, category: GolemDisconnectCategory.PeerClosed,
                webTransportErrorCode: code);
        }

        internal async Task ObserveAsync()
        {
            GolemDisconnectInfo info;
            try { info = await _peerClose.ConfigureAwait(false); }
            catch (Exception error)
            {
                info = GolemUnityLog.CreateDisconnectInfo(false, GolemDisconnectPhase.Receive,
                    error, "webtransport", _url);
            }
            Publish(info);
        }

        internal async Task FailAsync(Exception error, GolemDisconnectPhase phase, bool waitForPeerClose)
        {
            if (Reported) return;
            if (waitForPeerClose && !_peerClose.IsCompleted)
            {
                using (var timeout = new CancellationTokenSource())
                {
                    await Task.WhenAny(_peerClose, Task.Delay(_closeGraceMilliseconds, timeout.Token)).ConfigureAwait(false);
                    timeout.Cancel();
                }
            }
            if (_peerClose.Status == TaskStatus.RanToCompletion)
            {
                Publish(await _peerClose.ConfigureAwait(false));
                return;
            }
            Publish(GolemUnityLog.CreateDisconnectInfo(false, phase, error, "webtransport", _url));
        }

        internal void CloseLocally()
        {
            if (_peerClose.Status == TaskStatus.RanToCompletion)
            {
                Publish(_peerClose.GetAwaiter().GetResult());
                return;
            }
            Publish(new GolemDisconnectInfo(true, transport: "webtransport", url: _url,
                phase: GolemDisconnectPhase.Close, category: GolemDisconnectCategory.UserClosed));
        }

        private void Publish(GolemDisconnectInfo info)
        {
            if (Interlocked.Exchange(ref _reported, 1) == 0) _publish(info);
        }
    }
}
