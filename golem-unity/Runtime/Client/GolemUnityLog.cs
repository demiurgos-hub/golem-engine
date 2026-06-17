using System;
using System.Net.Sockets;
using System.Net.WebSockets;
using System.Threading;
using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Writes Golem Unity runtime diagnostics to the Unity console.</summary>
    public static class GolemUnityLog
    {
        public static bool DebugEnabled { get; set; }

        public static void Info(string message)
        {
            if (DebugEnabled)
            {
                Debug.Log("golem-unity: " + message);
            }
        }

        public static void Warn(string message)
        {
            Debug.LogWarning("golem-unity: " + message);
        }

        public static void Error(string message)
        {
            Debug.LogError("golem-unity: " + message);
        }

        public static string FormatError(Exception ex)
        {
            if (ex == null)
            {
                return "error=";
            }

            var details = $"error_type={ex.GetType().Name} error={Quote(ex.Message)}";
            if (ex.InnerException != null)
            {
                details += $" inner_error_type={ex.InnerException.GetType().Name} inner_error={Quote(ex.InnerException.Message)}";
            }
            if (DebugEnabled)
            {
                details += $" debug_error={Quote(ex.ToString())}";
            }
            return details;
        }

        public static GolemDisconnectCategory ClassifyError(Exception ex)
        {
            if (ex == null)
            {
                return GolemDisconnectCategory.Unknown;
            }
            if (ex is UriFormatException)
            {
                return GolemDisconnectCategory.InvalidUrl;
            }
            if (ex is OperationCanceledException || ex is TimeoutException)
            {
                return GolemDisconnectCategory.Timeout;
            }
            if (ex is WebSocketException wsEx)
            {
                return ClassifySocketError(wsEx.InnerException) != GolemDisconnectCategory.Unknown
                    ? ClassifySocketError(wsEx.InnerException)
                    : GolemDisconnectCategory.TransportBackend;
            }
            var socketCategory = ClassifySocketError(ex);
            if (socketCategory != GolemDisconnectCategory.Unknown)
            {
                return socketCategory;
            }

            var text = (ex.Message + " " + ex.InnerException?.Message).ToLowerInvariant();
            if (text.Contains("certificate") || text.Contains("tls") || text.Contains("ssl") || text.Contains("authentication"))
            {
                return GolemDisconnectCategory.Tls;
            }
            if (text.Contains("refused") || text.Contains("actively refused") || text.Contains("unreachable"))
            {
                return GolemDisconnectCategory.ConnectionRefused;
            }
            if (text.Contains("protocol") || text.Contains("frame") || text.Contains("payload"))
            {
                return GolemDisconnectCategory.Protocol;
            }
            if (text.Contains("closed") || text.Contains("reset"))
            {
                return GolemDisconnectCategory.PeerClosed;
            }
            if (text.Contains("webtransport") || text.Contains("backend"))
            {
                return GolemDisconnectCategory.TransportBackend;
            }
            return GolemDisconnectCategory.Unknown;
        }

        public static string RedactUrl(string url)
        {
            if (string.IsNullOrEmpty(url))
            {
                return url;
            }
            if (!Uri.TryCreate(url, UriKind.Absolute, out var uri))
            {
                return url;
            }
            return uri.GetLeftPart(UriPartial.Path);
        }

        public static void LogDisconnect(string transport, GolemDisconnectInfo info)
        {
            var transportName = !string.IsNullOrEmpty(info.Transport) ? info.Transport : transport;
            var parts = $"disconnected transport={transportName} was_clean={BoolString(info.WasClean)}";
            if (!string.IsNullOrEmpty(info.Url))
            {
                parts += $" url={RedactUrl(info.Url)}";
            }
            if (info.Phase != GolemDisconnectPhase.Unknown)
            {
                parts += $" phase={PhaseName(info.Phase)}";
            }
            if (info.Category != GolemDisconnectCategory.Unknown)
            {
                parts += $" category={CategoryName(info.Category)}";
            }
            if (info.Code.HasValue)
            {
                parts += $" code={info.Code.Value}";
            }
            if (!string.IsNullOrEmpty(info.Reason))
            {
                parts += $" reason={Quote(info.Reason)}";
            }

            if (info.WasClean && info.Error == null)
            {
                Warn(parts);
                return;
            }

            Error(parts + " " + FormatError(info.Error));
        }

        public static string PhaseName(GolemDisconnectPhase phase)
        {
            switch (phase)
            {
                case GolemDisconnectPhase.Connect:
                    return "connect";
                case GolemDisconnectPhase.OpenStream:
                    return "open_stream";
                case GolemDisconnectPhase.StreamHeaderWrite:
                    return "stream_header_write";
                case GolemDisconnectPhase.Receive:
                    return "receive";
                case GolemDisconnectPhase.Send:
                    return "send";
                case GolemDisconnectPhase.Close:
                    return "close";
                default:
                    return "unknown";
            }
        }

        public static string CategoryName(GolemDisconnectCategory category)
        {
            switch (category)
            {
                case GolemDisconnectCategory.InvalidUrl:
                    return "invalid_url";
                case GolemDisconnectCategory.Timeout:
                    return "timeout";
                case GolemDisconnectCategory.Tls:
                    return "tls";
                case GolemDisconnectCategory.ConnectionRefused:
                    return "connection_refused";
                case GolemDisconnectCategory.TransportBackend:
                    return "transport_backend";
                case GolemDisconnectCategory.Protocol:
                    return "protocol";
                case GolemDisconnectCategory.PeerClosed:
                    return "peer_closed";
                case GolemDisconnectCategory.UserClosed:
                    return "user_closed";
                default:
                    return "unknown";
            }
        }

        private static GolemDisconnectCategory ClassifySocketError(Exception ex)
        {
            if (ex is SocketException socketEx)
            {
                return socketEx.SocketErrorCode == SocketError.ConnectionRefused
                    ? GolemDisconnectCategory.ConnectionRefused
                    : GolemDisconnectCategory.TransportBackend;
            }
            return GolemDisconnectCategory.Unknown;
        }

        private static string BoolString(bool value)
        {
            return value ? "true" : "false";
        }

        private static string Quote(string value)
        {
            return "\"" + (value ?? string.Empty).Replace("\\", "\\\\").Replace("\"", "\\\"").Replace("\r", "\\r").Replace("\n", "\\n") + "\"";
        }
    }
}
