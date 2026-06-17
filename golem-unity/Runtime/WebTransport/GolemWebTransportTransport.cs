using System;
using System.Threading;
using System.Threading.Tasks;
using WebTransport;

namespace GolemEngine.Unity
{
    /// <summary>Reliable WebTransport stream transport for the Golem stream protocol.</summary>
    public sealed class GolemWebTransportTransport : IGolemTransport
    {
        private const string TransportName = "webtransport";
        private readonly WebTransportClientOptions _options;
        private readonly int _eventualAckIntervalMilliseconds;
        private WebTransportClient _client;
        private WebTransportSession _session;
        private WebTransportStream _stream;
        private GolemDatagramProtocol _protocol;
        private CancellationTokenSource _cts;
        private string _url;
        private bool _connected;

        public GolemWebTransportTransport()
            : this(new WebTransportClientOptions())
        {
        }

        public GolemWebTransportTransport(WebTransportClientOptions options)
            : this(options, 1)
        {
        }

        public GolemWebTransportTransport(WebTransportClientOptions options, int eventualAckIntervalMilliseconds)
        {
            _options = options ?? throw new ArgumentNullException(nameof(options));
            _eventualAckIntervalMilliseconds = eventualAckIntervalMilliseconds;
        }

        public bool Connected => _connected;
        public int MaxMessageBytes => GameClient.MaxReliableMessageBytes;
        public int MaxDatagramBytes => GolemDatagramProtocol.MaxWebTransportDatagramBytes;
        public event Action ConnectedEvent;
        public event Action<byte[]> MessageEvent;
        public event Action<byte[]> UnreliableStateMessageEvent;
        public event Action<byte[]> ReliableOrderedMessageEvent;
        public event Action<byte[]> EventualStateMessageEvent;
        public event Action<GolemDisconnectInfo> DisconnectedEvent;

        public async void Connect(string url)
        {
            Close();
            _url = url;
            _cts = new CancellationTokenSource();
            var redactedUrl = GolemUnityLog.RedactUrl(url);
            var phase = GolemDisconnectPhase.Connect;
            GolemUnityLog.Info($"connecting transport={TransportName} url={redactedUrl}");

            try
            {
                _client = new WebTransportClient(_options);
                _session = await _client.ConnectAsync(new Uri(url), _cts.Token);
                phase = GolemDisconnectPhase.OpenStream;
                _stream = await _session.OpenBidirectionalStreamAsync(_cts.Token);
                phase = GolemDisconnectPhase.StreamHeaderWrite;
                _protocol = new GolemDatagramProtocol(WriteDatagram, _eventualAckIntervalMilliseconds);

                // Golem accepts the client-opened WebTransport stream after the
                // first write, even when the write has no payload.
                await _stream.WriteAsync(Array.Empty<byte>(), _cts.Token);

                _connected = true;
                GolemUnityLog.Info($"connected transport={TransportName} url={redactedUrl}");
                ConnectedEvent?.Invoke();
                _ = ReceiveLoop(_cts.Token);
                _ = DatagramLoop(_cts.Token);
                _ = SchedulerLoop(_cts.Token);
            }
            catch (Exception ex)
            {
                _connected = false;
                var info = DisconnectInfo(false, phase, ex);
                GolemUnityLog.Error(
                    $"connect failed transport={TransportName} url={redactedUrl} phase={GolemUnityLog.PhaseName(phase)} category={GolemUnityLog.CategoryName(info.Category)} {GolemUnityLog.FormatError(ex)}");
                DisconnectedEvent?.Invoke(info);
                await DisposeTransportAsync();
            }
        }

