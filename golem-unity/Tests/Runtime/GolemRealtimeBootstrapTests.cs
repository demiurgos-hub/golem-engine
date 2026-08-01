using System;
using System.Collections.Generic;
using System.Security.Cryptography;
using System.Text;
using System.Threading.Tasks;
using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed class GolemRealtimeBootstrapTests
    {
        [Test]
        public void ParseRealtimeConfigReadsOptionalFields()
        {
            var hash = Sha256Hex("cert");
            var cfg = GolemRealtimeBootstrap.ParseRealtimeConfig($@"{{
                ""transport"": ""webtransport"",
                ""url"": ""https://example.com/wt"",
                ""serverCertificateHashes"": [{{""algorithm"": ""sha-256"", ""value"": ""{hash}""}}],
                ""eventualAckIntervalMs"": 25
            }}");

            Assert.That(cfg.Transport, Is.EqualTo(GolemConnectOptions.TransportWebTransport));
            Assert.That(cfg.Url, Is.EqualTo("https://example.com/wt"));
            Assert.That(cfg.EventualAckIntervalMs, Is.EqualTo(25));
            Assert.That(cfg.ServerCertificateHashes.Count, Is.EqualTo(1));
            Assert.That(cfg.ServerCertificateHashes[0].Value, Is.EqualTo(hash));
        }

        [Test]
        public void ParseRealtimeConfigRejectsInvalidAckIntervals()
        {
            Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig(
                    @"{""transport"":""websocket"",""url"":""ws://example.com/ws"",""eventualAckIntervalMs"":-1}"));
            Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig(
                    @"{""transport"":""websocket"",""url"":""ws://example.com/ws"",""eventualAckIntervalMs"":1.5}"));
            Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig(
                    @"{""transport"":""websocket"",""url"":""ws://example.com/ws"",""eventualAckIntervalMs"":2147483648}"));
        }

        [Test]
        public void ParseRealtimeConfigAllowsAbsentOrZeroAck()
        {
            var absent = GolemRealtimeBootstrap.ParseRealtimeConfig(
                @"{""transport"":""websocket"",""url"":""ws://example.com/ws""}");
            Assert.That(absent.EventualAckIntervalMs, Is.Null);
            var zero = GolemRealtimeBootstrap.ParseRealtimeConfig(
                @"{""transport"":""websocket"",""url"":""ws://example.com/ws"",""eventualAckIntervalMs"":0}");
            Assert.That(zero.EventualAckIntervalMs, Is.EqualTo(0));
            Assert.That(GolemConnectOptions.EffectiveEventualAckIntervalMs(0), Is.EqualTo(1));
            Assert.That(GolemConnectOptions.EffectiveEventualAckIntervalMs(40), Is.EqualTo(40));
        }

        [Test]
        public void BoundedDownloadHandlerCapsMemory()
        {
            var length = 0;
            var destination = new byte[16];
            Assert.That(GolemBoundedDownloadHandler.TryAccumulate(destination, ref length, Encoding.UTF8.GetBytes("hello "), 6, 16), Is.True);
            Assert.That(GolemBoundedDownloadHandler.TryAccumulate(destination, ref length, Encoding.UTF8.GetBytes("world!!"), 7, 16), Is.True);
            Assert.That(length, Is.EqualTo(13));
            Assert.That(GolemBoundedDownloadHandler.TryAccumulate(destination, ref length, Encoding.UTF8.GetBytes("overflow"), 8, 16), Is.False);
            Assert.That(length, Is.EqualTo(13));
        }

        [Test]
        public async Task FetchRealtimeConfigAsyncUsesInjectableFetchAndRejectsNon2xx()
        {
            var cfg = await GolemRealtimeBootstrap.FetchRealtimeConfigAsync(
                "https://example.test/cfg",
                default,
                (endpoint, _) =>
                {
                    Assert.That(endpoint, Is.EqualTo("https://example.test/cfg"));
                    return Task.FromResult(new GolemRealtimeHttpResponse(200,
                        @"{""transport"":""websocket"",""url"":""ws://example.com/ws""}"));
                });
            Assert.That(cfg.Transport, Is.EqualTo(GolemConnectOptions.TransportWebSocket));

            var error = Assert.ThrowsAsync<InvalidOperationException>(async () =>
                await GolemRealtimeBootstrap.FetchRealtimeConfigAsync(
                    "https://example.test/missing",
                    default,
                    (_, __) => Task.FromResult(new GolemRealtimeHttpResponse(404, "missing realtime config"))));
            Assert.That(error.Message, Does.Contain("status 404"));
            Assert.That(error.Message, Does.Contain("missing realtime config"));
        }

        [Test]
        public void FetchRealtimeConfigAsyncRejectsOversizedSuccessBodies()
        {
            var oversized = new string('x', GolemRealtimeBootstrap.MaxConfigBodyBytes + 8);
            var error = Assert.ThrowsAsync<InvalidOperationException>(async () =>
                await GolemRealtimeBootstrap.FetchRealtimeConfigAsync(
                    "https://example.test/cfg",
                    default,
                    (_, __) => Task.FromResult(new GolemRealtimeHttpResponse(200, oversized))));
            Assert.That(error.Message, Does.Contain("exceeds"));
        }

        [Test]
        public void AppendQueryPreservesEscapesDefaultPortFragmentAndDuplicates()
        {
            var url = "https://example.com:443/wt?room=a%2Bb&flag=1+2#frag";
            var result = GolemRealtimeBootstrap.AppendQuery(url, new[]
            {
                new KeyValuePair<string, string>("token", "a b"),
                new KeyValuePair<string, string>("token", "extra"),
                new KeyValuePair<string, string>("path", "a%b"),
            });
            Assert.That(result, Does.StartWith("https://example.com:443/wt?room=a%2Bb&flag=1+2"));
            Assert.That(result, Does.Contain("&token=a%20b&token=extra&path=a%25b#frag"));
            Assert.That(result, Does.Not.Contain("a%252Bb"));
            Assert.That(result, Does.EndWith("#frag"));
        }

        [Test]
        public void ConnectOptionsFromRealtimeConfigPropagatesAck()
        {
            var options = GolemRealtimeBootstrap.ConnectOptionsFromRealtimeConfig(
                new GolemRealtimeConfig(GolemConnectOptions.TransportWebTransport, "https://example.com/wt", null, 50));
            Assert.That(options.EventualAckIntervalMs, Is.EqualTo(50));
            Assert.That(GolemConnectOptions.EffectiveEventualAckIntervalMs(options.EventualAckIntervalMs), Is.EqualTo(50));
        }

        [Test]
        public void UnsupportedCertificateHashesAreRejectedExplicitly()
        {
            var hash = Sha256Hex("cert");
            var error = Assert.Throws<NotSupportedException>(() =>
                GolemRealtimeBootstrap.EnsureNativeWebTransportSupportsCertificateHashes(
                    new[] { new GolemCertificateHash("sha-256", hash) }));
            Assert.That(error.Message, Does.Contain("serverCertificateHashes"));
            Assert.That(error.Message, Does.Not.Contain(hash));
        }

        [Test]
        public void GameClientStringConnectPreservesParameterlessFactory()
        {
#pragma warning disable CS0618
            RecordingTransport created = null;
            var client = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                _ => Array.Empty<byte>(),
                _ => Array.Empty<byte>(),
                () =>
                {
                    created = new RecordingTransport();
                    return created;
                });
