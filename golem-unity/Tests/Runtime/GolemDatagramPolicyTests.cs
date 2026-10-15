using System;
using System.Collections;
using System.Collections.Generic;
using System.Linq;
using System.Reflection;
using System.Threading;
using System.Threading.Tasks;
using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed partial class GolemDatagramProtocolTests
    {
        // Fixed wire vectors from internal/netwire/wire_test.go, not calculated by production helpers.
        [TestCase(1, 0, "1234abcd000000018000000001020304ffffffff0001dead")]
        [TestCase(2, 0, "1234abcd000000018000000001020304ffffffff00025678dead")]
        [TestCase(3, 0, "1234abcd000000018000000001020304ffffffff000356789abcdead")]
        [TestCase(4, 0, "1234abcd000000018000000001020304ffffffff00040123456789abcdefdead")]
        [TestCase(255, 1, "1234abcd000000018000000001020304ffffffff01")]
        public void PacketEncodingMatchesGoldenBytes(int lane, int flags, string hex)
        {
            var packetType = typeof(GolemDatagramProtocol).GetNestedType("DatagramPacket", BindingFlags.NonPublic);
            var packet = Activator.CreateInstance(packetType);
            Action<string, object> set = (name, value) => packetType.GetField(name).SetValue(packet, value);
            set("PacketSeq", (ushort)0x1234);
            set("AckSeq", (ushort)0xabcd);
            set("AckMask", new uint[] { 1, 0x80000000, 0x01020304, 0xffffffff });
            set("Flags", (byte)flags);
            set("Lane", (GolemDatagramLane)lane);
            set("MessageId", (ushort)0x5678);
            set("OrderedSeq", (ushort)0x9abc);
            set("StateToken", 0x0123456789abcdefUL);
            set("Payload", new byte[] { 0xde, 0xad });
            var encoded = (byte[])typeof(GolemDatagramProtocol).GetMethod("EncodePacket", BindingFlags.NonPublic | BindingFlags.Static)
                .Invoke(null, new[] { packet });
            Assert.That(encoded, Is.EqualTo(Hex(hex)));
            var deliveries = new List<byte[]>();
            new GolemDatagramProtocol(_ => { }).Receive(encoded, (_, payload) => deliveries.Add(payload));
            Assert.That(deliveries.Count, Is.EqualTo(flags == 1 ? 0 : 1));
            if (flags == 0) Assert.That(deliveries[0], Is.EqualTo(new byte[] { 0xde, 0xad }));
        }

        [TestCase(GolemDatagramLane.Unreliable, 22)]
        [TestCase(GolemDatagramLane.ReliableUnordered, 24)]
        [TestCase(GolemDatagramLane.ReliableOrdered, 26)]
        [TestCase(GolemDatagramLane.EventualState, 30)]
        public void PayloadBudgetsAndBuiltInCeiling(GolemDatagramLane lane, int header)
        {
            foreach (var limit in new[] { 1, header - 1, header, 1200, 1500 })
                Assert.That(GolemDatagramProtocol.GetPayloadBudget(limit, lane), Is.EqualTo(limit - header));
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add);
            protocol.Send(lane, new byte[1200 - header]);
            Assert.That(sent[0].Length, Is.EqualTo(1200));
            Assert.That(Assert.Throws<InvalidOperationException>(() => protocol.Send(lane, new byte[1201 - header])).Message,
                Is.EqualTo("golem-unity: datagram size 1201 exceeds max 1200"));
        }

        [Test]
        public void DueAckUsesCopiedMaskAndDuplicateReceiptDoesNotDirtyAgain()
        {
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add, 1000);
            protocol.Receive(EncodePacket(5, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            protocol.Receive(EncodePacket(3, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            protocol.SendDueAck(DateTime.MinValue);
            Assert.That(sent, Is.Empty);
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent[0], Is.EqualTo(Hex("000000050000000200000000000000000000000001")));
            protocol.Receive(EncodePacket(3, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent.Count, Is.EqualTo(1));
            protocol.Receive(EncodePacket(6, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent[0][7], Is.EqualTo(2));
            Assert.That(sent[1][7], Is.EqualTo(5));
        }

        [TestCase(GolemDatagramLane.Unreliable)]
        [TestCase(GolemDatagramLane.ReliableUnordered)]
        [TestCase(GolemDatagramLane.ReliableOrdered)]
        public void SendConsumesAckBeforeWriterFailure(GolemDatagramLane lane)
        {
            var sent = new List<byte[]>();
            var fail = true;
            var protocol = new GolemDatagramProtocol(bytes => { if (fail) throw new InvalidOperationException("write"); sent.Add(bytes); });
            protocol.Receive(EncodePacket(7, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            Assert.Throws<InvalidOperationException>(() => protocol.Send(lane, new byte[] { 42 }));
            fail = false;
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent, Is.Empty);
            if (lane != GolemDatagramLane.Unreliable)
            {
                protocol.DrainRetries(DateTime.UtcNow.AddMilliseconds(100));
                Assert.That(sent.Count, Is.EqualTo(1));
                Assert.That(U16(sent[0], 0), Is.EqualTo(1));
                Assert.That(U16(sent[0], 22), Is.EqualTo(0));
                Assert.That(sent[0].Last(), Is.EqualTo(42));
            }
        }

        [Test]
        public void EncodingFailureConsumesAckAndPacketSequence()
        {
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add);
            protocol.Receive(EncodePacket(7, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            Assert.That(Assert.Throws<InvalidOperationException>(() => protocol.Send((GolemDatagramLane)255, null)).Message,
                Is.EqualTo("golem-unity: unknown datagram lane 255"));
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent, Is.Empty);
            protocol.Send(GolemDatagramLane.Unreliable, null);
            Assert.That(U16(sent[0], 0), Is.EqualTo(1));
        }

        [Test]
        public void ReliableEncodingFailureKeepsCommittedRetryState()
        {
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add);
            var now = new DateTime(2020, 1, 1);
            protocol.Receive(EncodePacket(7, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            var error = Assert.Throws<TargetInvocationException>(() => SendReliableAt(protocol, new byte[1175], now));
            Assert.That(error.InnerException, Is.TypeOf<InvalidOperationException>());
            Assert.That(error.InnerException.Message, Is.EqualTo("golem-unity: datagram size 1201 exceeds max 1200"));
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent, Is.Empty);
            Assert.Throws<InvalidOperationException>(() => protocol.DrainRetries(now.AddMilliseconds(75)));
            protocol.Send(GolemDatagramLane.Unreliable, null);
            Assert.That(U16(sent[0], 0), Is.EqualTo(2), "failed original and retry each consume a packet sequence");
        }

        [Test]
        public void RetryExpiryIsValidatedBeforeAnyPacketOrAckIsConsumed()
        {
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add);
            var now = new DateTime(2020, 1, 1);
            SendReliableAt(protocol, new byte[] { 1 }, now.AddMilliseconds(-100));
            SendReliableAt(protocol, new byte[] { 2 }, now.AddSeconds(-4));
            protocol.Receive(EncodePacket(9, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            Assert.That(Assert.Throws<InvalidOperationException>(() => protocol.DrainRetries(now)).Message,
                Is.EqualTo("golem-unity: reliable datagram delivery stalled"));
            Assert.That(sent.Count, Is.EqualTo(2));
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent.Count, Is.EqualTo(3));
            Assert.That(U16(sent[2], 0), Is.EqualTo(2));
            Assert.That(U16(sent[2], 2), Is.EqualTo(9));
            var pending = (IDictionary)typeof(GolemDatagramProtocol).GetField("_pendingReliable", BindingFlags.NonPublic | BindingFlags.Instance).GetValue(protocol);
            Assert.That(pending.Keys, Is.EquivalentTo(new ushort[] { 0, 1 }));
        }

        [Test]
        public void FailedAckOnlyWriteConsumesStateAndReceiveMaskWraps()
        {
            var sent = new List<byte[]>();
            var fail = true;
            var protocol = new GolemDatagramProtocol(bytes => { if (fail) throw new InvalidOperationException("write"); sent.Add(bytes); });
            protocol.Receive(EncodePacket(65535, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            protocol.Receive(EncodePacket(0, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            Assert.Throws<InvalidOperationException>(() => protocol.SendDueAck(DateTime.MaxValue));
            fail = false;
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent, Is.Empty);
            protocol.Send(GolemDatagramLane.Unreliable, null);
            Assert.That(sent[0], Is.EqualTo(Hex("00010000000000010000000000000000000000000001")));
        }

        [Test]
        public void RetriesRestampPacketsPreserveMessagesAndSnapshotIndependently()
        {
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add);
            var now = new DateTime(2020, 1, 1, 0, 0, 0, DateTimeKind.Utc);
            SendReliableAt(protocol, new byte[] { 41 }, now);
            SendReliableAt(protocol, new byte[] { 42 }, now);
            protocol.Receive(EncodePacket(7, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            protocol.DrainRetries(now);
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent.Count, Is.EqualTo(3), "no due retries must retain pending ACK");
            protocol.Receive(EncodePacket(8, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            protocol.DrainRetries(now.AddMilliseconds(75));
            Assert.That(sent.Count, Is.EqualTo(5));
            var retry = sent.Skip(3).OrderBy(p => p.Last()).ToArray();
            for (var i = 0; i < 2; i++)
            {
                Assert.That(U16(retry[i], 0), Is.GreaterThan(2));
                Assert.That(U16(retry[i], 2), Is.EqualTo(8));
                Assert.That(U16(retry[i], 22), Is.EqualTo(i));
                Assert.That(U16(retry[i], 24), Is.EqualTo(i));
            }
            var pending = (IDictionary)typeof(GolemDatagramProtocol).GetField("_pendingReliable", BindingFlags.NonPublic | BindingFlags.Instance).GetValue(protocol);
            var masks = new List<uint[]>();
            foreach (var item in pending.Values)
            {
                var packet = item.GetType().GetField("Packet").GetValue(item);
                masks.Add((uint[])packet.GetType().GetField("AckMask").GetValue(packet));
            }
            Assert.That(masks[0], Is.Not.SameAs(masks[1]));
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent.Count, Is.EqualTo(5));
            protocol.Receive(Hex("ffff00040000000100000000000000000000000001"), null);
            protocol.DrainRetries(now.AddSeconds(10));
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent.Count, Is.EqualTo(5), "ACK-only receipt clears retries without ACKing the ACK");
        }

        [Test]
        public void FailedRetryBatchCommitsAllRetriesBeforeWriting()
        {
            var sent = new List<byte[]>();
            var fail = false;
            var protocol = new GolemDatagramProtocol(b => { if (fail) throw new InvalidOperationException(); sent.Add(b); });
            var now = new DateTime(2020, 1, 1);
            SendReliableAt(protocol, new byte[] { 1 }, now);
            SendReliableAt(protocol, new byte[] { 2 }, now);
            protocol.Receive(EncodePacket(9, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            fail = true;
            Assert.Throws<InvalidOperationException>(() => protocol.DrainRetries(now.AddMilliseconds(75)));
            fail = false;
            protocol.SendDueAck(DateTime.MaxValue);
            protocol.DrainRetries(now.AddMilliseconds(100));
            Assert.That(sent.Count, Is.EqualTo(2));
            protocol.DrainRetries(now.AddMilliseconds(225));
            Assert.That(sent.Count, Is.EqualTo(4));
            Assert.That(sent.Skip(2).Select(p => U16(p, 0)), Is.EquivalentTo(new[] { 4, 5 }));
        }

        [Test]
        public void StreamPiggybackPreservesRefusalAndSequenceContracts()
        {
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add);
            var large = new byte[GameClient.MaxReliableMessageBytes - 22];
            Assert.That(protocol.WrapStreamPayload(large), Is.SameAs(large));
            protocol.Receive(EncodePacket(0x1234, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            Assert.That(protocol.WrapStreamPayload(large), Is.SameAs(large));
            var wrapped = protocol.WrapStreamPayload(new byte[GameClient.MaxReliableMessageBytes - 23]);
            Assert.That(wrapped.Length, Is.EqualTo(GameClient.MaxReliableMessageBytes));
            Assert.That(wrapped.Take(23).ToArray(), Is.EqualTo(Hex("004f475302123400000000000000000000000000000000")));
            protocol.SendDueAck(DateTime.MaxValue);
            Assert.That(sent, Is.Empty);
            protocol.Send(GolemDatagramLane.Unreliable, null);
            Assert.That(U16(sent[0], 0), Is.EqualTo(0));
        }

        [Test]
        public void AckOnlyAndOrdinaryPacketsShareWrappingSequence()
        {
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add);
            typeof(GolemDatagramProtocol).GetField("_nextPacketSeq", BindingFlags.NonPublic | BindingFlags.Instance).SetValue(protocol, ushort.MaxValue);
            protocol.Receive(EncodePacket(1, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
            protocol.SendDueAck(DateTime.MaxValue);
            protocol.Send(GolemDatagramLane.Unreliable, null);
            Assert.That(sent[0].Length, Is.EqualTo(21));
            Assert.That(U16(sent[0], 0), Is.EqualTo(65535));
            Assert.That(U16(sent[1], 0), Is.Zero);
        }

        [Test]
        public void ReceiveDuringBlockedWriteRetainsItsOwnPendingAck()
        {
            using (var entered = new ManualResetEventSlim())
            using (var release = new ManualResetEventSlim())
            {
                var sent = new List<byte[]>();
                var first = true;
                var protocol = new GolemDatagramProtocol(bytes =>
                {
                    if (first) { first = false; entered.Set(); if (!release.Wait(5000)) throw new TimeoutException(); }
                    sent.Add(bytes);
                });
                protocol.Receive(EncodePacket(1, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null);
                var writing = Task.Run(() => protocol.Send(GolemDatagramLane.Unreliable, null));
                try
                {
                    Assert.That(entered.Wait(2000), Is.True);
                    var receiving = Task.Run(() => protocol.Receive(EncodePacket(2, 100, GolemDatagramLane.Unreliable, Array.Empty<byte>()), null));
                    Assert.That(receiving.Wait(2000), Is.True, "write must not hold the protocol lock");
                }
                finally { release.Set(); }
                Assert.That(writing.Wait(2000), Is.True);
                protocol.SendDueAck(DateTime.MaxValue);
                Assert.That(sent.Count, Is.EqualTo(2));
                Assert.That(U16(sent[0], 2), Is.EqualTo(1));
                Assert.That(U16(sent[1], 2), Is.EqualTo(2));
            }
        }

        private static void SendReliableAt(GolemDatagramProtocol protocol, byte[] payload, DateTime now)
        {
            typeof(GolemDatagramProtocol).GetMethod("SendReliable", BindingFlags.NonPublic | BindingFlags.Instance)
                .Invoke(protocol, new object[] { GolemDatagramLane.ReliableOrdered, payload, now });
        }

        private static int U16(byte[] bytes, int offset) => (bytes[offset] << 8) | bytes[offset + 1];
        private static byte[] Hex(string hex) => Enumerable.Range(0, hex.Length / 2)
            .Select(i => Convert.ToByte(hex.Substring(i * 2, 2), 16)).ToArray();
    }
}
