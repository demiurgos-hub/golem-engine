using System.Collections.Generic;
using NUnit.Framework;
using System;

namespace GolemEngine.Unity.Tests
{
    public sealed class GameClientTests
    {
        [Test]
        public void DispatchesServerMessageToEntityManager()
        {
            var manager = new RecordingEntityManager();
            var client = new GameClient(
                manager,
                bytes => bytes,
                _ => Array.Empty<byte>(),
                _ => Array.Empty<byte>(),
                _ => new RecordingTransport());

            var payload = new byte[] { 9 };
            var message = new PbWriter().Tag(1, 2).Bytes(payload).Finish();
            typeof(GameClient)
                .GetMethod("HandleMessage", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Instance)
                ?.Invoke(client, new object[] { message });

            Assert.That(manager.LastUpdate, Is.EqualTo(payload));
        }

        [Test]
        public void DispatchesCompactStateBatchToEntityManager()
        {
            var manager = new RecordingCompactEntityManager();
            var client = new GameClient(
                manager,
                bytes => bytes,
                _ => Array.Empty<byte>(),
                _ => Array.Empty<byte>(),
                _ => new RecordingTransport());

            var frame = new byte[] { 1, 2, 3 };
            var batch = GolemDatagramProtocol.EncodeLengthPrefixedFrame(frame);
            typeof(GameClient)
                .GetMethod("HandleCompactStateBatch", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Instance)
                ?.Invoke(client, new object[] { batch });

            Assert.That(manager.LastCompactUpdate, Is.EqualTo(frame));
        }

        [Test]
        public void CompactStateBatchRequiresCompactEntityManager()
        {
            var client = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                _ => Array.Empty<byte>(),
                _ => Array.Empty<byte>(),
                _ => new RecordingTransport());

            var error = Assert.Throws<System.Reflection.TargetInvocationException>(() =>
                typeof(GameClient)
                    .GetMethod("HandleCompactStateBatch", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Instance)
                    ?.Invoke(client, new object[] { Array.Empty<byte>() }));
            Assert.That(error.InnerException, Is.TypeOf<InvalidOperationException>());
        }

        [Test]
        public void MapsCommandsToReliableDatagramLanesWithoutClientPacketWrapper()
        {
            var transport = new RecordingTransport(GolemDatagramProtocol.MaxWebTransportDatagramBytes);
            var client = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                cmd => (byte[])cmd,
                frames => ClientPacket(frames),
                _ => transport);

            client.Connect("ws://example.invalid/ws");
            var command = new byte[] { 9, 8, 7 };

            client.Send(command);
            client.SendOrdered(command);

