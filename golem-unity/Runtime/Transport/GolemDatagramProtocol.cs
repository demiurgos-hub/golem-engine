using System;
using System.Collections.Generic;

namespace GolemEngine.Unity
{
    /// <summary>Datagram lane identifiers used by the Golem WebTransport protocol.</summary>
    public enum GolemDatagramLane : byte
    {
        Unreliable = 1,
        ReliableUnordered = 2,
        ReliableOrdered = 3,
        EventualState = 4,
    }

    /// <summary>Implements Golem's WebTransport datagram packet protocol.</summary>
    public sealed class GolemDatagramProtocol
    {
        public const int MaxWebTransportDatagramBytes = 1200;
        public const int AckMaskWordCount = 4;
        public const int AckMaskBytes = AckMaskWordCount * 4;
        public const int PacketHeaderBytes = 2 + 2 + AckMaskBytes + 1;
        public const int LaneHeaderBytes = 1;
        public const int ReliableMessageIdBytes = 2;
        public const int ReliableOrderedSequenceBytes = 2;
        public const int EventualStateTokenBytes = 8;
        public const int AckWindow = AckMaskWordCount * 32;
        public const byte AckOnlyFlag = 1;

        private static readonly byte[] ClientReliableAckControlFrame = { 0x00, (byte)'O', (byte)'G', (byte)'S', 0x02 };
        private static readonly TimeSpan DefaultAckCoalesceDelay = TimeSpan.FromMilliseconds(1);
        private static readonly TimeSpan ReliableRetryBaseDelay = TimeSpan.FromMilliseconds(75);
        private static readonly TimeSpan ReliableRetryMaxDelay = TimeSpan.FromMilliseconds(400);
        private static readonly TimeSpan ReliableMessageTtl = TimeSpan.FromSeconds(3);
        private static readonly TimeSpan ReliableOrderedGapTimeout = TimeSpan.FromSeconds(3);
        private const int ReliableRetryLimit = 8;

        private readonly object _lock = new object();
        private readonly Action<byte[]> _write;
        private readonly TimeSpan _ackInterval;
        private readonly SequenceWindow _recvPackets = new SequenceWindow();
        private readonly SequenceWindow _recvReliableMessages = new SequenceWindow();
        private readonly OrderedReceiveState _orderedRecv = new OrderedReceiveState();
        private readonly Dictionary<ushort, PendingReliableDatagram> _pendingReliable = new Dictionary<ushort, PendingReliableDatagram>();
        private ushort _nextPacketSeq;
        private ushort _nextMessageId;
        private ushort _nextOrderedSeq;
        private bool _ackDirty;
        private DateTime _ackDueAt;

        public GolemDatagramProtocol(Action<byte[]> write, int ackIntervalMilliseconds = 1)
        {
            _write = write ?? throw new ArgumentNullException(nameof(write));
            _ackInterval = ackIntervalMilliseconds <= 0 ? DefaultAckCoalesceDelay : TimeSpan.FromMilliseconds(ackIntervalMilliseconds);
        }

        public static int SchedulerIntervalMilliseconds => 2;

        public static byte[] EncodeLengthPrefixedFrame(byte[] data)
        {
            data ??= Array.Empty<byte>();
            var outBytes = new byte[4 + data.Length];
            WriteUint32(outBytes, 0, (uint)data.Length);
            Array.Copy(data, 0, outBytes, 4, data.Length);
            return outBytes;
        }

        public static void DecodeLengthPrefixedFrames(byte[] data, Action<byte[]> fn)
        {
            data ??= Array.Empty<byte>();
            var offset = 0;
            while (offset < data.Length)
            {
                if (data.Length - offset < 4)
                {
                    throw new InvalidOperationException("golem-unity: length-prefixed batch truncated");
                }
                var length = checked((int)ReadUint32(data, offset));
                offset += 4;
                if (data.Length - offset < length)
                {
                    throw new InvalidOperationException("golem-unity: length-prefixed frame truncated");
                }
                var frame = new byte[length];
                Array.Copy(data, offset, frame, 0, length);
                fn?.Invoke(frame);
                offset += length;
            }
        }

        public void Send(GolemDatagramLane lane, byte[] payload)
        {
            if (lane == GolemDatagramLane.ReliableUnordered || lane == GolemDatagramLane.ReliableOrdered)
            {
                SendReliable(lane, payload, DateTime.UtcNow);
                return;
            }

            DatagramPacket packet;
            lock (_lock)
            {
                packet = new DatagramPacket
                {
                    PacketSeq = _nextPacketSeq++,
                    AckSeq = _recvPackets.Latest,
                    AckMask = CopyAckMask(_recvPackets.Mask),
                    Lane = lane,
                    Payload = CopyPayload(payload),
                };
                _ackDirty = false;
                _ackDueAt = default;
            }
            _write(EncodePacket(packet));
        }

