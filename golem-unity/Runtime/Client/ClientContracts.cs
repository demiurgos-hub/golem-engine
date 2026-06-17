using System;

namespace GolemEngine.Unity
{
    /// <summary>Connection or IO phase associated with a transport disconnect.</summary>
    public enum GolemDisconnectPhase
    {
        Unknown,
        Connect,
        OpenStream,
        StreamHeaderWrite,
        Receive,
        Send,
        Close
    }

    /// <summary>Best-effort category for transport disconnect diagnostics.</summary>
    public enum GolemDisconnectCategory
    {
        Unknown,
        InvalidUrl,
        Timeout,
        Tls,
        ConnectionRefused,
        TransportBackend,
        Protocol,
        PeerClosed,
        UserClosed
    }

    /// <summary>Transport-neutral connection close details.</summary>
    public readonly struct GolemDisconnectInfo
    {
        public GolemDisconnectInfo(
            bool wasClean,
            int? code = null,
            string reason = null,
            Exception error = null,
            string transport = null,
            string url = null,
            GolemDisconnectPhase phase = GolemDisconnectPhase.Unknown,
            GolemDisconnectCategory category = GolemDisconnectCategory.Unknown)
        {
            WasClean = wasClean;
            Code = code;
            Reason = reason;
            Error = error;
            Transport = transport;
            Url = url;
            Phase = phase;
            Category = category;
        }

        public bool WasClean { get; }
        public int? Code { get; }
        public string Reason { get; }
        public Exception Error { get; }
        public string Transport { get; }
        public string Url { get; }
        public GolemDisconnectPhase Phase { get; }
        public GolemDisconnectCategory Category { get; }
    }

    /// <summary>Transport abstraction used by GameClient to send reliable Golem packets and optional datagrams.</summary>
    public interface IGolemTransport
    {
        bool Connected { get; }
        int MaxMessageBytes { get; }
        int MaxDatagramBytes { get; }
        event Action ConnectedEvent;
        event Action<byte[]> MessageEvent;
        event Action<byte[]> UnreliableStateMessageEvent;
        event Action<byte[]> ReliableOrderedMessageEvent;
        event Action<byte[]> EventualStateMessageEvent;
        event Action<GolemDisconnectInfo> DisconnectedEvent;
        void Connect(string url);
        void Send(byte[] bytes);
        void SendUnreliable(byte[] bytes);
        void SendReliableUnordered(byte[] bytes);
        void SendReliableOrdered(byte[] bytes);
        void Close();
    }

    /// <summary>Contract implemented by generated entity managers.</summary>
    public interface IEntityManager
    {
        void ApplyUpdate(object update);
        object Get(long entityId);
    }

    /// <summary>Optional contract implemented by generated managers that can apply compact datagram state records.</summary>
    public interface ICompactEntityManager
    {
        void ApplyCompactUpdate(byte[] frame);
    }

    /// <summary>Contract implemented by generated world managers.</summary>
    public interface IWorldManager
    {
        void ApplyUpdate(object update);
    }

    /// <summary>Contract implemented by generated event managers.</summary>
    public interface IEventManager
    {
        void ApplyRaw(byte[] bytes);
    }

    /// <summary>Optional lifecycle hooks generated entity subclasses can implement.</summary>
    public interface IEntityLifecycle
    {
        void OnSpawn();
        void OnRemove();
    }
}
