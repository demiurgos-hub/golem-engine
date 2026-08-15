using System;
using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed class GolemReliableFrameCodecTests
    {
        [Test]
        public void EncodePrefixesPayloadWithBigEndianLength()
        {
            var payload = new byte[] { 1, 2, 3 };

            var frame = GolemReliableFrameCodec.Encode(payload);

            Assert.That(frame, Is.EqualTo(new byte[] { 0, 0, 0, 3, 1, 2, 3 }));
        }

        [Test]
        public void DecodeLengthReadsBigEndianLength()
        {
            var length = GolemReliableFrameCodec.DecodeLength(new byte[] { 0, 4, 0, 0 });

            Assert.That(length, Is.EqualTo(256 * 1024));
        }

        [Test]
        public void DecodeLengthRejectsOversizedFrames()
        {
            Assert.Throws<InvalidOperationException>(() =>
                GolemReliableFrameCodec.DecodeLength(new byte[] { 0, 4, 0, 1 }));
        }

        [Test]
        public void EncodeAcceptsLargeBoundedSnapshot()
        {
            var frame = GolemReliableFrameCodec.Encode(new byte[150000]);

            Assert.That(frame.Length, Is.EqualTo(150004));
            Assert.That(GolemReliableFrameCodec.DecodeLength(frame), Is.EqualTo(150000));
        }

        [Test]
        public void ClientCloseControlPayloadMatchesServerControlFrame()
        {
            Assert.That(
                GolemReliableFrameCodec.ClientCloseControlPayload(),
                Is.EqualTo(new byte[] { 0x00, (byte)'O', (byte)'G', (byte)'S', 0x01 }));
        }
    }
}
