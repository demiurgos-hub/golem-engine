using System;
using System.Net.Sockets;
using System.Net.WebSockets;
using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed partial class GameClientTests
    {
        [TestCase(false, 24)]
        [TestCase(true, 26)]
        public void CommandsUseTheCurrentTransportBudget(bool ordered, int header)
        {
            var transport = new RecordingTransport(1500);
            var client = new GameClient(new RecordingEntityManager(), b => b, c => (byte[])c, ClientPacket, _ => transport);
            client.Connect("custom-endpoint");
            Action<byte[]> send = bytes => { if (ordered) client.SendOrdered(bytes); else client.Send(bytes); };
            send(new byte[1500 - header]);
            transport.MaxDatagramBytes = header + 3;
            send(new byte[3]);
            Assert.That(Assert.Throws<InvalidOperationException>(() => send(new byte[4])).Message,
                Does.EndWith("command size 4 exceeds max payload 3"));
            transport.MaxDatagramBytes = header - 1;
            Assert.That(Assert.Throws<InvalidOperationException>(() => send(Array.Empty<byte>())).Message,
                Does.EndWith("command size 0 exceeds max payload -1"));
            Assert.That(transport.Sent, Is.Empty);
            Assert.That(ordered ? transport.ReliableOrderedSent.Count : transport.ReliableUnorderedSent.Count, Is.EqualTo(2));
            transport.MaxDatagramBytes = 0;
            send(new byte[] { 1 });
            transport.MaxDatagramBytes = -1;
            send(new byte[] { 2 });
            Flush(client);
            Assert.That(transport.Sent[0], Is.EqualTo(ClientPacket(new[] { new byte[] { 1 }, new byte[] { 2 } })));
        }

        [Test]
        public void SharedDisconnectFactoryPreservesMetadataAndClassification()
        {
            var cases = new (Exception error, GolemDisconnectCategory category)[]
            {
                (null, GolemDisconnectCategory.Unknown),
                (new UriFormatException(), GolemDisconnectCategory.InvalidUrl),
                (new OperationCanceledException(), GolemDisconnectCategory.Timeout),
                (new TimeoutException(), GolemDisconnectCategory.Timeout),
                (new SocketException((int)SocketError.ConnectionRefused), GolemDisconnectCategory.ConnectionRefused),
                (new WebSocketException("backend"), GolemDisconnectCategory.TransportBackend),
                (new Exception("TLS certificate failed"), GolemDisconnectCategory.Tls),
                (new Exception("webtransport backend failed"), GolemDisconnectCategory.TransportBackend),
            };
            foreach (var transport in new[] { "websocket", "webtransport" })
            foreach (var item in cases)
            foreach (var clean in new[] { false, true })
            {
                var info = GolemUnityLog.CreateDisconnectInfo(clean, GolemDisconnectPhase.OpenStream,
                    item.error, transport, "https://example.com/wt?token=secret");
                Assert.That(info.WasClean, Is.EqualTo(clean));
                Assert.That(info.Error, Is.SameAs(item.error));
                Assert.That(info.Transport, Is.EqualTo(transport));
                Assert.That(info.Url, Is.EqualTo("https://example.com/wt?token=secret"));
                Assert.That(info.Phase, Is.EqualTo(GolemDisconnectPhase.OpenStream));
                Assert.That(info.Code, Is.Null);
                Assert.That(info.Reason, Is.Null);
                Assert.That(info.Category, Is.EqualTo(clean ? GolemDisconnectCategory.UserClosed : item.category));
            }
        }
    }
}
