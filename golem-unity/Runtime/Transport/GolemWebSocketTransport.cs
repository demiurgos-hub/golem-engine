using System;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;

namespace GolemEngine.Unity
{
    /// <summary>Reliable WebSocket transport for the Golem stream protocol.</summary>
    public sealed class GolemWebSocketTransport : IGolemTransport
    {
        private const string TransportName = "websocket";
        private ClientWebSocket _socket;
        private CancellationTokenSource _cts;
        private string _url;

        public bool Connected => _socket?.State == WebSocketState.Open;
        public int MaxMessageBytes => GameClient.MaxReliableMessageBytes;
        public int MaxDatagramBytes => 0;
        public event Action ConnectedEvent;
        public event Action<byte[]> MessageEvent;
        public event Action<byte[]> UnreliableStateMessageEvent;
        public event Action<byte[]> ReliableOrderedMessageEvent;
        public event Action<byte[]> EventualStateMessageEvent;
        public event Action<GolemDisconnectInfo> DisconnectedEvent;

        public async void Connect(string url)
        {
            _url = url;
            _cts = new CancellationTokenSource();
            _socket = new ClientWebSocket();
            GolemUnityLog.Info($"connecting transport={TransportName} url={GolemUnityLog.RedactUrl(url)}");
            try
            {
                await _socket.ConnectAsync(new Uri(url), _cts.Token);
                GolemUnityLog.Info($"connected transport={TransportName} url={GolemUnityLog.RedactUrl(url)}");
                ConnectedEvent?.Invoke();
                _ = ReceiveLoop(_cts.Token);
            }
            catch (Exception ex)
            {
                var info = DisconnectInfo(false, GolemDisconnectPhase.Connect, ex);
                GolemUnityLog.Error(
                    $"connect failed transport={TransportName} url={GolemUnityLog.RedactUrl(url)} category={GolemUnityLog.CategoryName(info.Category)} {GolemUnityLog.FormatError(ex)}");
                DisconnectedEvent?.Invoke(info);
                _socket?.Dispose();
                _socket = null;
                _cts?.Dispose();
                _cts = null;
            }
        }

        public async void Send(byte[] bytes)
        {
            if (!Connected)
            {
                return;
            }
            if (bytes.Length > MaxMessageBytes)
            {
                throw new InvalidOperationException($"golem-unity: reliable message size {bytes.Length} exceeds max reliable message {MaxMessageBytes}");
            }
            try
            {
                await _socket.SendAsync(new ArraySegment<byte>(bytes), WebSocketMessageType.Binary, true, _cts.Token);
            }
            catch (Exception ex)
            {
                var info = DisconnectInfo(false, GolemDisconnectPhase.Send, ex);
                GolemUnityLog.Error(
                    $"send failed transport={TransportName} url={GolemUnityLog.RedactUrl(_url)} state={_socket?.State} category={GolemUnityLog.CategoryName(info.Category)} {GolemUnityLog.FormatError(ex)}");
                DisconnectedEvent?.Invoke(info);
            }
        }

        public void SendUnreliable(byte[] bytes)
        {
        }

        public void SendReliableUnordered(byte[] bytes)
        {
        }

        public void SendReliableOrdered(byte[] bytes)
        {
        }

        public async void Close()
        {
            var socket = _socket;
            if (socket == null)
            {
                return;
            }

            _cts?.Cancel();
            try
            {
                if (socket.State == WebSocketState.Open || socket.State == WebSocketState.CloseReceived)
                {
                    await socket.SendAsync(
                        new ArraySegment<byte>(GolemReliableFrameCodec.ClientCloseControlPayload()),
                        WebSocketMessageType.Binary,
                        true,
                        CancellationToken.None);
                    await socket.CloseAsync(WebSocketCloseStatus.NormalClosure, "golem client closed", CancellationToken.None);
                }
            }
            catch
            {
                if (GolemUnityLog.DebugEnabled)
                {
                    GolemUnityLog.Warn($"close failed transport={TransportName} url={GolemUnityLog.RedactUrl(_url)}");
                }
            }
            finally
            {
                socket.Dispose();
                _socket = null;
                _cts?.Dispose();
                _cts = null;
            }
        }

        private async Task ReceiveLoop(CancellationToken token)
        {
            var buffer = new byte[MaxMessageBytes];
            try
            {
                while (!token.IsCancellationRequested && _socket != null)
                {
                    var offset = 0;
                    WebSocketReceiveResult result;
                    do
                    {
                        if (offset == buffer.Length)
                        {
                            throw new InvalidOperationException($"golem-unity: websocket payload exceeds max reliable message {MaxMessageBytes}");
                        }
                        result = await _socket.ReceiveAsync(new ArraySegment<byte>(buffer, offset, buffer.Length - offset), token);
                        if (result.MessageType == WebSocketMessageType.Close)
                        {
                            var info = new GolemDisconnectInfo(true, (int?)result.CloseStatus, result.CloseStatusDescription);
                            GolemUnityLog.LogDisconnect("websocket", info);
                            DisconnectedEvent?.Invoke(info);
                            return;
                        }
                        offset += result.Count;
                    } while (!result.EndOfMessage);

                    var message = new byte[offset];
                    Array.Copy(buffer, message, offset);
                    MessageEvent?.Invoke(message);
                }
                if (!token.IsCancellationRequested)
                {
                    var info = new GolemDisconnectInfo(
                        false,
                        reason: "socket_closed",
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
