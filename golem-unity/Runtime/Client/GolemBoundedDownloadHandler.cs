using System;
using System.Text;
using UnityEngine.Networking;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Accumulates HTTP response bytes with a hard memory cap.
    /// Used by realtime-config fetch so success and error bodies are never fully buffered unbounded.
    /// </summary>
    public sealed class GolemBoundedDownloadHandler : DownloadHandlerScript
    {
        private readonly byte[] _buffer;
        private int _length;
        private bool _overflow;

        public GolemBoundedDownloadHandler(int maxBytes)
        {
            if (maxBytes <= 0)
            {
                throw new ArgumentOutOfRangeException(nameof(maxBytes));
            }
            MaxBytes = maxBytes;
            _buffer = new byte[maxBytes];
        }

        public int MaxBytes { get; }
        public bool Overflowed => _overflow;
        public int Length => _length;

        public string GetTextUtf8()
        {
            return _length == 0 ? string.Empty : Encoding.UTF8.GetString(_buffer, 0, _length);
        }

        public string GetErrorSnippet(int maxChars)
        {
            var text = GetTextUtf8();
            if (string.IsNullOrEmpty(text) || text.Length <= maxChars)
            {
                return text;
            }
            return text.Substring(0, maxChars);
        }

        /// <summary>
        /// Helper-level accumulate used by tests and DownloadHandlerScript.
        /// Returns false when the next chunk would exceed the cap (partial chunk is not stored).
        /// </summary>
        public static bool TryAccumulate(byte[] destination, ref int length, byte[] data, int dataLength, int maxBytes)
        {
            if (destination == null)
            {
                throw new ArgumentNullException(nameof(destination));
            }
            if (data == null || dataLength <= 0)
            {
                return true;
            }
            if (length + dataLength > maxBytes)
            {
                return false;
            }
            Buffer.BlockCopy(data, 0, destination, length, dataLength);
            length += dataLength;
            return true;
        }

        protected override bool ReceiveData(byte[] data, int dataLength)
        {
            if (_overflow)
            {
                return false;
            }
            if (!TryAccumulate(_buffer, ref _length, data, dataLength, MaxBytes))
            {
                _overflow = true;
                return false;
            }
            return true;
        }

        protected override byte[] GetData()
        {
            if (_length == 0)
            {
                return Array.Empty<byte>();
            }
            var copy = new byte[_length];
            Buffer.BlockCopy(_buffer, 0, copy, 0, _length);
            return copy;
        }

        protected override string GetText()
        {
            return GetTextUtf8();
        }
    }
}
