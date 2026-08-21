using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Threading;
using System.Threading.Tasks;

namespace GolemEngine.Unity
{
    /// <summary>Resolves fresh per-dial connection options for a credential-free endpoint candidate.</summary>
    public delegate Task<GolemConnectOptions> GolemConnectOptionsResolver(
        GolemConnectOptions baseOptions,
        CancellationToken cancellationToken);

    /// <summary>Capability checks used by generated clients with the built-in transports.</summary>
    public static class GolemTransportCapabilities
    {
        /// <summary>Reports whether the built-in Unity transport can attempt these options.</summary>
        public static bool BuiltInTransportSupported(GolemConnectOptions options)
        {
            if (options == null)
            {
                return false;
            }
            switch (options.Transport)
            {
                case GolemConnectOptions.TransportWebSocket:
#if UNITY_WEBGL
                    return false;
#else
                    return true;
#endif
                case GolemConnectOptions.TransportWebTransport:
#if UNITY_EDITOR_WIN || UNITY_STANDALONE_WIN
                    return RuntimeInformation.ProcessArchitecture == Architecture.X64 &&
                           (options.ServerCertificateHashes == null || options.ServerCertificateHashes.Count == 0);
#else
                    return false;
#endif
                default:
                    return false;
            }
        }
    }

    /// <summary>Certificate hash entry from realtime config or ConnectOptions (hex value on the wire).</summary>
    public readonly struct GolemCertificateHash
    {
        public GolemCertificateHash(string algorithm, string value)
        {
            Algorithm = algorithm ?? throw new ArgumentNullException(nameof(algorithm));
            Value = value ?? throw new ArgumentNullException(nameof(value));
        }

        public string Algorithm { get; }
        public string Value { get; }
    }

    /// <summary>Transport-aware connection options for GameClient.Connect.</summary>
    public sealed class GolemConnectOptions
    {
        public const string TransportWebSocket = "websocket";
        public const string TransportWebTransport = "webtransport";

        public GolemConnectOptions(
            string url,
            string transport = null,
            IReadOnlyList<GolemCertificateHash> serverCertificateHashes = null,
            int eventualAckIntervalMs = 0)
        {
            Url = url ?? throw new ArgumentNullException(nameof(url));
            Transport = transport ?? string.Empty;
            ServerCertificateHashes = CloneHashes(serverCertificateHashes);
            EventualAckIntervalMs = eventualAckIntervalMs;
        }

        /// <summary>Transport kind: websocket, webtransport, or empty for Connect(string) factory defaults.</summary>
        public string Transport { get; }

        /// <summary>WebSocket or WebTransport endpoint URL.</summary>
        public string Url { get; }

        /// <summary>Browser-style certificate hashes from realtime config (hex values).</summary>
        public IReadOnlyList<GolemCertificateHash> ServerCertificateHashes { get; }

        /// <summary>Eventual-state ACK coalesce interval in milliseconds; 0 uses the transport default.</summary>
        public int EventualAckIntervalMs { get; }

        /// <summary>
        /// Resolves the transport ACK interval: absent/zero keeps the WebTransport default (1 ms coalesce tick);
        /// positive values are used as-is.
        /// </summary>
        public static int EffectiveEventualAckIntervalMs(int eventualAckIntervalMs)
        {
            return eventualAckIntervalMs > 0 ? eventualAckIntervalMs : 1;
        }

        private static IReadOnlyList<GolemCertificateHash> CloneHashes(IReadOnlyList<GolemCertificateHash> hashes)
        {
            if (hashes == null || hashes.Count == 0)
            {
                return Array.Empty<GolemCertificateHash>();
            }
            var copy = new GolemCertificateHash[hashes.Count];
            for (var i = 0; i < hashes.Count; i++)
            {
                copy[i] = hashes[i];
            }
            return copy;
        }
    }
}