        public async void Send(byte[] bytes)
        {
            if (!Connected || _stream == null)
            {
                return;
            }
            if (bytes == null)
            {
                throw new ArgumentNullException(nameof(bytes));
            }
            if (bytes.Length > MaxMessageBytes)
            {
                throw new InvalidOperationException($"golem-unity: reliable message size {bytes.Length} exceeds max reliable message {MaxMessageBytes}");
            }

            try
            {
                var payload = _protocol == null ? bytes : _protocol.WrapStreamPayload(bytes);
                var frame = GolemReliableFrameCodec.Encode(payload, MaxMessageBytes);
                await _stream.WriteAsync(frame, _cts.Token);
            }
            catch (Exception ex)
            {
                _connected = false;
                var info = DisconnectInfo(false, GolemDisconnectPhase.Send, ex);
                GolemUnityLog.Error(
                    $"send failed transport={TransportName} url={GolemUnityLog.RedactUrl(_url)} category={GolemUnityLog.CategoryName(info.Category)} {GolemUnityLog.FormatError(ex)}");
                DisconnectedEvent?.Invoke(info);
            }
        }

        public void SendUnreliable(byte[] bytes)
        {
            _protocol?.Send(GolemDatagramLane.Unreliable, bytes);
        }

        public void SendReliableUnordered(byte[] bytes)
        {
            _protocol?.Send(GolemDatagramLane.ReliableUnordered, bytes);
        }

        public void SendReliableOrdered(byte[] bytes)
        {
            _protocol?.Send(GolemDatagramLane.ReliableOrdered, bytes);
        }

        public async void Close()
        {
            var stream = _stream;
            _cts?.Cancel();
            _connected = false;
            if (stream != null)
            {
                try
                {
                    var frame = GolemReliableFrameCodec.Encode(GolemReliableFrameCodec.ClientCloseControlPayload(), MaxMessageBytes);
                    await stream.WriteAsync(frame, CancellationToken.None);
                }
                catch
                {
                    if (GolemUnityLog.DebugEnabled)
                    {
                        GolemUnityLog.Warn($"close control write failed transport={TransportName} url={GolemUnityLog.RedactUrl(_url)}");
                    }
                }
            }
            await DisposeTransportAsync();
        }

        private async Task ReceiveLoop(CancellationToken token)
        {
            try
            {
                while (!token.IsCancellationRequested && _stream != null)
                {
                    var message = await ReadFrameAsync(token);
                    MessageEvent?.Invoke(message);
                }
                if (!token.IsCancellationRequested)
                {
                    var info = new GolemDisconnectInfo(
                        false,
                        reason: "stream_closed",
                        transport: TransportName,
                        url: _url,
                        phase: GolemDisconnectPhase.Receive,
                        category: GolemDisconnectCategory.PeerClosed);
                    GolemUnityLog.LogDisconnect(TransportName, info);
                    DisconnectedEvent?.Invoke(info);
                }
            }
            catch (OperationCanceledException)
            {
                var info = new GolemDisconnectInfo(
                    true,
                    transport: TransportName,
                    url: _url,
                    phase: GolemDisconnectPhase.Close,
                    category: GolemDisconnectCategory.UserClosed);
                GolemUnityLog.LogDisconnect(TransportName, info);
                DisconnectedEvent?.Invoke(info);
            }
            catch (Exception ex)
            {
                var info = DisconnectInfo(false, GolemDisconnectPhase.Receive, ex);
                GolemUnityLog.LogDisconnect(TransportName, info);
                DisconnectedEvent?.Invoke(info);
            }
            finally
            {
                _connected = false;
            }
        }

        private async Task DatagramLoop(CancellationToken token)
        {
            try
            {
                while (!token.IsCancellationRequested && _session != null)
                {
                    var datagram = await _session.ReceiveDatagramAsync(token);
                    _protocol?.Receive(datagram.Payload, DeliverDatagram);
                }
            }
            catch (OperationCanceledException)
            {
            }
            catch (Exception ex)
            {
                _connected = false;
                var info = DisconnectInfo(false, GolemDisconnectPhase.Receive, ex);
                GolemUnityLog.Error(
                    $"datagram receive failed transport={TransportName} url={GolemUnityLog.RedactUrl(_url)} category={GolemUnityLog.CategoryName(info.Category)} {GolemUnityLog.FormatError(ex)}");
                DisconnectedEvent?.Invoke(info);
            }
        }

