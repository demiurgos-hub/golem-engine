using System;
using System.Collections.Generic;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using UnityEngine.Networking;

namespace GolemEngine.Unity
{
    /// <summary>Client-side realtime config JSON served by golem.Server.RealtimeConfigHandler.</summary>
    public sealed class GolemRealtimeConfig
    {
        public GolemRealtimeConfig(
            string transport,
            string url,
            IReadOnlyList<GolemCertificateHash> serverCertificateHashes = null,
            int? eventualAckIntervalMs = null)
        {
            Transport = transport ?? throw new ArgumentNullException(nameof(transport));
            Url = url ?? throw new ArgumentNullException(nameof(url));
            ServerCertificateHashes = serverCertificateHashes ?? Array.Empty<GolemCertificateHash>();
            EventualAckIntervalMs = eventualAckIntervalMs;
        }

        public string Transport { get; }
        public string Url { get; }
        public IReadOnlyList<GolemCertificateHash> ServerCertificateHashes { get; }
        public int? EventualAckIntervalMs { get; }
    }

    /// <summary>HTTP response returned by an injectable realtime-config fetch function.</summary>
    public readonly struct GolemRealtimeHttpResponse
    {
        public GolemRealtimeHttpResponse(long statusCode, string body)
        {
            StatusCode = statusCode;
            Body = body ?? string.Empty;
        }

        public long StatusCode { get; }
        public string Body { get; }
    }

    /// <summary>
    /// Fetches and converts golem realtime-config JSON into <see cref="GolemConnectOptions"/>.
    /// Query helpers append auth params (token, char_id) without logging token values.
    /// </summary>
    public static class GolemRealtimeBootstrap
    {
        public const int MaxErrorBodyBytes = 512;
        public const int MaxConfigBodyBytes = 64 * 1024;
        private const string CertificateHashSHA256 = "sha-256";

        /// <summary>Loads and decodes a realtime config JSON endpoint.</summary>
        public static async Task<GolemRealtimeConfig> FetchRealtimeConfigAsync(
            string endpoint,
            CancellationToken cancellationToken = default,
            Func<string, CancellationToken, Task<GolemRealtimeHttpResponse>> fetch = null)
        {
            if (string.IsNullOrWhiteSpace(endpoint))
            {
                throw new ArgumentException("golem-unity: realtime config endpoint is required", nameof(endpoint));
            }

            var response = fetch != null
                ? await fetch(endpoint, cancellationToken).ConfigureAwait(false)
                : await DefaultFetchAsync(endpoint, cancellationToken).ConfigureAwait(false);

            if (response.StatusCode < 200 || response.StatusCode >= 300)
            {
                var snippet = Truncate(response.Body, MaxErrorBodyBytes);
                if (snippet.Length > 0)
                {
                    throw new InvalidOperationException(
                        $"golem-unity: fetching realtime config: status {response.StatusCode} body={Quote(snippet)}");
                }
                throw new InvalidOperationException(
                    $"golem-unity: fetching realtime config: status {response.StatusCode}");
            }

            if (response.Body != null && Encoding.UTF8.GetByteCount(response.Body) > MaxConfigBodyBytes)
            {
                throw new InvalidOperationException(
                    $"golem-unity: realtime config response exceeds {MaxConfigBodyBytes} bytes");
            }

            try
            {
                return ParseRealtimeConfig(response.Body);
            }
            catch (InvalidOperationException)
            {
                throw;
            }
            catch (Exception ex)
            {
                throw new InvalidOperationException("golem-unity: decoding realtime config: " + ex.Message, ex);
            }
        }

