using System;
using System.Collections.Generic;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
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
        public void ParseRealtimeConfigReadsWebSocketFallbackWithoutShadowingPrimary()
        {
            var cfg = GolemRealtimeBootstrap.ParseRealtimeConfig(@"{
                ""fallback"": {
                    ""transport"": ""websocket"",
                    ""url"": ""wss://example.com/api/ws"",
                    ""eventualAckIntervalMs"": 99,
                    ""serverCertificateHashes"": [{""algorithm"":""sha-256"",""value"":""0000000000000000000000000000000000000000000000000000000000000000""}]
                },
                ""transport"": ""webtransport"",
                ""url"": ""https://example.com:4433/api/wt"",
                ""eventualAckIntervalMs"": 25
            }");

            Assert.That(cfg.Transport, Is.EqualTo(GolemConnectOptions.TransportWebTransport));
            Assert.That(cfg.Url, Is.EqualTo("https://example.com:4433/api/wt"));
            Assert.That(cfg.EventualAckIntervalMs, Is.EqualTo(25));
            Assert.That(cfg.ServerCertificateHashes, Is.Empty);
            Assert.That(cfg.Fallback, Is.Not.Null);
            Assert.That(cfg.Fallback.Transport, Is.EqualTo(GolemConnectOptions.TransportWebSocket));
            Assert.That(cfg.Fallback.Url, Is.EqualTo("wss://example.com/api/ws"));
        }

        [Test]
        public void ParseRealtimeConfigRejectsUnsupportedFallbackPairs()
        {
            Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig(
                    @"{""transport"":""websocket"",""url"":""wss://example.com/ws"",""fallback"":{""transport"":""websocket"",""url"":""wss://example.com/other""}}"));
            Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig(
                    @"{""transport"":""webtransport"",""url"":""https://example.com/wt"",""fallback"":{""transport"":""webtransport"",""url"":""https://example.com/other""}}"));
            Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig(
                    @"{""transport"":""webtransport"",""url"":""https://example.com/wt"",""fallback"":null}"));
        }

        [Test]
        public void RealtimeConfigCopiesCertificateHashes()
        {
            var first = new GolemCertificateHash("sha-256", Sha256Hex("first"));
            var source = new List<GolemCertificateHash> { first };
            var config = new GolemRealtimeConfig(
                GolemConnectOptions.TransportWebTransport,
                "https://example.com/wt",
                source);

            source[0] = new GolemCertificateHash("sha-256", Sha256Hex("changed"));
            source.Clear();

            Assert.That(config.ServerCertificateHashes.Count, Is.EqualTo(1));
            Assert.That(config.ServerCertificateHashes[0].Value, Is.EqualTo(first.Value));
        }

        [Test]
        public void ParseRealtimeConfigRejectsInvalidEndpointSchemesAndTrailingJson()
        {
            Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig(
                    @"{""transport"":""webtransport"",""url"":""ws://example.com/wt""}"));
            Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig(
                    @"{""transport"":""websocket"",""url"":""https://example.com/ws""}"));
            Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig(
                    @"{""transport"":""websocket"",""url"":""wss://example.com/ws""} trailing"));
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
        public void LegacyFactoryRejectsRealtimeConnectionPlan()
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
                client.Connect(FallbackConfig(), (options, _) => Task.FromResult(options)));
            Assert.That(error.Message, Does.Contain("parameterless transport factory"));
        }

        [Test]
        public void BuiltInCapabilitiesRejectNativeWebTransportCertificateHashes()
        {
            var options = new GolemConnectOptions(
                "https://example.invalid/api/wt",
                GolemConnectOptions.TransportWebTransport,
                new[] { new GolemCertificateHash("sha-256", Sha256Hex("cert")) });

            Assert.That(GolemTransportCapabilities.BuiltInTransportSupported(options), Is.False);
#if !UNITY_WEBGL
            Assert.That(
                GolemTransportCapabilities.BuiltInTransportSupported(new GolemConnectOptions(
                    "wss://example.invalid/api/ws",
                    GolemConnectOptions.TransportWebSocket)),
                Is.True);
#endif
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

        [Test]
        public async Task ConnectionPlanSkipsUnsupportedWebTransportBeforeResolvingCredentials()
        {
            var resolved = new List<string>();
            var created = new List<GolemConnectOptions>();
            var client = NewPlanClient(
                options =>
                {
                    created.Add(options);
                    return new RecordingTransport();
                },
                options => options.Transport == GolemConnectOptions.TransportWebSocket);
            var config = FallbackConfig();

            client.Connect(config, (options, _) =>
            {
                resolved.Add(options.Transport);
                return Task.FromResult(new GolemConnectOptions(
                    options.Url + "?ticket=" + resolved.Count,
                    options.Transport,
                    options.ServerCertificateHashes,
                    options.EventualAckIntervalMs));
            });
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);

            Assert.That(resolved, Is.EqualTo(new[] { GolemConnectOptions.TransportWebSocket }));
            Assert.That(created, Has.Count.EqualTo(1));
            Assert.That(created[0].Url, Does.EndWith("ticket=1"));
            Assert.That(created[0].ServerCertificateHashes, Is.Empty);
            Assert.That(created[0].EventualAckIntervalMs, Is.Zero);
        }

        [Test]
        public async Task ConnectionPlanUsesFreshCredentialsAfterPreOpenWebTransportFailure()
        {
            var resolved = new List<string>();
            var created = new List<GolemConnectOptions>();
            var client = NewPlanClient(options =>
            {
                created.Add(options);
                return options.Transport == GolemConnectOptions.TransportWebTransport
                    ? (IGolemTransport)new PreOpenFailureTransport(options.Transport)
                    : new RecordingTransport();
            });

            client.Connect(FallbackConfig(), (options, _) =>
            {
                resolved.Add(options.Transport);
                return Task.FromResult(new GolemConnectOptions(
                    options.Url + "?ticket=" + resolved.Count,
                    options.Transport,
                    options.ServerCertificateHashes,
                    options.EventualAckIntervalMs));
            });
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);

            Assert.That(resolved, Is.EqualTo(new[]
            {
                GolemConnectOptions.TransportWebTransport,
                GolemConnectOptions.TransportWebSocket,
            }));
            Assert.That(created, Has.Count.EqualTo(2));
            Assert.That(created[0].Url, Does.EndWith("ticket=1"));
            Assert.That(created[1].Url, Does.EndWith("ticket=2"));
        }

        [Test]
        public async Task SuccessfulWebSocketFallbackIsStickyForEquivalentConfig()
        {
            var resolved = new List<string>();
            var client = NewPlanClient(options =>
                options.Transport == GolemConnectOptions.TransportWebTransport
                    ? (IGolemTransport)new PreOpenFailureTransport(options.Transport)
                    : new RecordingTransport());
            GolemConnectOptionsResolver resolver = (options, _) =>
            {
                resolved.Add(options.Transport);
                return Task.FromResult(options);
            };
            var config = FallbackConfig();

            client.Connect(config, resolver);
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);
            resolved.Clear();
            client.Connect(FallbackConfig(), resolver);
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);

            Assert.That(resolved, Is.EqualTo(new[] { GolemConnectOptions.TransportWebSocket }));
        }

        [Test]
        public async Task ResolverFailureDoesNotFallThroughToFallback()
        {
            var created = 0;
            var failures = 0;
            var client = NewPlanClient(_ =>
            {
                created++;
                return new RecordingTransport();
            });
            client.DisconnectedEvent += _ => failures++;

            client.Connect(FallbackConfig(), (_, __) =>
                Task.FromException<GolemConnectOptions>(new InvalidOperationException("ticket failed")));
            await Task.Delay(50);

            Assert.That(created, Is.Zero);
            Assert.That(failures, Is.LessThanOrEqualTo(1));
        }

        [Test]
        public async Task TransportFactoryFailureFallsBackWithFreshCredentials()
        {
            var resolved = new List<string>();
            var client = NewPlanClient(options =>
            {
                if (options.Transport == GolemConnectOptions.TransportWebTransport)
                {
                    throw new DllNotFoundException("native webtransport unavailable");
                }
                return new RecordingTransport();
            });

            client.Connect(FallbackConfig(), (options, _) =>
            {
                resolved.Add(options.Transport);
                return Task.FromResult(WithTicket(options, resolved.Count));
            });
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);

            Assert.That(resolved, Is.EqualTo(new[]
            {
                GolemConnectOptions.TransportWebTransport,
                GolemConnectOptions.TransportWebSocket,
            }));
        }

        [Test]
        public async Task ParentCancellationDuringResolverDoesNotDialOrFallThrough()
        {
            var created = 0;
            var resolved = new List<string>();
            var entered = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            var release = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            var cancellation = new CancellationTokenSource();
            var client = NewPlanClient(_ =>
            {
                created++;
                return new RecordingTransport();
            });

            client.Connect(FallbackConfig(), async (options, _) =>
            {
                resolved.Add(options.Transport);
                entered.TrySetResult(true);
                await release.Task;
                return options;
            }, cancellation.Token);
            await entered.Task;
            cancellation.Cancel();
            release.TrySetResult(true);
            await Task.Delay(50);

            Assert.That(created, Is.Zero);
            Assert.That(resolved, Is.EqualTo(new[] { GolemConnectOptions.TransportWebTransport }));
        }

        [Test]
        public async Task ParentCancellationClosesPendingWebSocket()
        {
            var pending = new ManualTransport(autoOpen: false);
            var cancellation = new CancellationTokenSource();
            var client = NewPlanClient(_ => pending);
            var config = new GolemRealtimeConfig(
                GolemConnectOptions.TransportWebSocket,
                "wss://example.invalid/api/ws");

            client.Connect(config, (options, _) => Task.FromResult(options), cancellation.Token);
            await WaitForAsync(() => pending.ConnectCalls == 1);
            cancellation.Cancel();
            await WaitForAsync(() => pending.CloseCalls > 0);

            Assert.That(client.ConnectedTransport, Is.Null);
        }

        [TestCase(401)]
        [TestCase(403)]
        [TestCase(426)]
        public async Task TerminalHandshakeStatusDoesNotFallThrough(int status)
        {
            var resolved = new List<string>();
            var created = new List<string>();
            var client = NewPlanClient(options =>
            {
                created.Add(options.Transport);
                return new PreOpenFailureTransport(
                    options.Transport,
                    new GolemDisconnectInfo(
                        false,
                        code: status,
                        reason: status == 426 ? "Upgrade Required" : "ticket rejected",
                        transport: options.Transport,
                        phase: GolemDisconnectPhase.Connect,
                        category: GolemDisconnectCategory.Protocol));
            });

            client.Connect(FallbackConfig(), (options, _) =>
            {
                resolved.Add(options.Transport);
                return Task.FromResult(WithTicket(options, resolved.Count));
            });
            await Task.Delay(100);

            Assert.That(resolved, Is.EqualTo(new[] { GolemConnectOptions.TransportWebTransport }));
            Assert.That(created, Is.EqualTo(new[] { GolemConnectOptions.TransportWebTransport }));
            Assert.That(client.ConnectedTransport, Is.Null);
        }

        [Test]
        public async Task TicketFailureReasonIsTerminalWithoutStatusCode()
        {
            var created = new List<string>();
            var client = NewPlanClient(options =>
            {
                created.Add(options.Transport);
                return new PreOpenFailureTransport(
                    options.Transport,
                    new GolemDisconnectInfo(
                        false,
                        reason: "ticket expired",
                        transport: options.Transport,
                        phase: GolemDisconnectPhase.Connect,
                        category: GolemDisconnectCategory.Protocol));
            });

            client.Connect(FallbackConfig(), (options, _) => Task.FromResult(options));
            await Task.Delay(100);

            Assert.That(created, Is.EqualTo(new[] { GolemConnectOptions.TransportWebTransport }));
            Assert.That(client.ConnectedTransport, Is.Null);
        }

        [Test]
        public async Task EstablishedDisconnectDoesNotSwitchTransport()
        {
            var webTransport = new ManualTransport(autoOpen: true);
            var resolved = new List<string>();
            var client = NewPlanClient(options =>
                options.Transport == GolemConnectOptions.TransportWebTransport
                    ? (IGolemTransport)webTransport
                    : new RecordingTransport());

            client.Connect(FallbackConfig(), (options, _) =>
            {
                resolved.Add(options.Transport);
                return Task.FromResult(options);
            });
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebTransport);
            webTransport.Fail(new GolemDisconnectInfo(
                false,
                reason: "lost",
                transport: GolemConnectOptions.TransportWebTransport,
                phase: GolemDisconnectPhase.Receive,
                category: GolemDisconnectCategory.PeerClosed));
            await Task.Delay(50);

            Assert.That(resolved, Is.EqualTo(new[] { GolemConnectOptions.TransportWebTransport }));
            Assert.That(client.ConnectedTransport, Is.Null);
        }

        [Test]
        public async Task StalePrimaryOpenCannotReplaceNewWebSocketConnection()
        {
            var stale = new ManualTransport(autoOpen: false);
            var client = NewPlanClient(options =>
                options.Transport == GolemConnectOptions.TransportWebTransport
                    ? (IGolemTransport)stale
                    : new RecordingTransport());

            client.Connect(FallbackConfig(), (options, _) => Task.FromResult(options));
            await WaitForAsync(() => stale.ConnectCalls == 1);
            client.Connect(
                new GolemRealtimeConfig(
                    GolemConnectOptions.TransportWebSocket,
                    "wss://other.example.invalid/api/ws"),
                (options, _) => Task.FromResult(options));
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);

            stale.Open();
            await Task.Delay(20);
            Assert.That(client.ConnectedTransport, Is.EqualTo(GolemConnectOptions.TransportWebSocket));
        }

        [Test]
        public async Task StickyWebSocketResetsWhenCredentialFreeConfigChanges()
        {
            var resolved = new List<string>();
            var client = NewPlanClient(options =>
                options.Transport == GolemConnectOptions.TransportWebTransport
                    ? (IGolemTransport)new PreOpenFailureTransport(options.Transport)
                    : new RecordingTransport());
            GolemConnectOptionsResolver resolver = (options, _) =>
            {
                resolved.Add(options.Transport);
                return Task.FromResult(options);
            };

            client.Connect(FallbackConfig(), resolver);
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);
            resolved.Clear();
            client.Connect(
                new GolemRealtimeConfig(
                    GolemConnectOptions.TransportWebTransport,
                    "https://example.invalid/api/wt",
                    fallback: new GolemRealtimeEndpoint(
                        GolemConnectOptions.TransportWebSocket,
                        "wss://changed.example.invalid/api/ws")),
                resolver);
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);

            Assert.That(resolved, Is.EqualTo(new[]
            {
                GolemConnectOptions.TransportWebTransport,
                GolemConnectOptions.TransportWebSocket,
            }));
        }

        [Test]
        public async Task DirectConnectResetsStickyWebSocketAffinity()
        {
            var resolved = new List<string>();
            var client = NewPlanClient(options =>
                options.Transport == GolemConnectOptions.TransportWebTransport
                    ? (IGolemTransport)new PreOpenFailureTransport(options.Transport)
                    : new RecordingTransport());
            GolemConnectOptionsResolver resolver = (options, _) =>
            {
                resolved.Add(options.Transport);
                return Task.FromResult(options);
            };
            var config = FallbackConfig();

            client.Connect(config, resolver);
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);
            client.Connect(new GolemConnectOptions(
                "wss://direct.example.invalid/api/ws",
                GolemConnectOptions.TransportWebSocket));
            resolved.Clear();
            client.Connect(config, resolver);
            await WaitForAsync(() => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket);

            Assert.That(resolved, Is.EqualTo(new[]
            {
                GolemConnectOptions.TransportWebTransport,
                GolemConnectOptions.TransportWebSocket,
            }));
        }

        [Test]
        public async Task ResolverCannotChangeCandidateEndpointOrMetadata()
        {
            var created = 0;
            var client = NewPlanClient(_ =>
            {
                created++;
                return new RecordingTransport();
            });

            client.Connect(FallbackConfig(), (options, _) => Task.FromResult(
                new GolemConnectOptions(
                    "https://attacker.example/api/wt?ticket=secret",
                    options.Transport,
                    options.ServerCertificateHashes,
                    options.EventualAckIntervalMs)));
            await Task.Delay(100);

            Assert.That(created, Is.Zero);
            Assert.That(client.ConnectedTransport, Is.Null);
        }

        [Test]
        public void DisconnectDiagnosticsRedactTicketBearingUrlsAndReasons()
        {
            var sanitize = typeof(GameClient).GetMethod(
                "SanitizeDisconnectInfo",
                System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Static);
            Assert.That(sanitize, Is.Not.Null);
            var options = new GolemConnectOptions(
                "https://example.invalid/api/wt?ticket=resolver-secret",
                GolemConnectOptions.TransportWebTransport);
            var raw = new GolemDisconnectInfo(
                false,
                reason: "failed https://example.invalid/api/wt?ticket=reason-secret",
                error: new InvalidOperationException("url has ticket=error-secret"),
                transport: GolemConnectOptions.TransportWebTransport,
                url: "https://example.invalid/api/wt?ticket=url-secret",
                phase: GolemDisconnectPhase.Connect,
                category: GolemDisconnectCategory.Protocol);

            var safe = (GolemDisconnectInfo)sanitize.Invoke(null, new object[] { raw, options });
            var diagnostics = (safe.Reason ?? string.Empty) + " " +
                              (safe.Url ?? string.Empty) + " " +
                              (safe.Error?.Message ?? string.Empty);

            Assert.That(diagnostics, Does.Not.Contain("resolver-secret"));
            Assert.That(diagnostics, Does.Not.Contain("reason-secret"));
            Assert.That(diagnostics, Does.Not.Contain("error-secret"));
            Assert.That(diagnostics, Does.Not.Contain("url-secret"));
            Assert.That(safe.Url, Is.EqualTo("https://example.invalid/api/wt"));
        }

        [Test]
        [Timeout(8000)]
        public async Task WebTransportEstablishmentDeadlineFallsBackAfterFiveSeconds()
        {
            var pending = new ManualTransport(autoOpen: false);
            var resolved = new List<string>();
            var client = NewPlanClient(options =>
                options.Transport == GolemConnectOptions.TransportWebTransport
                    ? (IGolemTransport)pending
                    : new RecordingTransport());
            var started = DateTime.UtcNow;

            client.Connect(FallbackConfig(), (options, _) =>
            {
                resolved.Add(options.Transport);
                return Task.FromResult(options);
            });
            await WaitForAsync(
                () => client.ConnectedTransport == GolemConnectOptions.TransportWebSocket,
                TimeSpan.FromSeconds(7));

            Assert.That((DateTime.UtcNow - started).TotalSeconds, Is.GreaterThanOrEqualTo(4.8));
            Assert.That(pending.CloseCalls, Is.GreaterThan(0));
            Assert.That(resolved, Is.EqualTo(new[]
            {
                GolemConnectOptions.TransportWebTransport,
                GolemConnectOptions.TransportWebSocket,
            }));
        }

        private static GameClient NewPlanClient(
            Func<GolemConnectOptions, IGolemTransport> factory,
            Func<GolemConnectOptions, bool> supported = null)
        {
            return new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                _ => Array.Empty<byte>(),
                _ => Array.Empty<byte>(),
                factory,
                transportSupported: supported ?? (_ => true));
        }

        private static GolemRealtimeConfig FallbackConfig()
        {
            return new GolemRealtimeConfig(
                GolemConnectOptions.TransportWebTransport,
                "https://example.invalid/api/wt",
                fallback: new GolemRealtimeEndpoint(
                    GolemConnectOptions.TransportWebSocket,
                    "wss://example.invalid/api/ws"));
        }

        private static GolemConnectOptions WithTicket(GolemConnectOptions options, int ticket)
        {
            return new GolemConnectOptions(
                options.Url + "?ticket=" + ticket,
                options.Transport,
                options.ServerCertificateHashes,
                options.EventualAckIntervalMs);
        }

        private static async Task WaitForAsync(
            Func<bool> predicate,
            TimeSpan? timeout = null)
        {
            var deadline = DateTime.UtcNow.Add(timeout ?? TimeSpan.FromSeconds(1));
            while (!predicate() && DateTime.UtcNow < deadline)
            {
                await Task.Delay(10);
            }
            Assert.That(predicate(), Is.True);
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

        private sealed class PreOpenFailureTransport : IGolemTransport
        {
            private readonly string _transport;
            private readonly GolemDisconnectInfo? _info;

            public PreOpenFailureTransport(string transport)
            {
                _transport = transport;
            }

            public PreOpenFailureTransport(string transport, GolemDisconnectInfo info)
            {
                _transport = transport;
                _info = info;
            }

            public bool Connected => false;
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
                DisconnectedEvent?.Invoke(_info ?? new GolemDisconnectInfo(
                        false,
                        reason: "pre_open_failed",
                        transport: _transport,
                        url: url,
                        phase: GolemDisconnectPhase.Connect,
                        category: GolemDisconnectCategory.ConnectionRefused));
            }

            public void Send(byte[] bytes) { }
            public void SendUnreliable(byte[] bytes) { }
            public void SendReliableUnordered(byte[] bytes) { }
            public void SendReliableOrdered(byte[] bytes) { }
            public void Close() { }
        }

        private sealed class ManualTransport : IGolemTransport
        {
            private readonly bool _autoOpen;
            private bool _connected;

            public ManualTransport(bool autoOpen)
            {
                _autoOpen = autoOpen;
            }

            public bool Connected => _connected;
            public int ConnectCalls { get; private set; }
            public int CloseCalls { get; private set; }
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
                ConnectCalls++;
                if (_autoOpen)
                {
                    Open();
                }
            }

            public void Open()
            {
                _connected = true;
                ConnectedEvent?.Invoke();
            }

            public void Fail(GolemDisconnectInfo info)
            {
                _connected = false;
                try
                {
                    DisconnectedEvent?.Invoke(info);
                }
                catch (InvalidOperationException ex) when (
                    ex.Message.IndexOf("DontDestroyOnLoad", StringComparison.Ordinal) >= 0)
                {
                    // EditMode cannot create the runtime callback dispatcher; connection
                    // state is updated before dispatch, which is what this test observes.
                }
            }

            public void Send(byte[] bytes) { }
            public void SendUnreliable(byte[] bytes) { }
            public void SendReliableUnordered(byte[] bytes) { }
            public void SendReliableOrdered(byte[] bytes) { }

            public void Close()
            {
                CloseCalls++;
                _connected = false;
            }
        }
    }
}
