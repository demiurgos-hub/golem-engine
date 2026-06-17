using System;
using System.Collections.Generic;
using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed class GolemDatagramProtocolTests
    {
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