        private async Task SchedulerLoop(CancellationToken token)
        {
            try
            {
                while (!token.IsCancellationRequested)
                {
                    await Task.Delay(GolemDatagramProtocol.SchedulerIntervalMilliseconds, token);
                    var now = DateTime.UtcNow;
                    _protocol?.SendDueAck(now);
                    _protocol?.DrainRetries(now);
                }
            }
            catch (OperationCanceledException)
            {
            }
            catch (Exception ex)
            {
                _connected = false;
                var info = DisconnectInfo(false, GolemDisconnectPhase.Receive, ex);
                GolemUnityLog.Error(
                    $"datagram scheduler failed transport={TransportName} url={GolemUnityLog.RedactUrl(_url)} category={GolemUnityLog.CategoryName(info.Category)} {GolemUnityLog.FormatError(ex)}");
                DisconnectedEvent?.Invoke(info);
            }
        }

        private void DeliverDatagram(GolemDatagramLane lane, byte[] payload)
        {
            switch (lane)
            {
                case GolemDatagramLane.Unreliable:
                    UnreliableStateMessageEvent?.Invoke(payload);
                    break;
                case GolemDatagramLane.ReliableOrdered:
                    ReliableOrderedMessageEvent?.Invoke(payload);
                    break;
                case GolemDatagramLane.EventualState:
                    EventualStateMessageEvent?.Invoke(payload);
                    break;
            }
        }

        private void WriteDatagram(byte[] data)
        {
            if (_session == null || !_connected)
            {
                return;
            }
            _ = _session.SendDatagramAsync(data, _cts.Token);
        }

        private async Task<byte[]> ReadFrameAsync(CancellationToken token)
        {
            var header = await ReadExactlyAsync(GolemReliableFrameCodec.HeaderBytes, token);
            var length = GolemReliableFrameCodec.DecodeLength(header, MaxMessageBytes);
            return await ReadExactlyAsync(length, token);
        }

        private async Task<byte[]> ReadExactlyAsync(int length, CancellationToken token)
        {
            var buffer = new byte[length];
            var offset = 0;
            while (offset < length)
            {
                var bytesRead = await _stream.ReadAsync(buffer.AsMemory(offset, length - offset), token);
                if (bytesRead == 0)
                {
                    throw new InvalidOperationException("golem-unity: webtransport stream closed before reliable frame completed");
                }
                offset += bytesRead;
            }
            return buffer;
        }

        private async Task DisposeTransportAsync()
        {
            var stream = _stream;
            var session = _session;
            var client = _client;
            _stream = null;
            _session = null;
            _client = null;
            _protocol = null;

            try
            {
                if (stream != null)
                {
                    await stream.DisposeAsync();
                }
                if (session != null)
                {
                    await session.DisposeAsync();
                }
                if (client != null)
                {
                    await client.DisposeAsync();
                }
            }
            catch (Exception ex)
            {
                if (GolemUnityLog.DebugEnabled)
                {
                    GolemUnityLog.Warn($"dispose failed transport={TransportName} url={GolemUnityLog.RedactUrl(_url)} {GolemUnityLog.FormatError(ex)}");
                }
            }
            finally
            {
                _cts?.Dispose();
                _cts = null;
            }
        }

        private GolemDisconnectInfo DisconnectInfo(bool wasClean, GolemDisconnectPhase phase, Exception ex)
        {
            return new GolemDisconnectInfo(
                wasClean,
                error: ex,
                transport: TransportName,
                url: _url,
                phase: phase,
                category: wasClean ? GolemDisconnectCategory.UserClosed : GolemUnityLog.ClassifyError(ex));
        }
    }
}