#pragma warning restore CS0618

            client.Connect("ws://example.invalid/ws?token=secret");
            Assert.That(created, Is.Not.Null);
            Assert.That(created.ConnectedUrl, Is.EqualTo("ws://example.invalid/ws?token=secret"));
        }

        [Test]
        public void LegacyFactoryRejectsNonEmptyTransportOnOptionsConnect()
        {
#pragma warning disable CS0618
            var client = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                _ => Array.Empty<byte>(),
                _ => Array.Empty<byte>(),
                () => new RecordingTransport());
#pragma warning restore CS0618

            var error = Assert.Throws<InvalidOperationException>(() =>
                client.Connect(new GolemConnectOptions(
                    "https://example.invalid/wt",
                    GolemConnectOptions.TransportWebTransport)));
            Assert.That(error.Message, Does.Contain("parameterless transport factory"));
        }

        [Test]
        public void GameClientOptionsConnectPropagatesAckAndTransportToFactory()
        {
            GolemConnectOptions seen = null;
            var client = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                _ => Array.Empty<byte>(),
                _ => Array.Empty<byte>(),
                options =>
                {
                    seen = options;
                    return new RecordingTransport();
                });

            client.Connect(new GolemConnectOptions(
                "https://example.invalid/wt",
                GolemConnectOptions.TransportWebTransport,
                Array.Empty<GolemCertificateHash>(),
                40));

            Assert.That(seen, Is.Not.Null);
            Assert.That(seen.Transport, Is.EqualTo(GolemConnectOptions.TransportWebTransport));
            Assert.That(seen.EventualAckIntervalMs, Is.EqualTo(40));
            Assert.That(GolemConnectOptions.EffectiveEventualAckIntervalMs(seen.EventualAckIntervalMs), Is.EqualTo(40));
        }

        private static string Sha256Hex(string input)
        {
            using (var sha = SHA256.Create())
            {
                var bytes = sha.ComputeHash(Encoding.UTF8.GetBytes(input));
                var sb = new StringBuilder(bytes.Length * 2);
                foreach (var b in bytes)
                {
                    sb.Append(b.ToString("x2"));
                }
                return sb.ToString();
            }
        }

        private sealed class RecordingEntityManager : IEntityManager
        {
            public void ApplyUpdate(object update) { }
            public object Get(long entityId) => null;
        }

        private sealed class RecordingTransport : IGolemTransport
        {
            public string ConnectedUrl { get; private set; }
            public bool Connected => true;
            public int MaxMessageBytes => GameClient.MaxReliableMessageBytes;
            public int MaxDatagramBytes => 0;
            public event Action ConnectedEvent;
            public event Action<byte[]> MessageEvent;
            public event Action<byte[]> UnreliableStateMessageEvent;
            public event Action<byte[]> ReliableOrderedMessageEvent;
            public event Action<byte[]> EventualStateMessageEvent;
            public event Action<GolemDisconnectInfo> DisconnectedEvent;

            public void Connect(string url)
            {
                ConnectedUrl = url;
                ConnectedEvent?.Invoke();
            }

            public void Send(byte[] bytes) { }
            public void SendUnreliable(byte[] bytes) { }
            public void SendReliableUnordered(byte[] bytes) { }
            public void SendReliableOrdered(byte[] bytes) { }
            public void Close() { }
        }
    }
}