        public void Receive(byte[] data, Action<GolemDatagramLane, byte[]> deliver)
        {
            var now = DateTime.UtcNow;
            var packet = DecodePacket(data);
            lock (_lock)
            {
                ApplyPeerAcksLocked(packet.AckSeq, packet.AckMask);
            }
            if ((packet.Flags & AckOnlyFlag) != 0)
            {
                return;
            }

            bool accepted;
            lock (_lock)
            {
                accepted = _recvPackets.Accept(packet.PacketSeq);
                if (accepted)
                {
                    ScheduleAckLocked(now);
                }
            }
            if (!accepted)
            {
                return;
            }

            if (packet.Lane == GolemDatagramLane.ReliableUnordered || packet.Lane == GolemDatagramLane.ReliableOrdered)
            {
                lock (_lock)
                {
                    accepted = _recvReliableMessages.Accept(packet.MessageId);
                }
                if (!accepted)
                {
                    return;
                }
            }

            if (packet.Lane == GolemDatagramLane.ReliableOrdered)
            {
                DeliverOrdered(packet, now, deliver);
                return;
            }

            deliver?.Invoke(packet.Lane, CopyPayload(packet.Payload));
        }

        public void SendDueAck(DateTime now)
        {
            DatagramPacket packet;
            lock (_lock)
            {
                if (!_ackDirty || (_ackDueAt != default && now < _ackDueAt))
                {
                    return;
                }
                packet = new DatagramPacket
                {
                    PacketSeq = _nextPacketSeq++,
                    AckSeq = _recvPackets.Latest,
                    AckMask = CopyAckMask(_recvPackets.Mask),
                    Flags = AckOnlyFlag,
                };
                _ackDirty = false;
                _ackDueAt = default;
            }
            _write(EncodePacket(packet));
        }

        public void DrainRetries(DateTime now)
        {
            var packets = new List<DatagramPacket>();
            lock (_lock)
            {
                var retryKeys = new List<ushort>();
                foreach (var item in _pendingReliable)
                {
                    var pending = item.Value;
                    if (now - pending.QueuedAt > ReliableMessageTtl || pending.Attempts >= ReliableRetryLimit)
                    {
                        throw new InvalidOperationException("golem-unity: reliable datagram delivery stalled");
                    }
                    if (now >= pending.NextSendAt)
                    {
                        retryKeys.Add(item.Key);
                    }
                }

                foreach (var key in retryKeys)
                {
                    var pending = _pendingReliable[key];
                    _pendingReliable.Remove(key);
                    var packet = pending.Packet;
                    packet.PacketSeq = _nextPacketSeq++;
                    packet.AckSeq = _recvPackets.Latest;
                    packet.AckMask = CopyAckMask(_recvPackets.Mask);
                    pending.Packet = packet;
                    pending.Attempts++;
                    pending.LastSentAt = now;
                    pending.NextSendAt = now + RetryDelay(pending.Attempts);
                    _pendingReliable[packet.PacketSeq] = pending;
                    packets.Add(packet);
                }

                if (packets.Count > 0)
                {
                    _ackDirty = false;
                    _ackDueAt = default;
                }
            }

            foreach (var packet in packets)
            {
                _write(EncodePacket(packet));
            }
        }

        public byte[] WrapStreamPayload(byte[] payload)
        {
            payload ??= Array.Empty<byte>();
            ushort ackSeq;
            uint[] ackMask;
            lock (_lock)
            {
                if (!_ackDirty || payload.Length + ClientReliableAckControlFrame.Length + 2 + AckMaskBytes > GameClient.MaxReliableMessageBytes)
                {
                    return payload;
                }
                ackSeq = _recvPackets.Latest;
                ackMask = CopyAckMask(_recvPackets.Mask);
                _ackDirty = false;
                _ackDueAt = default;
            }

            var outBytes = new byte[ClientReliableAckControlFrame.Length + 2 + AckMaskBytes + payload.Length];
            var offset = 0;
            Array.Copy(ClientReliableAckControlFrame, 0, outBytes, offset, ClientReliableAckControlFrame.Length);
            offset += ClientReliableAckControlFrame.Length;
            WriteUint16(outBytes, offset, ackSeq);
            offset += 2;
            for (var i = 0; i < AckMaskWordCount; i++)
            {
                WriteUint32(outBytes, offset, ackMask[i]);
                offset += 4;
            }
            Array.Copy(payload, 0, outBytes, offset, payload.Length);
            return outBytes;
        }

