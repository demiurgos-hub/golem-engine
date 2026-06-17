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
            var length = GolemReliableFrameCodec.DecodeLength(new byte[] { 0, 0, 0x7d, 0 });

            Assert.That(length, Is.EqualTo(32000));
        }

        [Test]
        public void DecodeLengthRejectsOversizedFrames()
        {
            Assert.Throws<InvalidOperationException>(() =>
                GolemReliableFrameCodec.DecodeLength(new byte[] { 0, 0, 0x7d, 1 }));
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
