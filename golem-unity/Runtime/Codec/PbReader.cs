using System;
using System.Text;

namespace GolemEngine.Unity
{
    /// <summary>Reads the protobuf wire-format scalar values used by Golem schemas.</summary>
    public sealed class PbReader
    {
        private readonly byte[] _buffer;
        private int _position;

        public PbReader(byte[] buffer)
        {
            _buffer = buffer ?? Array.Empty<byte>();
        }

        public bool Done => _position >= _buffer.Length;

        public (int Field, int Wire) Tag()
        {
            var value = Uvarint();
            return ((int)(value >> 3), (int)(value & 7));
        }

        public int Int32()
        {
            return unchecked((int)Uvarint());
        }

        public long Int64()
        {
            return unchecked((long)Uvarint());
        }

        public uint Uint32()
        {
            return unchecked((uint)Uvarint());
        }

        public ulong Uint64()
        {
            return Uvarint();
        }

        public int Sint32()
        {
            var value = Uvarint();
            return (int)((value >> 1) ^ (ulong)-(long)(value & 1));
        }

        public long Sint64()
        {
            var value = Uvarint();
            return (long)((value >> 1) ^ (ulong)-(long)(value & 1));
        }

        public bool Bool()
        {
            return Uvarint() != 0;
        }

        public float Float()
        {
            EnsureAvailable(4);
            var bytes = Slice(_position, 4);
            _position += 4;
            if (!BitConverter.IsLittleEndian)
            {
                Array.Reverse(bytes);
            }
            return BitConverter.ToSingle(bytes, 0);
        }

        public double Double()
        {
            EnsureAvailable(8);
            var bytes = Slice(_position, 8);
            _position += 8;
            if (!BitConverter.IsLittleEndian)
            {
                Array.Reverse(bytes);
            }
            return BitConverter.ToDouble(bytes, 0);
        }

        public string String()
        {
            var bytes = Bytes();
            return Encoding.UTF8.GetString(bytes);
        }

        public byte[] Bytes()
        {
            var length = checked((int)Uvarint());
            EnsureAvailable(length);
            var bytes = Slice(_position, length);
            _position += length;
            return bytes;
        }

        public byte[] Remaining()
        {
            var bytes = Slice(_position, _buffer.Length - _position);
            _position = _buffer.Length;
            return bytes;
        }

        public void Skip(int wireType)
        {
            switch (wireType)
            {
                case 0:
                    Uvarint();
                    break;
                case 1:
                    EnsureAvailable(8);
                    _position += 8;
                    break;
                case 2:
                    var length = checked((int)Uvarint());
                    EnsureAvailable(length);
                    _position += length;
                    break;
                case 5:
                    EnsureAvailable(4);
                    _position += 4;
                    break;
                default:
                    throw new InvalidOperationException($"unsupported wire type {wireType}");
            }
        }

        private ulong Uvarint()
        {
            ulong result = 0;
            var shift = 0;
            while (_position < _buffer.Length && shift < 70)
            {
                var b = _buffer[_position++];
                result |= (ulong)(b & 0x7f) << shift;
                if (b < 0x80)
                {
                    return result;
                }
                shift += 7;
            }
            throw new InvalidOperationException("protobuf varint is truncated or too large");
        }

        private void EnsureAvailable(int length)
        {
            if (length < 0 || _buffer.Length - _position < length)
            {
                throw new InvalidOperationException("protobuf payload is truncated");
            }
        }

        private byte[] Slice(int offset, int length)
        {
            var bytes = new byte[length];
            Array.Copy(_buffer, offset, bytes, 0, length);
            return bytes;
        }
    }
}