        /// <summary>Parses realtime config JSON without performing an HTTP fetch.</summary>
        public static GolemRealtimeConfig ParseRealtimeConfig(string json)
        {
            if (json == null)
            {
                throw new ArgumentNullException(nameof(json));
            }

            var transport = RequireStringField(json, "transport");
            var url = RequireStringField(json, "url");
            if (string.IsNullOrWhiteSpace(transport))
            {
                throw new InvalidOperationException("golem-unity: realtime config transport is required");
            }
            if (string.IsNullOrWhiteSpace(url))
            {
                throw new InvalidOperationException("golem-unity: realtime config url is required");
            }
            transport = transport.Trim();
            if (transport != GolemConnectOptions.TransportWebSocket &&
                transport != GolemConnectOptions.TransportWebTransport)
            {
                throw new InvalidOperationException($"golem-unity: unsupported transport \"{transport}\"");
            }

            int? ack = null;
            if (TryReadEventualAckIntervalMs(json, out var ackValue))
            {
                ack = ackValue;
            }

            var hashes = ParseCertificateHashes(json);
            return new GolemRealtimeConfig(transport, url, hashes, ack);
        }

        /// <summary>Converts realtime config into connect options, appending query parameters.</summary>
        public static GolemConnectOptions ConnectOptionsFromRealtimeConfig(
            GolemRealtimeConfig config,
            IEnumerable<KeyValuePair<string, string>> query = null)
        {
            if (config == null)
            {
                throw new ArgumentNullException(nameof(config));
            }
            if (config.Transport != GolemConnectOptions.TransportWebSocket &&
                config.Transport != GolemConnectOptions.TransportWebTransport)
            {
                throw new InvalidOperationException($"golem-unity: unsupported transport \"{config.Transport}\"");
            }
            if (string.IsNullOrWhiteSpace(config.Url))
            {
                throw new InvalidOperationException("golem-unity: realtime config url is required");
            }

            var url = AppendQuery(config.Url, query);
            var ack = config.EventualAckIntervalMs ?? 0;
            return new GolemConnectOptions(url, config.Transport, config.ServerCertificateHashes, ack);
        }

        /// <summary>Appends one query parameter to a transport URL, preserving existing values.</summary>
        public static string WithQueryParam(string url, string key, string value)
        {
            return AppendQuery(url, new[] { new KeyValuePair<string, string>(key, value) });
        }

        /// <summary>Appends query parameters to a transport URL, preserving existing values and duplicates.</summary>
        public static string WithQuery(string url, IEnumerable<KeyValuePair<string, string>> values)
        {
            return AppendQuery(url, values);
        }

        /// <summary>
        /// Rejects browser-style certificate hashes that the native WebTransport client cannot apply.
        /// Call before constructing WebTransport so hashes are never silently ignored.
        /// </summary>
        public static void EnsureNativeWebTransportSupportsCertificateHashes(
            IReadOnlyList<GolemCertificateHash> hashes)
        {
            if (hashes != null && hashes.Count > 0)
            {
                throw new NotSupportedException(
                    "golem-unity: serverCertificateHashes are not supported by the native WebTransport client; use a trusted certificate authority or configure WebTransportClientOptions.AllowUntrustedCertificates for local development");
            }
        }

        private static async Task<GolemRealtimeHttpResponse> DefaultFetchAsync(
            string endpoint,
            CancellationToken cancellationToken)
        {
            // CancellationToken is polled between await Task.Yield() resumes on this worker.
            // UnityWebRequest is not Abort()'d here: Abort is a Unity main-thread API concern, and
            // canceling the await may leave an in-flight request completing in the background.
            using (var request = new UnityWebRequest(endpoint, UnityWebRequest.kHttpVerbGET))
            {
                var handler = new GolemBoundedDownloadHandler(MaxConfigBodyBytes);
                request.downloadHandler = handler;
                var operation = request.SendWebRequest();
                while (!operation.isDone)
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    await Task.Yield();
                }

                if (!string.IsNullOrEmpty(request.error) && request.responseCode == 0)
                {
                    throw new InvalidOperationException(
                        "golem-unity: fetching realtime config: " + request.error);
                }

                if (handler.Overflowed)
                {
                    throw new InvalidOperationException(
                        $"golem-unity: realtime config response exceeds {MaxConfigBodyBytes} bytes");
                }

                var body = request.responseCode >= 200 && request.responseCode < 300
                    ? handler.GetTextUtf8()
                    : handler.GetErrorSnippet(MaxErrorBodyBytes);
                return new GolemRealtimeHttpResponse(request.responseCode, body);
            }
        }

