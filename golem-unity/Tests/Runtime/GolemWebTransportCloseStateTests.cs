using System;
using System.Collections.Generic;
using System.IO;
using System.Reflection;
using System.Threading.Tasks;
using NUnit.Framework;

namespace GolemEngine.Unity.Tests
{
    public sealed class GolemWebTransportCloseStateTests
    {
        [TestCase("ticket_expired", "ticket_expired")]
        [TestCase("active_character_exists", "active_character_exists")]
        [TestCase("token=secret", "transport_operation_failed")]
        [TestCase("ticket secret_value", "transport_operation_failed")]
        [TestCase("ticket_expired?token=secret", "transport_operation_failed")]
        public void ClientSanitizationRetainsCodesAndRedactsCredentialDiagnostics(string reason, string expected)
        {
            var method = typeof(GameClient).GetMethod("SanitizeDisconnectInfo", BindingFlags.Static | BindingFlags.NonPublic);
            var raw = GolemWebTransportCloseState.PeerClose(uint.MaxValue, reason, "https://host/wt?token=secret");
            var safe = (GolemDisconnectInfo)method.Invoke(null, new object[] { raw, new GolemConnectOptions("https://host/wt", "webtransport") });
            Assert.That(safe.WebTransportErrorCode, Is.EqualTo(uint.MaxValue));
            Assert.That(safe.Code, Is.Null);
            Assert.That(safe.Reason, Is.EqualTo(expected));
            Assert.That(safe.Url, Is.EqualTo("https://host/wt"));
        }

        [TestCase(0u, "")]
        [TestCase(0u, "active_character_exists")]
        [TestCase(19u, "ticket_expired")]
        [TestCase(uint.MaxValue, "reconnect_not_found ☃")]
        public async Task PeerMetadataIsExactAndPublishedOnce(uint code, string reason)
        {
            var peer = new TaskCompletionSource<GolemDisconnectInfo>();
            var results = new List<GolemDisconnectInfo>();
            var state = new GolemWebTransportCloseState(peer.Task, results.Add, "https://host/wt");
            var observing = state.ObserveAsync();
            peer.SetResult(GolemWebTransportCloseState.PeerClose(code, reason, "https://host/wt"));
            await observing;
            await state.FailAsync(new IOException("closed"), GolemDisconnectPhase.Receive, true);
            state.CloseLocally();
            Assert.That(results.Count, Is.EqualTo(1));
            Assert.That(results[0].Reason, Is.EqualTo(reason));
            Assert.That(results[0].WebTransportErrorCode, Is.EqualTo(code));
            Assert.That(results[0].Code, Is.EqualTo(code <= int.MaxValue ? (int?)code : null));
            Assert.That(results[0].WasClean, Is.True);
            Assert.That(results[0].Category, Is.EqualTo(GolemDisconnectCategory.PeerClosed));
        }

        [Test]
        public async Task StreamEofWaitsForIndependentConnectCapsule()
        {
            var peer = new TaskCompletionSource<GolemDisconnectInfo>();
            var results = new List<GolemDisconnectInfo>();
            var state = new GolemWebTransportCloseState(peer.Task, results.Add, "https://host/wt");
            var failure = state.FailAsync(new EndOfStreamException(), GolemDisconnectPhase.Receive, true);
            Assert.That(failure.IsCompleted, Is.False);
            Assert.That(results, Is.Empty);
            peer.SetResult(GolemWebTransportCloseState.PeerClose(0, "admission_cancelled", "https://host/wt"));
            await failure;
            Assert.That(results[0].Reason, Is.EqualTo("admission_cancelled"));
        }

        [Test]
        public async Task StreamOnlyClosureHasBoundedFallbackAndDoesNotPublishLateCapsule()
        {
            var peer = new TaskCompletionSource<GolemDisconnectInfo>();
            var results = new List<GolemDisconnectInfo>();
            var state = new GolemWebTransportCloseState(peer.Task, results.Add, "https://host/wt", 0);
            var error = new EndOfStreamException("closed");
            await state.FailAsync(error, GolemDisconnectPhase.Receive, true);
            Assert.That(results.Count, Is.EqualTo(1));
            Assert.That(results[0].Error, Is.SameAs(error));
            Assert.That(results[0].Reason, Is.Null);
            peer.SetResult(GolemWebTransportCloseState.PeerClose(0, "late", "https://host/wt"));
            await state.ObserveAsync();
            Assert.That(results.Count, Is.EqualTo(1));
        }

        [Test]
        public async Task ProtocolFailureDoesNotWaitForSessionClosure()
        {
            var peer = new TaskCompletionSource<GolemDisconnectInfo>();
            var results = new List<GolemDisconnectInfo>();
            var state = new GolemWebTransportCloseState(peer.Task, results.Add, "https://host/wt");
            var error = new InvalidOperationException("invalid frame length");
            await state.FailAsync(error, GolemDisconnectPhase.Receive, false);
            Assert.That(results.Count, Is.EqualTo(1));
            Assert.That(results[0].Category, Is.EqualTo(GolemDisconnectCategory.Protocol));
        }

        [Test]
        public async Task ConnectionLossIsNotInventedAsAnApplicationClose()
        {
            var peer = Task.FromException<GolemDisconnectInfo>(new IOException("connection closed"));
            var results = new List<GolemDisconnectInfo>();
            var state = new GolemWebTransportCloseState(peer, results.Add, "https://host/wt");
            await state.ObserveAsync();
            Assert.That(results.Count, Is.EqualTo(1));
            Assert.That(results[0].WasClean, Is.False);
            Assert.That(results[0].Reason, Is.Null);
            Assert.That(results[0].WebTransportErrorCode, Is.Null);
        }

        [Test]
        public async Task LocalCloseCannotBeOverwrittenByCancellation()
        {
            var peer = new TaskCompletionSource<GolemDisconnectInfo>();
            var results = new List<GolemDisconnectInfo>();
            var state = new GolemWebTransportCloseState(peer.Task, results.Add, "https://host/wt");
            state.CloseLocally();
            peer.SetCanceled();
            await state.ObserveAsync();
            Assert.That(results.Count, Is.EqualTo(1));
            Assert.That(results[0].Category, Is.EqualTo(GolemDisconnectCategory.UserClosed));
            Assert.That(results[0].Reason, Is.Null);
        }
    }
}
