using System;
using System.Collections.Generic;
using System.Text;

namespace GolemEngine.Unity
{
    /// <summary>Writes the protobuf wire-format scalar values used by Golem schemas.</summary>
    public sealed class PbWriter
    {
        private readonly List<byte> _buffer = new List<byte>();

        public PbWriter Tag(int field, int wireType)
        {
            return Varint((ulong)((field << 3) | wireType));
        }

        public PbWriter Int32(int value)
        {
            return value < 0 ? Varint(unchecked((ulong)(long)value)) : Varint((ulong)value);
        }

        public PbWriter Int64(long value)
        {
            return Varint(unchecked((ulong)value));
        }

        public PbWriter Uint32(uint value)
        {
            return Varint(value);
        }

        public PbWriter Uint64(ulong value)
        {
            return Varint(value);
        }

        public PbWriter Sint32(int value)
        {
            return Varint((uint)((value << 1) ^ (value >> 31)));
        }

        public PbWriter Sint64(long value)
        {
            return Varint((ulong)((value << 1) ^ (value >> 63)));
        }

        public PbWriter Bool(bool value)
        {
            return Varint(value ? 1UL : 0UL);
        }

        public PbWriter Float(float value)
        {
            var bytes = BitConverter.GetBytes(value);
            if (!BitConverter.IsLittleEndian)
            {
                Array.Reverse(bytes);
            }
            _buffer.AddRange(bytes);
            return this;
        }

        public PbWriter Double(double value)
        {
            var bytes = BitConverter.GetBytes(value);
            if (!BitConverter.IsLittleEndian)
            {
                Array.Reverse(bytes);
            }
            _buffer.AddRange(bytes);
            return this;
        }

        public PbWriter String(string value)
        {
            var bytes = Encoding.UTF8.GetBytes(value ?? string.Empty);
            Varint((ulong)bytes.Length);
            _buffer.AddRange(bytes);
            return this;
        }

        public PbWriter Bytes(byte[] value)
        {
            value ??= Array.Empty<byte>();
            Varint((ulong)value.Length);
            _buffer.AddRange(value);
            return this;
        }

        public byte[] Finish()
        {
            return _buffer.ToArray();
        }

        private PbWriter Varint(ulong value)
        {
            while (value > 0x7f)
            {
                _buffer.Add((byte)((value & 0x7f) | 0x80));
                value >>= 7;
            }
            _buffer.Add((byte)value);
            return this;
        }
    }
}