        /// <summary>
        /// Appends query parameters to an absolute URL without reconstructing via UriBuilder.
        /// Preserves the original prefix, existing query bytes (+/percent escapes, default ports),
        /// duplicate keys, and fragment; newly encoded pairs are inserted immediately before the fragment.
        /// </summary>
        public static string AppendQuery(string url, IEnumerable<KeyValuePair<string, string>> values)
        {
            if (string.IsNullOrEmpty(url))
            {
                throw new ArgumentException("golem-unity: url is required", nameof(url));
            }
            if (values == null)
            {
                return url;
            }

            var additions = new List<KeyValuePair<string, string>>();
            foreach (var pair in values)
            {
                if (string.IsNullOrEmpty(pair.Key))
                {
                    throw new ArgumentException("golem-unity: query parameter key cannot be empty");
                }
                additions.Add(new KeyValuePair<string, string>(pair.Key, pair.Value ?? string.Empty));
            }
            if (additions.Count == 0)
            {
                return url;
            }

            if (!Uri.TryCreate(url, UriKind.Absolute, out _))
            {
                throw new InvalidOperationException("golem-unity: parsing realtime transport URL: invalid absolute URL");
            }

            var fragmentIndex = url.IndexOf('#');
            var beforeFragment = fragmentIndex >= 0 ? url.Substring(0, fragmentIndex) : url;
            var fragment = fragmentIndex >= 0 ? url.Substring(fragmentIndex) : string.Empty;
            var sb = new StringBuilder(beforeFragment.Length + 64);
            sb.Append(beforeFragment);
            var hasQuery = beforeFragment.IndexOf('?') >= 0;
            foreach (var pair in additions)
            {
                sb.Append(hasQuery ? '&' : '?');
                hasQuery = true;
                sb.Append(Uri.EscapeDataString(pair.Key));
                sb.Append('=');
                sb.Append(Uri.EscapeDataString(pair.Value ?? string.Empty));
            }
            sb.Append(fragment);
            return sb.ToString();
        }

        private static List<GolemCertificateHash> ParseCertificateHashes(string json)
        {
            var hashes = new List<GolemCertificateHash>();
            if (!TryFindObjectArray(json, "serverCertificateHashes", out var arrayJson))
            {
                return hashes;
            }

            var index = 0;
            foreach (var objectJson in IterateObjects(arrayJson))
            {
                var algorithm = RequireStringField(objectJson, "algorithm");
                var value = RequireStringField(objectJson, "value");
                hashes.Add(DecodeCertificateHash(algorithm, value, index));
                index++;
            }
            return hashes;
        }

        private static GolemCertificateHash DecodeCertificateHash(string algorithm, string value, int index)
        {
            var normalized = (algorithm ?? string.Empty).Trim().ToLowerInvariant();
            if (normalized != CertificateHashSHA256)
            {
                throw new InvalidOperationException(
                    $"golem-unity: unsupported certificate hash algorithm \"{algorithm}\"");
            }
            var bytes = HexToBytes(value);
            if (bytes.Length != 32)
            {
                throw new InvalidOperationException(
                    $"golem-unity: sha-256 certificate hash length {bytes.Length}, want 32");
            }
            return new GolemCertificateHash(normalized, value);
        }

        private static byte[] HexToBytes(string hex)
        {
            if (hex == null)
            {
                throw new InvalidOperationException("golem-unity: certificate hash hex string is required");
            }
            var normalized = new StringBuilder(hex.Length);
            foreach (var ch in hex)
            {
                if (!char.IsWhiteSpace(ch))
                {
                    normalized.Append(ch);
                }
            }
            if (normalized.Length % 2 != 0)
            {
                throw new InvalidOperationException("golem-unity: certificate hash hex string must have even length");
            }
            for (var i = 0; i < normalized.Length; i++)
            {
                if (!IsHex(normalized[i]))
                {
                    throw new InvalidOperationException("golem-unity: certificate hash hex string contains non-hex characters");
                }
            }
            var bytes = new byte[normalized.Length / 2];
            for (var i = 0; i < bytes.Length; i++)
            {
                bytes[i] = Convert.ToByte(normalized.ToString(i * 2, 2), 16);
            }
            return bytes;
        }

