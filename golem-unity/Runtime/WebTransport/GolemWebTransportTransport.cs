using System;
using System.IO;
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
        private Connection _connection;

        private sealed class Connection
        {
            internal readonly CancellationTokenSource Cancellation = new CancellationTokenSource();
            internal readonly string Url;
            internal WebTransportClient Client;
            internal WebTransportSession Session;
            internal WebTransportStream Stream;
            internal GolemDatagramProtocol Protocol;
            internal GolemWebTransportCloseState CloseState;
            internal bool Connected;
            internal bool ClosingLocally;
            internal bool DatagramSendFailureLogged;
            internal readonly CancellationToken Token;
            private bool _cancellationDisposed;
            internal Connection(string url) { Url = url; Token = Cancellation.Token; }
            internal void Cancel()
            {
                lock (this) { if (!_cancellationDisposed) Cancellation.Cancel(); }
            }
            internal void DisposeCancellation()
            {
                lock (this)
                {
                    if (_cancellationDisposed) return;
                    _cancellationDisposed = true;
                    Cancellation.Dispose();
                }
            }
        }

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
            _eventualAckIntervalMilliseconds = GolemConnectOptions.EffectiveEventualAckIntervalMs(eventualAckIntervalMilliseconds);
        }

        /// <summary>
        /// Builds a WebTransport transport from connect options.
        /// Applies eventual ACK interval; rejects non-empty browser-style certificate hashes.
        /// Custom options-aware factories that construct this type directly own certificate handling.
        /// </summary>
        public static GolemWebTransportTransport FromConnectOptions(GolemConnectOptions options)
        {
            if (options == null)
            {
                throw new ArgumentNullException(nameof(options));
            }
            GolemRealtimeBootstrap.EnsureNativeWebTransportSupportsCertificateHashes(options.ServerCertificateHashes);
            var clientOptions = new WebTransportClientOptions();
            return new GolemWebTransportTransport(clientOptions, options.EventualAckIntervalMs);
        }

        /// <summary>
        /// Builds a WebTransport transport from connect options with an explicit client options instance
        /// (for example development <see cref="WebTransportClientOptions.AllowUntrustedCertificates"/>).
        /// Built-in helpers still reject non-empty browser-style certificate hashes; custom factories that
        /// bypass <see cref="FromConnectOptions"/> own certificate policy.
        /// </summary>
        public static GolemWebTransportTransport FromConnectOptions(
            GolemConnectOptions options,
            WebTransportClientOptions clientOptions)
        {
            if (options == null)
            {
                throw new ArgumentNullException(nameof(options));
            }
            GolemRealtimeBootstrap.EnsureNativeWebTransportSupportsCertificateHashes(options.ServerCertificateHashes);
            return new GolemWebTransportTransport(clientOptions, options.EventualAckIntervalMs);
        }

        /// <summary>Resolved eventual-state ACK interval used by the datagram protocol (test seam).</summary>
        public int EventualAckIntervalMilliseconds => _eventualAckIntervalMilliseconds;

        public bool Connected => _connection?.Connected == true;
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
            var connection = new Connection(url);
            _connection = connection;
            var token = connection.Token;
            var phase = GolemDisconnectPhase.Connect;
            GolemUnityLog.Info($"connecting transport={TransportName} url={GolemUnityLog.RedactUrl(url)}");
            try
            {
                // IO must not wait behind Unity's main thread: the server's ACK
                // deadline starts at CONNECT, before scene startup finishes.
                var client = new WebTransportClient(_options);
                lock (connection) connection.Client = client;
                var session = await client.ConnectAsync(new Uri(url), token).ConfigureAwait(false);
                lock (connection) connection.Session = session;
                token.ThrowIfCancellationRequested();
                connection.CloseState = new GolemWebTransportCloseState(
                    ReadPeerCloseAsync(session, url), info => ReportDisconnect(connection, info), url);
                _ = connection.CloseState.ObserveAsync();
                phase = GolemDisconnectPhase.OpenStream;
                var stream = await session.OpenBidirectionalStreamAsync(token).ConfigureAwait(false);
                lock (connection) connection.Stream = stream;
                token.ThrowIfCancellationRequested();
                phase = GolemDisconnectPhase.StreamHeaderWrite;
                connection.Protocol = new GolemDatagramProtocol(bytes => WriteDatagram(connection, bytes), _eventualAckIntervalMilliseconds);
                // The first write registers the client-opened stream with Golem.
                await stream.WriteAsync(Array.Empty<byte>(), token).ConfigureAwait(false);
                token.ThrowIfCancellationRequested();
                lock (connection)
                {
                    token.ThrowIfCancellationRequested();
                    if (connection.CloseState.Reported) throw new OperationCanceledException(token);
                    connection.Connected = true;
                    GolemUnityLog.Info($"connected transport={TransportName} url={GolemUnityLog.RedactUrl(url)}");
                    ConnectedEvent?.Invoke();
                }
                _ = Task.Run(() => ReceiveLoop(connection, token));
                _ = Task.Run(() => DatagramLoop(connection, token));
                _ = Task.Run(() => SchedulerLoop(connection, token));
            }
            catch (Exception error)
            {
                if (!token.IsCancellationRequested) await FailAsync(connection, error, phase).ConfigureAwait(false);
                await DisposeTransportAsync(connection).ConfigureAwait(false);
            }
        }

        public async void Send(byte[] bytes)
        {
            var connection = _connection;
            if (connection == null || !connection.Connected || connection.Stream == null) return;
            if (bytes == null) throw new ArgumentNullException(nameof(bytes));
            if (bytes.Length > MaxMessageBytes)
                throw new InvalidOperationException($"golem-unity: reliable message size {bytes.Length} exceeds max reliable message {MaxMessageBytes}");
            try
            {
                var payload = connection.Protocol == null ? bytes : connection.Protocol.WrapStreamPayload(bytes);
                var frame = GolemReliableFrameCodec.Encode(payload, MaxMessageBytes);
                await connection.Stream.WriteAsync(frame, connection.Token).ConfigureAwait(false);
            }
            catch (Exception error)
            {
                await FailAsync(connection, error, GolemDisconnectPhase.Send).ConfigureAwait(false);
            }
        }

        public void SendUnreliable(byte[] bytes) => _connection?.Protocol?.Send(GolemDatagramLane.Unreliable, bytes);
        public void SendReliableUnordered(byte[] bytes) => _connection?.Protocol?.Send(GolemDatagramLane.ReliableUnordered, bytes);
        public void SendReliableOrdered(byte[] bytes) => _connection?.Protocol?.Send(GolemDatagramLane.ReliableOrdered, bytes);

        public async void Close()
        {
            var connection = _connection;
            if (connection == null) return;
            _connection = null;
            connection.Connected = false;
            connection.ClosingLocally = true;
            connection.CloseState?.CloseLocally();
            connection.Cancel();
            if (connection.Stream != null)
            {
                try
                {
                    var frame = GolemReliableFrameCodec.Encode(GolemReliableFrameCodec.ClientCloseControlPayload(), MaxMessageBytes);
                    await connection.Stream.WriteAsync(frame, CancellationToken.None).ConfigureAwait(false);
                }
                catch
                {
                    if (GolemUnityLog.DebugEnabled)
                        GolemUnityLog.Warn($"close control write failed transport={TransportName} url={GolemUnityLog.RedactUrl(connection.Url)}");
                }
            }
            await DisposeTransportAsync(connection).ConfigureAwait(false);
        }

        private static async Task<GolemDisconnectInfo> ReadPeerCloseAsync(WebTransportSession session, string url)
        {
            var close = await session.CloseCompletion.ConfigureAwait(false);
            return GolemWebTransportCloseState.PeerClose(checked((uint)close.ErrorCode), close.Reason, url);
        }

        private void ReportDisconnect(Connection connection, GolemDisconnectInfo info)
        {
            lock (connection)
            {
                connection.Connected = false;
                if (ReferenceEquals(_connection, connection) || connection.ClosingLocally)
                {
                    GolemUnityLog.LogDisconnect(TransportName, info);
                    DisconnectedEvent?.Invoke(info);
                }
            }
            if (!connection.ClosingLocally)
            {
                connection.Cancel();
                _ = DisposeTransportAsync(connection);
            }
        }

        private Task FailAsync(Connection connection, Exception error, GolemDisconnectPhase phase)
        {
            connection.Connected = false;
            if (connection.CloseState != null)
            {
                // QUIC stream shutdown may precede the CONNECT close capsule.
                // Frame/codec failures are not session closure and stay immediate.
                return connection.CloseState.FailAsync(error, phase,
                    error is EndOfStreamException || error is WebTransportException);
            }
            if (ReferenceEquals(_connection, connection))
                ReportDisconnect(connection, GolemUnityLog.CreateDisconnectInfo(false, phase, error, TransportName, connection.Url));
            return Task.CompletedTask;
        }

        private async Task ReceiveLoop(Connection connection, CancellationToken token)
        {
            try
            {
                while (!token.IsCancellationRequested)
                {
                    var message = await ReadFrameAsync(connection.Stream, token).ConfigureAwait(false);
                    if (connection.Connected) MessageEvent?.Invoke(message);
                }
            }
            catch (OperationCanceledException) when (token.IsCancellationRequested) { }
            catch (Exception error)
            {
                await FailAsync(connection, error, GolemDisconnectPhase.Receive).ConfigureAwait(false);
            }
        }

        private async Task DatagramLoop(Connection connection, CancellationToken token)
        {
            try
            {
                while (!token.IsCancellationRequested)
                {
                    var datagram = await connection.Session.ReceiveDatagramAsync(token).ConfigureAwait(false);
                    if (connection.Connected) connection.Protocol.Receive(datagram.Payload, DeliverDatagram);
                }
            }
            catch (OperationCanceledException) when (token.IsCancellationRequested) { }
            catch (Exception error)
            {
                await FailAsync(connection, error, GolemDisconnectPhase.Receive).ConfigureAwait(false);
            }
        }

        private async Task SchedulerLoop(Connection connection, CancellationToken token)
        {
            try
            {
                while (!token.IsCancellationRequested)
                {
                    await Task.Delay(GolemDatagramProtocol.SchedulerIntervalMilliseconds, token).ConfigureAwait(false);
                    if (!connection.Connected) continue;
                    var now = DateTime.UtcNow;
                    connection.Protocol.SendDueAck(now);
                    connection.Protocol.DrainRetries(now);
                }
            }
            catch (OperationCanceledException) when (token.IsCancellationRequested) { }
            catch (Exception error)
            {
                await FailAsync(connection, error, GolemDisconnectPhase.Receive).ConfigureAwait(false);
            }
        }

        private void DeliverDatagram(GolemDatagramLane lane, byte[] payload)
        {
            switch (lane)
            {
                case GolemDatagramLane.Unreliable: UnreliableStateMessageEvent?.Invoke(payload); break;
                case GolemDatagramLane.ReliableOrdered: ReliableOrderedMessageEvent?.Invoke(payload); break;
                case GolemDatagramLane.EventualState: EventualStateMessageEvent?.Invoke(payload); break;
            }
        }

        private void WriteDatagram(Connection connection, byte[] data)
        {
            if (connection.Session == null || !connection.Connected) return;
            _ = ObserveDatagramSendAsync(connection, connection.Session.SendDatagramAsync(data, connection.Token));
        }

        private static async Task ObserveDatagramSendAsync(Connection connection, ValueTask send)
        {
            try { await send.ConfigureAwait(false); }
            catch (OperationCanceledException) { }
            catch (Exception error)
            {
                // Best-effort datagram failure is diagnostic, not a session close.
                if (!connection.DatagramSendFailureLogged)
                {
                    connection.DatagramSendFailureLogged = true;
                    GolemUnityLog.Error($"datagram send failed transport={TransportName} url={GolemUnityLog.RedactUrl(connection.Url)} category={GolemUnityLog.CategoryName(GolemUnityLog.ClassifyError(error))} {GolemUnityLog.FormatError(error)}");
                }
                else if (GolemUnityLog.DebugEnabled)
                    GolemUnityLog.Warn($"datagram send failed transport={TransportName} url={GolemUnityLog.RedactUrl(connection.Url)} {GolemUnityLog.FormatError(error)}");
            }
        }

        private async Task<byte[]> ReadFrameAsync(WebTransportStream stream, CancellationToken token)
        {
            var header = await ReadExactlyAsync(stream, GolemReliableFrameCodec.HeaderBytes, token).ConfigureAwait(false);
            var length = GolemReliableFrameCodec.DecodeLength(header, MaxMessageBytes);
            return await ReadExactlyAsync(stream, length, token).ConfigureAwait(false);
        }

        private static async Task<byte[]> ReadExactlyAsync(WebTransportStream stream, int length, CancellationToken token)
        {
            var buffer = new byte[length];
            var offset = 0;
            while (offset < length)
            {
                var bytesRead = await stream.ReadAsync(buffer.AsMemory(offset, length - offset), token).ConfigureAwait(false);
                if (bytesRead == 0)
                    throw new EndOfStreamException("golem-unity: webtransport stream closed before reliable frame completed");
                offset += bytesRead;
            }
            return buffer;
        }

        private static async Task DisposeTransportAsync(Connection connection)
        {
            IAsyncDisposable[] resources;
            lock (connection)
            {
                resources = new IAsyncDisposable[] { connection.Stream, connection.Session, connection.Client };
                connection.Stream = null;
                connection.Session = null;
                connection.Client = null;
            }
            foreach (var resource in resources)
            {
                try
                {
                    if (resource != null) await resource.DisposeAsync().ConfigureAwait(false);
                }
                catch (Exception error)
                {
                    if (GolemUnityLog.DebugEnabled)
                        GolemUnityLog.Warn($"dispose failed transport={TransportName} url={GolemUnityLog.RedactUrl(connection.Url)} {GolemUnityLog.FormatError(error)}");
                }
            }
            // All IO uses this connection's token. A delayed callback can never
            // cancel or dispose a newer connection created on the same adapter.
            connection.DisposeCancellation();
        }
    }
}