            Assert.That(transport.ReliableUnorderedSent[0], Is.EqualTo(command));
            Assert.That(transport.ReliableOrderedSent[0], Is.EqualTo(command));
            Assert.That(transport.Sent, Is.Empty);
        }

        [Test]
        public void ExposesOnlyUnifiedCommandSendingMethods()
        {
            Assert.That(typeof(GameClient).GetMethod("Send", new[] { typeof(object) }), Is.Not.Null);
            Assert.That(typeof(GameClient).GetMethod("SendOrdered", new[] { typeof(object) }), Is.Not.Null);
            Assert.That(typeof(GameClient).GetMethod("SendUnreliable"), Is.Null);
            Assert.That(typeof(GameClient).GetMethod("SendReliableUnordered"), Is.Null);
            Assert.That(typeof(GameClient).GetMethod("SendReliableOrdered"), Is.Null);
        }

        [Test]
        public void BatchesWebSocketCommandsInCallOrderOnSharedReliableStream()
        {
            var transport = new RecordingTransport();
            var client = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                cmd => (byte[])cmd,
                frames => ClientPacket(frames),
                _ => transport);

            client.Connect("ws://example.invalid/ws");
            var first = new byte[] { 1 };
            var second = new byte[] { 2 };

            client.Send(first);
            client.SendOrdered(second);
            Flush(client);

            Assert.That(transport.Sent, Has.Count.EqualTo(1));
            Assert.That(transport.Sent[0], Is.EqualTo(ClientPacket(new[] { first, second })));
            Assert.That(transport.ReliableUnorderedSent, Is.Empty);
            Assert.That(transport.ReliableOrderedSent, Is.Empty);
        }

        [Test]
        public void RejectsOversizedDatagramCommandsWithoutStreamFallback()
        {
            var unorderedTransport = new RecordingTransport(
                GolemDatagramProtocol.PacketHeaderBytes +
                GolemDatagramProtocol.LaneHeaderBytes +
                GolemDatagramProtocol.ReliableMessageIdBytes +
                3);
            var unorderedClient = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                cmd => (byte[])cmd,
                frames => ClientPacket(frames),
                _ => unorderedTransport);
            unorderedClient.Connect("https://example.invalid/wt");

            var command = new byte[4];
            var unorderedError = Assert.Throws<InvalidOperationException>(() => unorderedClient.Send(command));
            Assert.That(unorderedError.Message, Does.Contain("reliable unordered command size 4 exceeds max payload 3"));
            Assert.That(unorderedTransport.Sent, Is.Empty);
            Assert.That(unorderedTransport.ReliableUnorderedSent, Is.Empty);

            var orderedTransport = new RecordingTransport(
                GolemDatagramProtocol.PacketHeaderBytes +
                GolemDatagramProtocol.LaneHeaderBytes +
                GolemDatagramProtocol.ReliableMessageIdBytes +
                GolemDatagramProtocol.ReliableOrderedSequenceBytes +
                3);
            var orderedClient = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                cmd => (byte[])cmd,
                frames => ClientPacket(frames),
                _ => orderedTransport);
            orderedClient.Connect("https://example.invalid/wt");

            var orderedError = Assert.Throws<InvalidOperationException>(() => orderedClient.SendOrdered(command));
            Assert.That(orderedError.Message, Does.Contain("reliable ordered command size 4 exceeds max payload 3"));
            Assert.That(orderedTransport.Sent, Is.Empty);
            Assert.That(orderedTransport.ReliableOrderedSent, Is.Empty);
        }

        [Test]
        public void IgnoresCommandsWhileDisconnected()
        {
            var encodeCalls = 0;
            var client = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                cmd =>
                {
                    encodeCalls++;
                    return (byte[])cmd;
                },
                frames => ClientPacket(frames),
                _ => new RecordingTransport());

            client.Send(new byte[] { 1 });
            client.SendOrdered(new byte[] { 2 });

            Assert.That(encodeCalls, Is.Zero);
        }

        [Test]
        public void SplitsQueuedCommandsAtReliableCap()
        {
            var transport = new RecordingTransport();
            var client = new GameClient(
                new RecordingEntityManager(),
                bytes => bytes,
                cmd => (byte[])cmd,
                frames => ClientPacket(frames),
                _ => transport);

            client.Connect("ws://example.invalid/ws");

            client.Send(new byte[GameClient.MaxReliableMessageBytes - 4]);
            client.Send(new byte[1]);

            Flush(client);

            Assert.That(transport.Sent.Count, Is.EqualTo(2));
        }

        [Test]
        public void DisconnectInfoKeepsDiagnosticMetadata()
        {
            var error = new InvalidOperationException("native backend failed");
            var info = new GolemDisconnectInfo(
                false,
                error: error,
                transport: "webtransport",
                url: "https://localhost:4433/wt?token=secret",
                phase: GolemDisconnectPhase.OpenStream,
                category: GolemDisconnectCategory.TransportBackend);

            Assert.That(info.WasClean, Is.False);
            Assert.That(info.Error, Is.SameAs(error));
            Assert.That(info.Transport, Is.EqualTo("webtransport"));
            Assert.That(info.Url, Is.EqualTo("https://localhost:4433/wt?token=secret"));
            Assert.That(info.Phase, Is.EqualTo(GolemDisconnectPhase.OpenStream));
            Assert.That(info.Category, Is.EqualTo(GolemDisconnectCategory.TransportBackend));
            Assert.That(GolemUnityLog.RedactUrl(info.Url), Is.EqualTo("https://localhost:4433/wt"));
        }

        [Test]
        public void LogFormatterIncludesExceptionTypeAndInnerError()
        {
            var inner = new TimeoutException("dial timed out");
            var error = new InvalidOperationException("native backend failed", inner);

            var formatted = GolemUnityLog.FormatError(error);

            Assert.That(formatted, Does.Contain("error_type=InvalidOperationException"));
            Assert.That(formatted, Does.Contain("error=\"native backend failed\""));
            Assert.That(formatted, Does.Contain("inner_error_type=TimeoutException"));
            Assert.That(formatted, Does.Contain("inner_error=\"dial timed out\""));
        }

        private static byte[] ClientPacket(IReadOnlyList<byte[]> frames)
        {
            var w = new PbWriter();
            foreach (var frame in frames)
            {
                w.Tag(1, 2).Bytes(frame);
            }
            return w.Finish();
        }

        private static void Flush(GameClient client)
        {
            typeof(GameClient)
                .GetMethod("Flush", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Instance)
                ?.Invoke(client, Array.Empty<object>());
        }

        private sealed class RecordingEntityManager : IEntityManager
        {
            public object LastUpdate { get; private set; }
            public void ApplyUpdate(object update) { LastUpdate = update; }
            public object Get(long entityId) { return null; }
        }

        private sealed class RecordingCompactEntityManager : IEntityManager, ICompactEntityManager
        {
            public object LastUpdate { get; private set; }
            public byte[] LastCompactUpdate { get; private set; }
            public void ApplyUpdate(object update) { LastUpdate = update; }
            public void ApplyCompactUpdate(byte[] frame) { LastCompactUpdate = frame; }
            public object Get(long entityId) { return null; }
        }

        private sealed class RecordingTransport : IGolemTransport
        {
            private readonly int _maxDatagramBytes;

            public RecordingTransport(int maxDatagramBytes = 0)
            {
                _maxDatagramBytes = maxDatagramBytes;
            }

            public bool Connected => true;
            public int MaxMessageBytes => GameClient.MaxReliableMessageBytes;
            public int MaxDatagramBytes => _maxDatagramBytes;
            public List<byte[]> Sent { get; } = new List<byte[]>();
            public List<byte[]> UnreliableSent { get; } = new List<byte[]>();
            public List<byte[]> ReliableUnorderedSent { get; } = new List<byte[]>();
            public List<byte[]> ReliableOrderedSent { get; } = new List<byte[]>();
            public event Action ConnectedEvent;
            public event Action<byte[]> MessageEvent;
            public event Action<byte[]> UnreliableStateMessageEvent;
            public event Action<byte[]> ReliableOrderedMessageEvent;
            public event Action<byte[]> EventualStateMessageEvent;
            public event Action<GolemDisconnectInfo> DisconnectedEvent;
            public void Connect(string url) { ConnectedEvent?.Invoke(); }
            public void Send(byte[] bytes) { Sent.Add(bytes); }
            public void SendUnreliable(byte[] bytes) { UnreliableSent.Add(bytes); }
            public void SendReliableUnordered(byte[] bytes) { ReliableUnorderedSent.Add(bytes); }
            public void SendReliableOrdered(byte[] bytes) { ReliableOrderedSent.Add(bytes); }
            public void Close() { DisconnectedEvent?.Invoke(new GolemDisconnectInfo(true)); }
        }
    }
}
