using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed partial class GolemRealtimeBootstrapTests
    {
        [TestCase("websocket", "ws://example.com/ws")]
        [TestCase("websocket", "WSS://example.com:443/ws?q=a%2Bb+#frag")]
        [TestCase("webtransport", "HTTPS://example.com/wt")]
        public void EndpointValidationKeepsAcceptedUrlBytes(string transport, string url)
        {
            var config = GolemRealtimeBootstrap.ParseRealtimeConfig(
                $"{{\"transport\":\"{transport}\",\"url\":\"{url}\"}}");
            Assert.That(config.Url, Is.EqualTo(url));
            var connection = new ManualTransport(true);
            var client = NewPlanClient(_ => connection);
            Assert.DoesNotThrow(() => client.Connect(config, null));
            Assert.That(connection.ConnectCalls, Is.EqualTo(1));
            client.Disconnect();
        }

        [TestCase("websocket", "/relative?token=secret", false)]
        [TestCase("websocket", "ws://[bad", false)]
        [TestCase("websocket", "wss://user:secret@example.com/ws", false)]
        [TestCase("websocket", "https://user:secret@example.com/ws", false)]
        [TestCase("websocket", "https://example.com/ws?token=secret", true)]
        [TestCase("webtransport", "http://example.com/wt", true)]
        [TestCase("webtransport", "wss://example.com/wt", true)]
        public void EndpointErrorsKeepCallerContext(string transport, string url, bool scheme)
        {
            var parseError = Assert.Throws<InvalidOperationException>(() =>
                GolemRealtimeBootstrap.ParseRealtimeConfig($"{{\"transport\":\"{transport}\",\"url\":\"{url}\"}}"));
            Assert.That(parseError.Message, Is.EqualTo(scheme
                ? $"golem-unity: realtime primary URL has an invalid scheme for {transport}"
                : "golem-unity: realtime primary URL must be absolute"));
            var client = NewPlanClient(_ => throw new Exception("must not dial"));
            var error = Assert.Throws<ArgumentException>(() =>
                client.Connect(new GolemRealtimeConfig(transport, url), null));
            Assert.That(error.ParamName, Is.EqualTo("config"));
            Assert.That(error.Message, Does.StartWith(scheme
                ? $"golem-unity: invalid URL scheme for {transport} endpoint"
                : "golem-unity: realtime endpoint must be an absolute URL"));
            Assert.That(error.Message, Does.Not.Contain("secret"));
        }

        [TestCase("/relative", false)]
        [TestCase("wss://user:secret@example.com/ws", false)]
        [TestCase("https://example.com/ws", true)]
        public void FallbackEndpointErrorsKeepCallerContext(string url, bool scheme)
        {
            var error = Assert.Throws<InvalidOperationException>(() => GolemRealtimeBootstrap.ParseRealtimeConfig(
                "{\"transport\":\"webtransport\",\"url\":\"https://example.com/wt\",\"fallback\":" +
                $"{{\"transport\":\"websocket\",\"url\":\"{url}\"}}}}"));
            Assert.That(error.Message, Is.EqualTo(scheme
                ? "golem-unity: realtime fallback URL has an invalid scheme for websocket"
                : "golem-unity: realtime fallback URL must be absolute"));
            var client = NewPlanClient(_ => throw new Exception("must not dial"));
            var argument = Assert.Throws<ArgumentException>(() => client.Connect(
                new GolemRealtimeConfig("webtransport", "https://example.com/wt", fallback: new GolemRealtimeEndpoint("websocket", url)), null));
            Assert.That(argument.ParamName, Is.EqualTo("config"));
            Assert.That(argument.Message, Does.Not.Contain("secret"));
        }

        [Test]
        public void EndpointValidationDoesNotReplaceOuterValidationOrDirectConnect()
        {
            var client = NewPlanClient(_ => new ManualTransport(true));
            Assert.That(Assert.Throws<ArgumentException>(() => client.Connect(
                new GolemRealtimeConfig("unknown", "/relative"), null)).Message,
                Does.StartWith("golem-unity: unsupported primary transport"));
            Assert.That(Assert.Throws<ArgumentException>(() => client.Connect(
                new GolemRealtimeConfig("websocket", " "), null)).Message,
                Does.StartWith("golem-unity: primary url is required"));
            Assert.DoesNotThrow(() => client.Connect(new GolemConnectOptions("custom-endpoint", "custom")));
            client.Disconnect();
        }

        [TestCase(false)]
        [TestCase(true)]
        public void BothOptionsTypesCopyArraysAndListsIndependently(bool array)
        {
            var first = new GolemCertificateHash("SHA-256", "unchanged");
            IList<GolemCertificateHash> source = array
                ? (IList<GolemCertificateHash>)new[] { first, default(GolemCertificateHash) }
                : new List<GolemCertificateHash> { first, default };
            var hashes = (IReadOnlyList<GolemCertificateHash>)source;
            var config = new GolemRealtimeConfig("webtransport", "https://example.com/wt", hashes);
            var options = new GolemConnectOptions(config.Url, config.Transport, hashes);
            var converted = GolemRealtimeBootstrap.ConnectOptionsFromRealtimeConfig(config);
            source[0] = new GolemCertificateHash("changed", "changed");
            foreach (var copy in new[] { config.ServerCertificateHashes, options.ServerCertificateHashes, converted.ServerCertificateHashes })
            {
                Assert.That(copy, Is.TypeOf<GolemCertificateHash[]>());
                Assert.That(copy, Is.Not.SameAs(source));
                Assert.That(copy.Count, Is.EqualTo(2));
                Assert.That(copy[0].Algorithm, Is.EqualTo("SHA-256"));
                Assert.That(copy[0].Value, Is.EqualTo("unchanged"));
                Assert.That(copy[1].Algorithm, Is.Null);
            }
            ((GolemCertificateHash[])config.ServerCertificateHashes)[0] = source[0];
            Assert.That(options.ServerCertificateHashes[0].Value, Is.EqualTo("unchanged"));
            Assert.That(converted.ServerCertificateHashes[0].Value, Is.EqualTo("unchanged"));
            Assert.That(new GolemConnectOptions("x").ServerCertificateHashes, Is.Empty);
            Assert.That(new GolemRealtimeConfig("x", "x", Array.Empty<GolemCertificateHash>()).ServerCertificateHashes, Is.Empty);
        }

        [TestCase("401", true)]
        [TestCase("HTTP/2 403", true)]
        [TestCase("HTTP/1.1 426", true)]
        [TestCase("status code '401'", true)]
        [TestCase("status code \"403\"", true)]
        [TestCase("STATUS 426", true)]
        [TestCase("request failed (403)", true)]
        [TestCase("UPGRADE REQUIRED", true)]
        [TestCase("ticket expired", true)]
        [TestCase("ticket consumed", true)]
        [TestCase("ticket rejected", true)]
        [TestCase("backend 401 packets", false)]
        [TestCase("outer 401", false)]
        [TestCase("ticket requested", false)]
        [TestCase("HTTP 503", false)]
        public async Task HandshakeTextIsSharedAcrossThrownAndReportedFailures(string text, bool terminal)
        {
            foreach (var source in new[] { "reason", "exception", "thrown" })
            {
                var attempts = new List<string>();
                var nested = new InvalidOperationException(text == "401" ? string.Empty : "outer", new Exception(text));
                var client = NewPlanClient(options =>
                {
                    attempts.Add(options.Transport);
                    if (options.Transport == "websocket") return new ManualTransport(true);
                    return source == "thrown"
                        ? (IGolemTransport)new ManualTransport(false) { ConnectError = nested }
                        : new PreOpenFailureTransport("webtransport", source == "reason"
                            ? new GolemDisconnectInfo(false, reason: text)
                            : new GolemDisconnectInfo(false, error: nested));
                });
                client.Connect(FallbackConfig(), (options, _) => Task.FromResult(options));
                // Both fake transports complete synchronously; no timing-dependent assertion.
                Assert.That(attempts.Count, Is.EqualTo(terminal ? 1 : 2), text + " source=" + source);
                client.Disconnect();
            }
            await Task.CompletedTask;
        }
    }
}
