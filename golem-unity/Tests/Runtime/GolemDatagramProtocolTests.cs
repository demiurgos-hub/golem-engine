using System;
using System.Collections.Generic;
using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed partial class GolemDatagramProtocolTests
    {
        [Test]
        public void TryGetEventRecordReadsTransitionTagsLikeTheServer()
        {
            // Server golden bytes (internal/netwire): untagged 00 0a 01; tagged with 300: 00 00 ac 02 0a 01.
            Assert.That(GolemDatagramProtocol.TryGetEventRecord(new byte[] { 0, 0x0a, 0x01 }, out ulong transition, out var serverEvent), Is.True);
            Assert.That(transition, Is.EqualTo(0UL));
            Assert.That(serverEvent, Is.EqualTo(new byte[] { 0x0a, 0x01 }));
            Assert.That(GolemDatagramProtocol.TryGetEventRecord(new byte[] { 0, 0, 0xac, 0x02, 0x0a, 0x01 }, out transition, out serverEvent), Is.True);
            Assert.That(transition, Is.EqualTo(300UL));
            Assert.That(serverEvent, Is.EqualTo(new byte[] { 0x0a, 0x01 }));
            Assert.That(GolemDatagramProtocol.TryGetEventRecord(new byte[] { 0, 0, 0xac, 0x02, 0x0a, 0x01 }, out byte[] untaggedView), Is.True);
            Assert.That(untaggedView, Is.EqualTo(new byte[] { 0x0a, 0x01 }));

            // Empty events stay unambiguous in both forms.
            Assert.That(GolemDatagramProtocol.TryGetEventRecord(new byte[] { 0 }, out transition, out serverEvent), Is.True);
            Assert.That(transition, Is.EqualTo(0UL));
            Assert.That(serverEvent, Is.Empty);
            Assert.That(GolemDatagramProtocol.TryGetEventRecord(new byte[] { 0, 0, 7 }, out transition, out serverEvent), Is.True);
            Assert.That(transition, Is.EqualTo(7UL));
            Assert.That(serverEvent, Is.Empty);

            // Malformed tagged records and compact state frames are rejected.
            foreach (var frame in new[]
            {
                Array.Empty<byte>(),
                new byte[] { 0, 0 },
                new byte[] { 0, 0, 0x80 },
                new byte[] { 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01 },
                new byte[] { 1, 0 },
            })
            {
                Assert.That(GolemDatagramProtocol.TryGetEventRecord(frame, out transition, out serverEvent), Is.False, BitConverter.ToString(frame));
            }
        }

        [Test]
        public void SendUnreliableEncodesLaneAndPayload()
        {
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add);

            protocol.Send(GolemDatagramLane.Unreliable, new byte[] { 4, 5, 6 });

            Assert.That(sent, Has.Count.EqualTo(1));
            Assert.That(sent[0][GolemDatagramProtocol.PacketHeaderBytes], Is.EqualTo((byte)GolemDatagramLane.Unreliable));
            Assert.That(Slice(sent[0], GolemDatagramProtocol.PacketHeaderBytes + 1), Is.EqualTo(new byte[] { 4, 5, 6 }));
        }

        [Test]
        public void ReceiveEventualStateDeliversPayload()
        {
            var protocol = new GolemDatagramProtocol(_ => { });
            var delivered = new List<byte[]>();
            var packet = EncodePacket(
                3,
                0,
                GolemDatagramLane.EventualState,
                new byte[] { 7, 8 },
                stateToken: 99);

            protocol.Receive(packet, (lane, payload) =>
            {
                Assert.That(lane, Is.EqualTo(GolemDatagramLane.EventualState));
                delivered.Add(payload);
            });

            Assert.That(delivered, Has.Count.EqualTo(1));
            Assert.That(delivered[0], Is.EqualTo(new byte[] { 7, 8 }));
        }

        [Test]
        public void LateEventualStateIsNeitherDeliveredNorAcknowledged()
        {
            var sent = new List<byte[]>();
            var protocol = new GolemDatagramProtocol(sent.Add);
            var delivered = new List<byte[]>();
            protocol.Receive(
                EncodePacket(10, 0, GolemDatagramLane.EventualState, new byte[] { 1 }, stateToken: 1),
                (_, payload) => delivered.Add(payload));
            protocol.Receive(
                EncodePacket(9, 0, GolemDatagramLane.EventualState, new byte[] { 2 }, stateToken: 2),
                (_, payload) => delivered.Add(payload));

            protocol.SendDueAck(DateTime.UtcNow.AddSeconds(1));

            Assert.That(delivered, Has.Count.EqualTo(1));
            Assert.That(delivered[0], Is.EqualTo(new byte[] { 1 }));
            Assert.That(sent, Has.Count.EqualTo(1));
            var firstMaskWord = (sent[0][4] << 24) | (sent[0][5] << 16) | (sent[0][6] << 8) | sent[0][7];
            Assert.That(firstMaskWord & 1, Is.Zero);
        }

        [Test]
        public void ReliableOrderedBuffersUntilMissingSequenceArrives()
        {
            var protocol = new GolemDatagramProtocol(_ => { });
            var delivered = new List<byte[]>();

            protocol.Receive(
                EncodePacket(1, 0, GolemDatagramLane.ReliableOrdered, new byte[] { 0 }, messageId: 1, orderedSeq: 0),
                (_, payload) => delivered.Add(payload));
            protocol.Receive(
                EncodePacket(2, 0, GolemDatagramLane.ReliableOrdered, new byte[] { 2 }, messageId: 2, orderedSeq: 2),
                (_, payload) => delivered.Add(payload));
            protocol.Receive(
                EncodePacket(3, 0, GolemDatagramLane.ReliableOrdered, new byte[] { 1 }, messageId: 3, orderedSeq: 1),
                (_, payload) => delivered.Add(payload));

            Assert.That(delivered, Has.Count.EqualTo(3));
            Assert.That(delivered[0], Is.EqualTo(new byte[] { 0 }));
            Assert.That(delivered[1], Is.EqualTo(new byte[] { 1 }));
            Assert.That(delivered[2], Is.EqualTo(new byte[] { 2 }));
        }

        [Test]
        public void WrapStreamPayloadPiggybacksPendingAck()
        {
            var protocol = new GolemDatagramProtocol(_ => { }, ackIntervalMilliseconds: 1000);
            protocol.Receive(
                EncodePacket(5, 0, GolemDatagramLane.Unreliable, new byte[] { 1 }),
                null);

            var wrapped = protocol.WrapStreamPayload(new byte[] { 9 });

            Assert.That(wrapped.Length, Is.EqualTo(5 + 2 + GolemDatagramProtocol.AckMaskBytes + 1));
            Assert.That(wrapped[0], Is.EqualTo(0x00));
            Assert.That(wrapped[1], Is.EqualTo((byte)'O'));
            Assert.That(wrapped[2], Is.EqualTo((byte)'G'));
            Assert.That(wrapped[3], Is.EqualTo((byte)'S'));
            Assert.That(wrapped[4], Is.EqualTo(0x02));
            Assert.That(wrapped[5], Is.EqualTo(0));
            Assert.That(wrapped[6], Is.EqualTo(5));
            Assert.That(wrapped[wrapped.Length - 1], Is.EqualTo(9));
        }

        [Test]
        public void DecodeLengthPrefixedFramesReadsAllFrames()
        {
            var first = GolemDatagramProtocol.EncodeLengthPrefixedFrame(new byte[] { 1 });
            var second = GolemDatagramProtocol.EncodeLengthPrefixedFrame(new byte[] { 2, 3 });
            var batch = new byte[first.Length + second.Length];
            Array.Copy(first, 0, batch, 0, first.Length);
            Array.Copy(second, 0, batch, first.Length, second.Length);
            var frames = new List<byte[]>();

            GolemDatagramProtocol.DecodeLengthPrefixedFrames(batch, frames.Add);

            Assert.That(frames, Has.Count.EqualTo(2));
            Assert.That(frames[0], Is.EqualTo(new byte[] { 1 }));
            Assert.That(frames[1], Is.EqualTo(new byte[] { 2, 3 }));
        }

        [Test]
        public void AcknowledgesReliablePacketsOnlyAfterDelivery()
        {
            // Server.PrepareSpaceChange relies on a reliable packet entering the ACK window only once its record was
            // handed on. WrapStreamPayload piggybacks a pending ACK, so an unchanged payload means none is pending.
            var protocol = new GolemDatagramProtocol(_ => { }, ackIntervalMilliseconds: 1000);
            var delivered = new List<byte[]>();
            protocol.Receive(
                EncodePacket(5, 0, GolemDatagramLane.ReliableUnordered, new byte[] { 1 }, messageId: 0),
                (_, payload) =>
                {
                    Assert.That(protocol.WrapStreamPayload(new byte[] { 9 }), Has.Length.EqualTo(1), "acknowledged before delivery");
                    delivered.Add(payload);
                });
            var wrapped = protocol.WrapStreamPayload(new byte[] { 9 });
            Assert.That(wrapped.Length, Is.GreaterThan(1));
            Assert.That(wrapped[6], Is.EqualTo(5));

            protocol.Receive(
                EncodePacket(6, 0, GolemDatagramLane.ReliableOrdered, new byte[] { 2 }, messageId: 1, orderedSeq: 0),
                (_, payload) =>
                {
                    Assert.That(protocol.WrapStreamPayload(new byte[] { 9 }), Has.Length.EqualTo(1), "acknowledged before delivery");
                    delivered.Add(payload);
                });
            wrapped = protocol.WrapStreamPayload(new byte[] { 9 });
            Assert.That(wrapped[6], Is.EqualTo(6));

            // A resend of a delivered message is acknowledged without a second delivery.
            protocol.Receive(
                EncodePacket(7, 0, GolemDatagramLane.ReliableOrdered, new byte[] { 2 }, messageId: 1, orderedSeq: 0),
                (_, payload) => delivered.Add(payload));
            wrapped = protocol.WrapStreamPayload(new byte[] { 9 });
            Assert.That(wrapped[6], Is.EqualTo(7));
            Assert.That(delivered, Has.Count.EqualTo(2));

            // Unreliable packets keep acknowledging on receipt.
            protocol.Receive(
                EncodePacket(8, 0, GolemDatagramLane.Unreliable, new byte[] { 3 }),
                (_, _) => Assert.That(protocol.WrapStreamPayload(new byte[] { 9 }).Length, Is.GreaterThan(1)));
        }

        private static byte[] Slice(byte[] data, int offset)
        {
            var outBytes = new byte[data.Length - offset];
            Array.Copy(data, offset, outBytes, 0, outBytes.Length);
            return outBytes;
        }

        private static byte[] EncodePacket(
            ushort packetSeq,
            ushort ackSeq,
            GolemDatagramLane lane,
            byte[] payload,
            ushort messageId = 0,
            ushort orderedSeq = 0,
            ulong stateToken = 0)
        {
            var length = GolemDatagramProtocol.PacketHeaderBytes + GolemDatagramProtocol.LaneHeaderBytes + payload.Length;
            if (lane == GolemDatagramLane.ReliableUnordered)
            {
                length += GolemDatagramProtocol.ReliableMessageIdBytes;
            }
            else if (lane == GolemDatagramLane.ReliableOrdered)
            {
                length += GolemDatagramProtocol.ReliableMessageIdBytes + GolemDatagramProtocol.ReliableOrderedSequenceBytes;
            }
            else if (lane == GolemDatagramLane.EventualState)
            {
                length += GolemDatagramProtocol.EventualStateTokenBytes;
            }

            var packet = new byte[length];
            WriteUint16(packet, 0, packetSeq);
            WriteUint16(packet, 2, ackSeq);
            var offset = GolemDatagramProtocol.PacketHeaderBytes;
            packet[offset++] = (byte)lane;
            if (lane == GolemDatagramLane.ReliableUnordered)
            {
                WriteUint16(packet, offset, messageId);
                offset += 2;
            }
            else if (lane == GolemDatagramLane.ReliableOrdered)
            {
                WriteUint16(packet, offset, messageId);
                offset += 2;
                WriteUint16(packet, offset, orderedSeq);
                offset += 2;
            }
            else if (lane == GolemDatagramLane.EventualState)
            {
                WriteUint64(packet, offset, stateToken);
                offset += 8;
            }
            Array.Copy(payload, 0, packet, offset, payload.Length);
            return packet;
        }

        private static void WriteUint16(byte[] data, int offset, ushort value)
        {
            data[offset] = (byte)((value >> 8) & 0xff);
            data[offset + 1] = (byte)(value & 0xff);
        }

        private static void WriteUint32(byte[] data, int offset, uint value)
        {
            data[offset] = (byte)((value >> 24) & 0xff);
            data[offset + 1] = (byte)((value >> 16) & 0xff);
            data[offset + 2] = (byte)((value >> 8) & 0xff);
            data[offset + 3] = (byte)(value & 0xff);
        }

        private static void WriteUint64(byte[] data, int offset, ulong value)
        {
            WriteUint32(data, offset, (uint)(value >> 32));
            WriteUint32(data, offset + 4, (uint)value);
        }
    }
}