        private static bool IsHex(char ch)
        {
            return (ch >= '0' && ch <= '9') ||
                   (ch >= 'a' && ch <= 'f') ||
                   (ch >= 'A' && ch <= 'F');
        }

        private static string RequireStringField(string json, string field)
        {
            if (!TryReadStringField(json, field, out var value))
            {
                throw new InvalidOperationException($"golem-unity: realtime config missing string field \"{field}\"");
            }
            return value;
        }

        private static bool TryReadStringField(string json, string field, out string value)
        {
            value = null;
            var key = "\"" + field + "\"";
            var keyIndex = IndexOfJsonKey(json, key);
            if (keyIndex < 0)
            {
                return false;
            }
            var i = keyIndex + key.Length;
            i = SkipWs(json, i);
            if (i >= json.Length || json[i] != ':')
            {
                return false;
            }
            i = SkipWs(json, i + 1);
            if (i >= json.Length || json[i] != '"')
            {
                return false;
            }
            return TryReadJsonString(json, i, out value, out _);
        }

        private static bool TryReadEventualAckIntervalMs(string json, out int value)
        {
            value = 0;
            var key = "\"eventualAckIntervalMs\"";
            var keyIndex = IndexOfJsonKey(json, key);
            if (keyIndex < 0)
            {
                return false;
            }
            var i = keyIndex + key.Length;
            i = SkipWs(json, i);
            if (i >= json.Length || json[i] != ':')
            {
                throw new InvalidOperationException("golem-unity: eventualAckIntervalMs must be an integer");
            }
            i = SkipWs(json, i + 1);
            var start = i;
            if (i < json.Length && (json[i] == '-' || json[i] == '+'))
            {
                i++;
            }
            var digits = 0;
            while (i < json.Length && char.IsDigit(json[i]))
            {
                i++;
                digits++;
            }
            if (digits == 0)
            {
                throw new InvalidOperationException("golem-unity: eventualAckIntervalMs must be an integer");
            }
            if (i < json.Length && (json[i] == '.' || json[i] == 'e' || json[i] == 'E'))
            {
                throw new InvalidOperationException("golem-unity: eventualAckIntervalMs must be an integer");
            }
            var text = json.Substring(start, i - start);
            if (!long.TryParse(text, System.Globalization.NumberStyles.Integer, System.Globalization.CultureInfo.InvariantCulture, out var parsed))
            {
                throw new InvalidOperationException("golem-unity: eventualAckIntervalMs must be an integer");
            }
            if (parsed < 0 || parsed > int.MaxValue)
            {
                throw new InvalidOperationException("golem-unity: eventualAckIntervalMs out of int32 range");
            }
            value = (int)parsed;
            return true;
        }

        private static bool TryFindObjectArray(string json, string field, out string arrayJson)
        {
            arrayJson = null;
            var key = "\"" + field + "\"";
            var keyIndex = IndexOfJsonKey(json, key);
            if (keyIndex < 0)
            {
                return false;
            }
            var i = keyIndex + key.Length;
            i = SkipWs(json, i);
            if (i >= json.Length || json[i] != ':')
            {
                return false;
            }
            i = SkipWs(json, i + 1);
            if (i >= json.Length || json[i] != '[')
            {
                throw new InvalidOperationException("golem-unity: serverCertificateHashes must be an array");
            }
            var end = FindMatchingBracket(json, i, '[', ']');
            arrayJson = json.Substring(i, end - i + 1);
            return true;
        }

