using System;

namespace GolemEngine.Unity
{
    /// <summary>Encodes and decodes Golem's length-prefixed reliable stream frames.</summary>
    public static class GolemReliableFrameCodec
    {
        public const int HeaderBytes = 4;
        private static readonly byte[] ClientCloseControlFrame = { 0x00, (byte)'O', (byte)'G', (byte)'S', 0x01 };

        public static byte[] ClientCloseControlPayload()
        {
            var payload = new byte[ClientCloseControlFrame.Length];
            Array.Copy(ClientCloseControlFrame, payload, payload.Length);
            return payload;
        }

        public static byte[] Encode(byte[] payload, int maxPayloadBytes = GameClient.MaxReliableMessageBytes)
        {
            if (payload == null)
            {
                throw new ArgumentNullException(nameof(payload));
            }
            ValidatePayloadLength(payload.Length, maxPayloadBytes);

            var frame = new byte[HeaderBytes + payload.Length];
            WriteLength(frame, payload.Length);
            Array.Copy(payload, 0, frame, HeaderBytes, payload.Length);
            return frame;
        }

        public static int DecodeLength(byte[] header, int maxPayloadBytes = GameClient.MaxReliableMessageBytes)
        {
            if (header == null)
            {
                throw new ArgumentNullException(nameof(header));
            }
            if (header.Length < HeaderBytes)
            {
                throw new ArgumentException("golem-unity: reliable frame header must contain 4 bytes", nameof(header));
            }

            var length =
                (header[0] << 24) |
                (header[1] << 16) |
                (header[2] << 8) |
                header[3];
            ValidatePayloadLength(length, maxPayloadBytes);
            return length;
        }

        private static void WriteLength(byte[] frame, int length)
        {
            frame[0] = (byte)((length >> 24) & 0xff);
            frame[1] = (byte)((length >> 16) & 0xff);
            frame[2] = (byte)((length >> 8) & 0xff);
            frame[3] = (byte)(length & 0xff);
        }

        private static void ValidatePayloadLength(int length, int maxPayloadBytes)
        {
            if (length < 0 || length > maxPayloadBytes)
            {
                throw new InvalidOperationException($"golem-unity: reliable frame size {length} exceeds max reliable message {maxPayloadBytes}");
            }
        }
    }
}