        private void SendReliable(GolemDatagramLane lane, byte[] payload, DateTime now)
        {
            DatagramPacket packet;
            lock (_lock)
            {
                packet = new DatagramPacket
                {
                    PacketSeq = _nextPacketSeq++,
                    AckSeq = _recvPackets.Latest,
                    AckMask = CopyAckMask(_recvPackets.Mask),
                    Lane = lane,
                    MessageId = _nextMessageId++,
                    Payload = CopyPayload(payload),
                };
                if (lane == GolemDatagramLane.ReliableOrdered)
                {
                    packet.OrderedSeq = _nextOrderedSeq++;
                }
                _ackDirty = false;
                _ackDueAt = default;
                _pendingReliable[packet.PacketSeq] = new PendingReliableDatagram
                {
                    Packet = packet,
                    QueuedAt = now,
                    LastSentAt = now,
                    NextSendAt = now + ReliableRetryBaseDelay,
                    Attempts = 1,
                };
            }
            _write(EncodePacket(packet));
        }

        private void DeliverOrdered(DatagramPacket packet, DateTime now, Action<GolemDatagramLane, byte[]> deliver)
        {
            var deliveries = new List<byte[]>();
            lock (_lock)
            {
                if (!_orderedRecv.Init)
                {
                    _orderedRecv.Init = true;
                    _orderedRecv.NextSeq = packet.OrderedSeq;
                }
                if (packet.OrderedSeq == _orderedRecv.NextSeq)
                {
                    deliveries.Add(CopyPayload(packet.Payload));
                    _orderedRecv.NextSeq++;
                    while (_orderedRecv.Pending.TryGetValue(_orderedRecv.NextSeq, out var payload))
                    {
                        _orderedRecv.Pending.Remove(_orderedRecv.NextSeq);
                        deliveries.Add(CopyPayload(payload));
                        _orderedRecv.NextSeq++;
                    }
                    if (_orderedRecv.Pending.Count == 0)
                    {
                        _orderedRecv.GapSince = default;
                    }
                }
                else if (SequenceNewer(packet.OrderedSeq, _orderedRecv.NextSeq))
                {
                    _orderedRecv.Pending[packet.OrderedSeq] = CopyPayload(packet.Payload);
                    if (_orderedRecv.GapSince == default)
                    {
                        _orderedRecv.GapSince = now;
                    }
                    if (now - _orderedRecv.GapSince > ReliableOrderedGapTimeout)
                    {
                        throw new InvalidOperationException("golem-unity: reliable ordered datagram gap expired");
                    }
                }
            }

            foreach (var payload in deliveries)
            {
                deliver?.Invoke(GolemDatagramLane.ReliableOrdered, payload);
            }
        }

        private void ScheduleAckLocked(DateTime now)
        {
            _ackDirty = true;
            _ackDueAt = now + _ackInterval;
        }

        private void ApplyPeerAcksLocked(ushort ackSeq, uint[] ackMask)
        {
            var acked = new List<ushort>();
            foreach (var seq in _pendingReliable.Keys)
            {
                if (seq == ackSeq)
                {
                    acked.Add(seq);
                    continue;
                }
                var diff = SequenceDistance(seq, ackSeq);
                if (diff <= 0 || diff > AckWindow)
                {
                    continue;
                }
                var bit = diff - 1;
                if ((ackMask[bit / 32] & (1u << (bit % 32))) != 0)
                {
                    acked.Add(seq);
                }
            }
            foreach (var seq in acked)
            {
                _pendingReliable.Remove(seq);
            }
        }