        private static IEnumerable<string> IterateObjects(string arrayJson)
        {
            var i = 1;
            while (i < arrayJson.Length)
            {
                i = SkipWs(arrayJson, i);
                if (i >= arrayJson.Length || arrayJson[i] == ']')
                {
                    yield break;
                }
                if (arrayJson[i] != '{')
                {
                    throw new InvalidOperationException("golem-unity: serverCertificateHashes entries must be objects");
                }
                var end = FindMatchingBracket(arrayJson, i, '{', '}');
                yield return arrayJson.Substring(i, end - i + 1);
                i = end + 1;
                i = SkipWs(arrayJson, i);
                if (i < arrayJson.Length && arrayJson[i] == ',')
                {
                    i++;
                }
            }
        }

        private static int IndexOfJsonKey(string json, string key)
        {
            var start = 0;
            while (start < json.Length)
            {
                var index = json.IndexOf(key, start, StringComparison.Ordinal);
                if (index < 0)
                {
                    return -1;
                }
                if (index == 0 || IsJsonBoundary(json[index - 1]))
                {
                    return index;
                }
                start = index + key.Length;
            }
            return -1;
        }

        private static bool IsJsonBoundary(char ch)
        {
            return char.IsWhiteSpace(ch) || ch == '{' || ch == ',' || ch == '[';
        }

        private static bool TryReadJsonString(string json, int startQuote, out string value, out int endExclusive)
        {
            value = null;
            endExclusive = startQuote;
            if (startQuote >= json.Length || json[startQuote] != '"')
            {
                return false;
            }
            var sb = new StringBuilder();
            var i = startQuote + 1;
            while (i < json.Length)
            {
                var ch = json[i++];
                if (ch == '"')
                {
                    value = sb.ToString();
                    endExclusive = i;
                    return true;
                }
                if (ch == '\\' && i < json.Length)
                {
                    var esc = json[i++];
                    switch (esc)
                    {
                        case '"':
                        case '\\':
                        case '/':
                            sb.Append(esc);
                            break;
                        case 'b':
                            sb.Append('\b');
                            break;
                        case 'f':
                            sb.Append('\f');
                            break;
                        case 'n':
                            sb.Append('\n');
                            break;
                        case 'r':
                            sb.Append('\r');
                            break;
                        case 't':
                            sb.Append('\t');
                            break;
                        case 'u':
                            if (i + 4 > json.Length)
                            {
                                return false;
                            }
                            var hex = json.Substring(i, 4);
                            sb.Append((char)Convert.ToInt32(hex, 16));
                            i += 4;
                            break;
                        default:
                            sb.Append(esc);
                            break;
                    }
                    continue;
                }
                sb.Append(ch);
            }
            return false;
        }

        private static int FindMatchingBracket(string json, int openIndex, char open, char close)
        {
            var depth = 0;
            var inString = false;
            for (var i = openIndex; i < json.Length; i++)
            {
                var ch = json[i];
                if (inString)
                {
                    if (ch == '\\')
                    {
                        i++;
                        continue;
                    }
                    if (ch == '"')
                    {
                        inString = false;
                    }
                    continue;
                }
                if (ch == '"')
                {
                    inString = true;
                    continue;
                }
                if (ch == open)
                {
                    depth++;
                }
                else if (ch == close)
                {
                    depth--;
                    if (depth == 0)
                    {
                        return i;
                    }
                }
            }
            throw new InvalidOperationException("golem-unity: malformed JSON while parsing realtime config");
        }

        private static int SkipWs(string json, int index)
        {
            while (index < json.Length && char.IsWhiteSpace(json[index]))
            {
                index++;
            }
            return index;
        }

        private static string Truncate(string value, int maxChars)
        {
            if (string.IsNullOrEmpty(value) || value.Length <= maxChars)
            {
                return value ?? string.Empty;
            }
            return value.Substring(0, maxChars);
        }

        private static string Quote(string value)
        {
            return "\"" + value.Replace("\\", "\\\\").Replace("\"", "\\\"") + "\"";
        }
    }
}
