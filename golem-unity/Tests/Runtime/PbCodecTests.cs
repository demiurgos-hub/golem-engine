using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed class PbCodecTests
    {
        [Test]
        public void RoundTripsScalarValues()
        {
            var bytes = new PbWriter()
                .Tag(1, 0).Int64(-42)
                .Tag(2, 0).Uint64(99)
                .Tag(3, 5).Float(1.5f)
                .Tag(4, 1).Double(2.5)
                .Tag(5, 0).Bool(true)
                .Tag(6, 2).String("hello")
                .Tag(7, 2).Bytes(new byte[] { 1, 2, 3 })
                .Finish();

            var r = new PbReader(bytes);
            Assert.That(r.Tag().Field, Is.EqualTo(1));
            Assert.That(r.Int64(), Is.EqualTo(-42));
            Assert.That(r.Tag().Field, Is.EqualTo(2));
            Assert.That(r.Uint64(), Is.EqualTo(99));
            Assert.That(r.Tag().Field, Is.EqualTo(3));
            Assert.That(r.Float(), Is.EqualTo(1.5f));
            Assert.That(r.Tag().Field, Is.EqualTo(4));
            Assert.That(r.Double(), Is.EqualTo(2.5));
            Assert.That(r.Tag().Field, Is.EqualTo(5));
            Assert.That(r.Bool(), Is.True);
            Assert.That(r.Tag().Field, Is.EqualTo(6));
            Assert.That(r.String(), Is.EqualTo("hello"));
            Assert.That(r.Tag().Field, Is.EqualTo(7));
            Assert.That(r.Bytes(), Is.EqualTo(new byte[] { 1, 2, 3 }));
            Assert.That(r.Done, Is.True);
        }
    }
}