        private static byte[] EncodePacket(DatagramPacket packet)
        {
            if ((packet.Flags & AckOnlyFlag) == 0 && !IsKnownLane(packet.Lane))
            {
                throw new InvalidOperationException($"golem-unity: unknown datagram lane {(byte)packet.Lane}");
            }

            var length = PacketHeaderBytes;
            if ((packet.Flags & AckOnlyFlag) == 0)
            {
                length += LaneHeaderBytes + packet.Payload.Length;
                if (packet.Lane == GolemDatagramLane.ReliableUnordered)
                {
                    length += ReliableMessageIdBytes;
                }
                else if (packet.Lane == GolemDatagramLane.ReliableOrdered)
                {
                    length += ReliableMessageIdBytes + ReliableOrderedSequenceBytes;
                }
                else if (packet.Lane == GolemDatagramLane.EventualState)
                {
                    length += EventualStateTokenBytes;
                }
            }
            if (length > MaxWebTransportDatagramBytes)
            {
                throw new InvalidOperationException($"golem-unity: datagram size {length} exceeds max {MaxWebTransportDatagramBytes}");
            }

            var outBytes = new byte[length];
            WriteUint16(outBytes, 0, packet.PacketSeq);
            WriteUint16(outBytes, 2, packet.AckSeq);
            for (var i = 0; i < AckMaskWordCount; i++)
            {
                WriteUint32(outBytes, 4 + i * 4, packet.AckMask[i]);
            }
            outBytes[PacketHeaderBytes - 1] = packet.Flags;
            if ((packet.Flags & AckOnlyFlag) != 0)
            {
                return outBytes;
            }

            var offset = PacketHeaderBytes;
            outBytes[offset++] = (byte)packet.Lane;
            if (packet.Lane == GolemDatagramLane.ReliableUnordered)
            {
                WriteUint16(outBytes, offset, packet.MessageId);
                offset += 2;
            }
            else if (packet.Lane == GolemDatagramLane.ReliableOrdered)
            {
                WriteUint16(outBytes, offset, packet.MessageId);
                offset += 2;
                WriteUint16(outBytes, offset, packet.OrderedSeq);
                offset += 2;
            }
            else if (packet.Lane == GolemDatagramLane.EventualState)
            {
                WriteUint64(outBytes, offset, packet.StateToken);
                offset += 8;
            }
            Array.Copy(packet.Payload, 0, outBytes, offset, packet.Payload.Length);
            return outBytes;
        }

        private static DatagramPacket DecodePacket(byte[] data)
        {
            if (data == null || data.Length < PacketHeaderBytes)
            {
                throw new InvalidOperationException("golem-unity: datagram packet too small");
            }

            var packet = new DatagramPacket
            {
                PacketSeq = ReadUint16(data, 0),
                AckSeq = ReadUint16(data, 2),
                AckMask = new uint[AckMaskWordCount],
                Flags = data[PacketHeaderBytes - 1],
            };
            for (var i = 0; i < AckMaskWordCount; i++)
            {
                packet.AckMask[i] = ReadUint32(data, 4 + i * 4);
            }
            if ((packet.Flags & AckOnlyFlag) != 0)
            {
                return packet;
            }

            var offset = PacketHeaderBytes;
            if (data.Length < offset + LaneHeaderBytes)
            {
                throw new InvalidOperationException("golem-unity: datagram lane missing");
            }
            packet.Lane = (GolemDatagramLane)data[offset++];
            switch (packet.Lane)
            {
                case GolemDatagramLane.Unreliable:
                    break;
                case GolemDatagramLane.ReliableUnordered:
                    if (data.Length < offset + ReliableMessageIdBytes)
                    {
                        throw new InvalidOperationException("golem-unity: reliable datagram message id missing");
                    }
                    packet.MessageId = ReadUint16(data, offset);
                    offset += 2;
                    break;
                case GolemDatagramLane.ReliableOrdered:
                    if (data.Length < offset + ReliableMessageIdBytes + ReliableOrderedSequenceBytes)
                    {
                        throw new InvalidOperationException("golem-unity: ordered datagram header missing");
                    }
                    packet.MessageId = ReadUint16(data, offset);
                    offset += 2;
                    packet.OrderedSeq = ReadUint16(data, offset);
                    offset += 2;
                    break;
                case GolemDatagramLane.EventualState:
                    if (data.Length < offset + EventualStateTokenBytes)
                    {
                        throw new InvalidOperationException("golem-unity: state datagram token missing");
                    }
                    packet.StateToken = ReadUint64(data, offset);
                    offset += 8;
                    break;
                default:
                    throw new InvalidOperationException($"golem-unity: unknown datagram lane {(byte)packet.Lane}");
            }

            packet.Payload = new byte[data.Length - offset];
            Array.Copy(data, offset, packet.Payload, 0, packet.Payload.Length);
            return packet;
        }

        private static bool IsKnownLane(GolemDatagramLane lane)
        {
            return lane == GolemDatagramLane.Unreliable ||
                lane == GolemDatagramLane.ReliableUnordered ||
                lane == GolemDatagramLane.ReliableOrdered ||
                lane == GolemDatagramLane.EventualState;
        }

