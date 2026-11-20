using System.Collections.Generic;
using NUnit.Framework;
using System;

namespace GolemEngine.Unity.Tests
{
    public sealed partial class GameClientTests
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
        public void DispatchesDatagramEventRecordsToEventManager()
        {
            var manager = new RecordingCompactEntityManager();
            var events = new RecordingEventManager();
            var client = new GameClient(
                manager,
                bytes => bytes,
                _ => Array.Empty<byte>(),
                _ => Array.Empty<byte>(),
                _ => new RecordingTransport(),
                eventManager: events);

            var frame = new byte[] { 1, 2, 3 };
            var record = new byte[] { GolemDatagramProtocol.EventRecordTag, 7, 8 };
            var batch = new List<byte>(GolemDatagramProtocol.EncodeLengthPrefixedFrame(record));
            batch.AddRange(GolemDatagramProtocol.EncodeLengthPrefixedFrame(frame));
            typeof(GameClient)
                .GetMethod("HandleCompactStateBatch", System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Instance)
                ?.Invoke(client, new object[] { batch.ToArray() });

            Assert.That(events.LastEvent, Is.EqualTo(new byte[] { 7, 8 }));
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

        [Test]
        public void SpaceMessagesUseServerGoldenBytes()
        {
            Assert.That(SpaceChangedField(5, 1), Is.EqualTo(new byte[] { 0x22, 0x04, 0x08, 0x05, 0x10, 0x01 }));
            Assert.That(SpaceSettledField(1), Is.EqualTo(new byte[] { 0x2a, 0x02, 0x08, 0x01 }));
            Assert.That(
                GameClient.EncodeSpaceAck(0x0102030405060708UL),
                Is.EqualTo(new byte[] { 0x00, 0x4f, 0x47, 0x53, 0x03, 1, 2, 3, 4, 5, 6, 7, 8 }));
        }

        [Test]
        public void RaisesSpaceChangedBeforeFollowingFramesAndAcknowledgesSettle()
        {
            var manager = new FencedEntityManager();
            var transport = new RecordingTransport(GolemDatagramProtocol.MaxWebTransportDatagramBytes);
            var client = SpaceClient(manager, transport);
            var changes = new List<(uint, uint)>();
            client.SpaceChanged += (oldSpace, newSpace) =>
            {
                Assert.That(client.Space, Is.EqualTo(newSpace));
                manager.Log.Add($"space {oldSpace}->{newSpace}");
                changes.Add((oldSpace, newSpace));
            };
            client.Connect("https://example.invalid/wt");

            InvokePrivate(client, "HandleMessage", Concat(
                EntityField(1, 3, "exit"),
                SpaceChangedField(5, 1),
                EntityField(2, 4, "carried"),
                EntityField(3, 1, "entered"),
                SpaceSettledField(1)));

            Assert.That(manager.Log, Is.EqualTo(new[] { "full 1@3=exit", "space 0->5", "full 2@4=carried", "full 3@1=entered" }));
            Assert.That(client.Space, Is.EqualTo(5u));
            Assert.That(transport.Sent, Has.Count.EqualTo(1));
            Assert.That(transport.Sent[0], Is.EqualTo(GameClient.EncodeSpaceAck(1)));

            // Returning to space 0 omits the space field.
            InvokePrivate(client, "HandleMessage", Concat(SpaceChangedField(0, 2), SpaceSettledField(2)));
            Assert.That(client.Space, Is.EqualTo(0u));
            Assert.That(changes, Is.EqualTo(new[] { (0u, 5u), (5u, 0u) }));
            Assert.That(transport.Sent[1], Is.EqualTo(GameClient.EncodeSpaceAck(2)));
        }

        [Test]
        public void HoldsDatagramBatchesUntilSpaceSettledAndLetsRevisionFenceRejectLateState()
        {
            var manager = new FencedEntityManager();
            var transport = new RecordingTransport(GolemDatagramProtocol.MaxWebTransportDatagramBytes);
            var client = SpaceClient(manager, transport);
            client.Connect("https://example.invalid/wt");

            InvokePrivate(client, "HandleMessage", EntityField(1, 8, "old"));
            InvokePrivate(client, "HandleMessage", SpaceChangedField(7, 3));
            InvokePrivate(client, "ReceiveCompactStateBatch", CompactBatch(1, 9, "late"));
            InvokePrivate(client, "ReceiveCompactStateBatch", CompactBatch(1, 10, "carried"));
            InvokePrivate(client, "ReceiveCompactStateBatch", CompactBatch(1, 11, "next"));
            Assert.That(manager.Log, Is.EqualTo(new[] { "full 1@8=old" }));
            Assert.That(transport.Sent, Is.Empty);

            InvokePrivate(client, "HandleMessage", Concat(EntityField(1, 10, "carried"), SpaceSettledField(3)));
            Assert.That(manager.Log, Is.EqualTo(new[]
            {
                "full 1@8=old",
                "full 1@10=carried",
                "reject compact 1@9",
                "compact 1@10=carried",
                "compact 1@11=next",
            }));
            Assert.That(transport.Sent, Has.Count.EqualTo(1));
            Assert.That(transport.Sent[0], Is.EqualTo(GameClient.EncodeSpaceAck(3)));

            InvokePrivate(client, "ReceiveCompactStateBatch", CompactBatch(1, 12, "live"));
            Assert.That(manager.Log[manager.Log.Count - 1], Is.EqualTo("compact 1@12=live"));
        }

        [Test]
        public void NewerSpaceChangedSupersedesOpenTransition()
        {
            var manager = new FencedEntityManager();
            var transport = new RecordingTransport(GolemDatagramProtocol.MaxWebTransportDatagramBytes);
            var client = SpaceClient(manager, transport);
            client.Connect("https://example.invalid/wt");

            InvokePrivate(client, "HandleMessage", SpaceChangedField(1, 1));
            InvokePrivate(client, "ReceiveCompactStateBatch", CompactBatch(1, 1, "held"));
            InvokePrivate(client, "HandleMessage", Concat(SpaceChangedField(2, 2), SpaceSettledField(1)));
            Assert.That(manager.Log, Is.Empty);
            Assert.That(transport.Sent, Is.Empty);

            InvokePrivate(client, "HandleMessage", SpaceSettledField(2));
            Assert.That(manager.Log, Is.EqualTo(new[] { "compact 1@1=held" }));
            Assert.That(transport.Sent, Has.Count.EqualTo(1));
            Assert.That(transport.Sent[0], Is.EqualTo(GameClient.EncodeSpaceAck(2)));
            Assert.That(client.Space, Is.EqualTo(2u));
        }

        [Test]
        public void SpaceHoldBackOverflowClosesConnectionWithError()
        {
            var manager = new FencedEntityManager();
            var transport = new RecordingTransport(GolemDatagramProtocol.MaxWebTransportDatagramBytes);
            var client = SpaceClient(manager, transport);
            var disconnects = new List<GolemDisconnectInfo>();
            client.DisconnectedEvent += info => disconnects.Add(info);
            client.Connect("https://example.invalid/wt");

            InvokePrivate(client, "HandleMessage", SpaceChangedField(4, 1));
#if UNITY_5_3_OR_NEWER
            // The .NET policy consumer stubs Unity's logger; the Unity runner fails on unexpected errors.
            UnityEngine.TestTools.LogAssert.Expect(
                UnityEngine.LogType.Error,
                new System.Text.RegularExpressions.Regex("space_transition_overflow"));
#endif
            for (var i = 0; i <= GameClient.MaxHeldSpaceTransitionBatches; i++)
            {
                InvokePrivate(client, "ReceiveCompactStateBatch", CompactBatch(1, 1, "x"));
            }

            Assert.That(disconnects, Has.Count.EqualTo(1));
            Assert.That(disconnects[0].WasClean, Is.False);
            Assert.That(disconnects[0].Reason, Is.EqualTo("space_transition_overflow"));
            Assert.That(disconnects[0].Category, Is.EqualTo(GolemDisconnectCategory.Protocol));
            Assert.That(disconnects[0].Error, Is.Not.Null);
            Assert.That(client.Connected, Is.False);
            Assert.That(client.Space, Is.EqualTo(0u));
            Assert.That(manager.Log, Is.Empty);

            client.Disconnect();
            Assert.That(disconnects, Has.Count.EqualTo(1));
        }

        [Test]
        public void SpaceResetsSilentlyOnDisconnectAndReconnect()
        {
            var manager = new FencedEntityManager();
            var client = SpaceClient(manager, new RecordingTransport(GolemDatagramProtocol.MaxWebTransportDatagramBytes));
            var calls = 0;
            client.SpaceChanged += (_, _) => calls++;
            client.Connect("https://example.invalid/wt");

            InvokePrivate(client, "HandleMessage", SpaceChangedField(3, 1));
            InvokePrivate(client, "ReceiveCompactStateBatch", CompactBatch(1, 1, "held"));
            Assert.That(client.Space, Is.EqualTo(3u));

            client.Disconnect();
            Assert.That(client.Space, Is.EqualTo(0u));
            client.Connect("https://example.invalid/wt");
            Assert.That(client.Space, Is.EqualTo(0u));
            Assert.That(calls, Is.EqualTo(1));

            // The previous connection's open transition and held batch are gone.
            InvokePrivate(client, "ReceiveCompactStateBatch", CompactBatch(1, 2, "fresh"));
            Assert.That(manager.Log, Is.EqualTo(new[] { "compact 1@2=fresh" }));
        }

        [Test]
        public void MessagesWithoutSpaceFieldsBehaveAsBefore()
        {
            var manager = new FencedEntityManager();
            var transport = new RecordingTransport(GolemDatagramProtocol.MaxWebTransportDatagramBytes);
            var client = SpaceClient(manager, transport);
            var calls = 0;
            client.SpaceChanged += (_, _) => calls++;
            client.Connect("https://example.invalid/wt");

            InvokePrivate(client, "HandleMessage", Concat(
                EntityField(1, 1, "a"),
                new PbWriter().Tag(9, 2).Bytes(new byte[] { 7 }).Finish(),
                EntityField(2, 1, "b")));
            InvokePrivate(client, "ReceiveCompactStateBatch", CompactBatch(1, 2, "c"));

            Assert.That(manager.Log, Is.EqualTo(new[] { "full 1@1=a", "full 2@1=b", "compact 1@2=c" }));
            Assert.That(client.Space, Is.EqualTo(0u));
            Assert.That(calls, Is.Zero);
            Assert.That(transport.Sent, Is.Empty);
        }

        private static GameClient SpaceClient(FencedEntityManager manager, RecordingTransport transport)
        {
            return new GameClient(
                manager,
                bytes => bytes,
                cmd => (byte[])cmd,
                frames => ClientPacket(frames),
                _ => transport);
        }

        private static void InvokePrivate(GameClient client, string name, byte[] argument)
        {
            var method = typeof(GameClient).GetMethod(name, System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Instance);
            Assert.That(method, Is.Not.Null, name);
            method.Invoke(client, new object[] { argument });
        }

        private static byte[] FencedRecord(byte id, byte revision, string value)
        {
            var record = new List<byte> { id, revision };
            record.AddRange(System.Text.Encoding.ASCII.GetBytes(value));
            return record.ToArray();
        }

        private static byte[] EntityField(byte id, byte revision, string value)
        {
            return new PbWriter().Tag(1, 2).Bytes(FencedRecord(id, revision, value)).Finish();
        }

        private static byte[] CompactBatch(byte id, byte revision, string value)
        {
            return GolemDatagramProtocol.EncodeLengthPrefixedFrame(FencedRecord(id, revision, value));
        }

        private static byte[] SpaceChangedField(uint space, ulong transition)
        {
            var inner = new PbWriter();
            if (space != 0) inner.Tag(1, 0).Uint32(space);
            if (transition != 0) inner.Tag(2, 0).Uint64(transition);
            return new PbWriter().Tag(4, 2).Bytes(inner.Finish()).Finish();
        }

        private static byte[] SpaceSettledField(ulong transition)
        {
            var inner = new PbWriter();
            if (transition != 0) inner.Tag(1, 0).Uint64(transition);
            return new PbWriter().Tag(5, 2).Bytes(inner.Finish()).Finish();
        }

        private static byte[] Concat(params byte[][] parts)
        {
            var output = new List<byte>();
            foreach (var part in parts)
            {
                output.AddRange(part);
            }
            return output.ToArray();
        }

        /// <summary>Mimics the generated managers' revision fence: older revisions are rejected, equal ones apply.</summary>
        private sealed class FencedEntityManager : IEntityManager, ICompactEntityManager
        {
            private readonly Dictionary<int, int> _last = new Dictionary<int, int>();
            public List<string> Log { get; } = new List<string>();
            public void ApplyUpdate(object update) { Apply("full", (byte[])update); }
            public void ApplyCompactUpdate(byte[] frame) { Apply("compact", frame); }
            public object Get(long entityId) { return null; }

            private void Apply(string kind, byte[] frame)
            {
                int id = frame[0];
                int revision = frame[1];
                var value = System.Text.Encoding.ASCII.GetString(frame, 2, frame.Length - 2);
                if (_last.TryGetValue(id, out var last) && revision < last)
                {
                    Log.Add($"reject {kind} {id}@{revision}");
                    return;
                }
                _last[id] = revision;
                Log.Add($"{kind} {id}@{revision}={value}");
            }
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

        private sealed class RecordingEventManager : IEventManager
        {
            public byte[] LastEvent { get; private set; }
            public void ApplyRaw(byte[] bytes) { LastEvent = bytes; }
        }

        private sealed class RecordingTransport : IGolemTransport
        {
            public RecordingTransport(int maxDatagramBytes = 0)
            {
                MaxDatagramBytes = maxDatagramBytes;
            }

            public bool Connected => true;
            public int MaxMessageBytes => GameClient.MaxReliableMessageBytes;
            public int MaxDatagramBytes { get; set; }
            public List<byte[]> Sent { get; } = new List<byte[]>();
            public List<byte[]> UnreliableSent { get; } = new List<byte[]>();
            public List<byte[]> ReliableUnorderedSent { get; } = new List<byte[]>();
            public List<byte[]> ReliableOrderedSent { get; } = new List<byte[]>();
            public event Action ConnectedEvent;
            public event Action<byte[]> MessageEvent;
            public event Action<byte[]> UnreliableStateMessageEvent;
            public event Action<byte[]> ReliableUnorderedMessageEvent;
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