        private static bool SequenceNewer(ushort a, ushort b)
        {
            return a != b && unchecked((ushort)(a - b)) < 0x8000;
        }

        private static int SequenceDistance(ushort older, ushort newer)
        {
            return unchecked((ushort)(newer - older));
        }

        private static TimeSpan RetryDelay(int attempts)
        {
            var multiplier = 1 << Math.Min(attempts - 1, 3);
            var delay = TimeSpan.FromTicks(ReliableRetryBaseDelay.Ticks * multiplier);
            return delay > ReliableRetryMaxDelay ? ReliableRetryMaxDelay : delay;
        }

        private static byte[] CopyPayload(byte[] payload)
        {
            payload ??= Array.Empty<byte>();
            var copy = new byte[payload.Length];
            Array.Copy(payload, copy, payload.Length);
            return copy;
        }

        private static uint[] CopyAckMask(uint[] mask)
        {
            var copy = new uint[AckMaskWordCount];
            if (mask != null)
            {
                Array.Copy(mask, copy, Math.Min(mask.Length, AckMaskWordCount));
            }
            return copy;
        }

        private static ushort ReadUint16(byte[] data, int offset)
        {
            return (ushort)((data[offset] << 8) | data[offset + 1]);
        }

        private static uint ReadUint32(byte[] data, int offset)
        {
            return ((uint)data[offset] << 24) |
                ((uint)data[offset + 1] << 16) |
                ((uint)data[offset + 2] << 8) |
                data[offset + 3];
        }

        private static ulong ReadUint64(byte[] data, int offset)
        {
            return ((ulong)ReadUint32(data, offset) << 32) | ReadUint32(data, offset + 4);
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

        private struct DatagramPacket
        {
            public ushort PacketSeq;
            public ushort AckSeq;
            public uint[] AckMask;
            public byte Flags;
            public GolemDatagramLane Lane;
            public ushort MessageId;
            public ushort OrderedSeq;
            public ulong StateToken;
            public byte[] Payload;
        }

        private sealed class PendingReliableDatagram
        {
            public DatagramPacket Packet;
            public DateTime QueuedAt;
            public DateTime LastSentAt;
            public DateTime NextSendAt;
            public int Attempts;
        }

        private sealed class OrderedReceiveState
        {
            public bool Init;
            public ushort NextSeq;
            public readonly Dictionary<ushort, byte[]> Pending = new Dictionary<ushort, byte[]>();
            public DateTime GapSince;
        }

        private sealed class SequenceWindow
        {
            public bool Init { get; private set; }
            public ushort Latest { get; private set; }
            public uint[] Mask { get; } = new uint[AckMaskWordCount];

            public bool Accept(ushort seq)
            {
                if (!Init)
                {
                    Init = true;
                    Latest = seq;
                    return true;
                }
                if (seq == Latest)
                {
                    return false;
                }
                if (SequenceNewer(seq, Latest))
                {
                    var diff = SequenceDistance(Latest, seq);
                    ShiftLeft(Mask, diff);
                    SetBit(Mask, diff - 1);
                    Latest = seq;
                    return true;
                }

                var oldDiff = SequenceDistance(seq, Latest);
                if (oldDiff == 0 || oldDiff > AckWindow)
                {
                    return false;
                }
                var bit = oldDiff - 1;
                if ((Mask[bit / 32] & (1u << (bit % 32))) != 0)
                {
                    return false;
                }
                SetBit(Mask, bit);
                return true;
            }

            private static void SetBit(uint[] mask, int bit)
            {
                if (bit < 0 || bit >= AckWindow)
                {
                    return;
                }
                mask[bit / 32] |= 1u << (bit % 32);
            }

            private static void ShiftLeft(uint[] mask, int bits)
            {
                if (bits <= 0)
                {
                    return;
                }
                if (bits >= AckWindow)
                {
                    Array.Clear(mask, 0, mask.Length);
                    return;
                }

                var shifted = new uint[AckMaskWordCount];
                var wordShift = bits / 32;
                var bitShift = bits % 32;
                for (var i = AckMaskWordCount - 1; i >= 0; i--)
                {
                    var src = i - wordShift;
                    if (src < 0)
                    {
                        continue;
                    }
                    shifted[i] = mask[src] << bitShift;
                    if (bitShift > 0 && src > 0)
                    {
                        shifted[i] |= mask[src - 1] >> (32 - bitShift);
                    }
                }
                Array.Copy(shifted, mask, AckMaskWordCount);
            }
        }
    }
}
