using System;
using System.Collections.Generic;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

namespace GolemEngine.Unity
{
    /// <summary>Owns a Golem transport connection and routes decoded protocol frames to generated managers.</summary>
    public sealed class GameClient
    {
        public const int MaxReliableMessageBytes = 256 * 1024;
        public const int WebTransportFallbackTimeoutMilliseconds = 5000;

        private readonly Func<byte[], object> _decodeEntityUpdate;
        private readonly Func<object, byte[]> _encodeCommand;
        private readonly Func<IReadOnlyList<byte[]>, byte[]> _encodePacket;
        private readonly Func<byte[], object> _decodeWorldUpdate;
        private readonly Func<GolemConnectOptions, IGolemTransport> _transportFactory;
        private readonly Func<GolemConnectOptions, bool> _transportSupported;
        private readonly bool _legacyTransportFactory;
        private readonly object _connectionGate = new object();
        private readonly List<byte[]> _queuedFrames = new List<byte[]>();
        private int _queuedBytes;
        private bool _flushScheduled;
        private IGolemTransport _transport;
        private IGolemTransport _pendingTransport;
        private CancellationTokenSource _connectionCts;
        private int _connectionGeneration;
        private string _connectedTransport;
        private string _stickyPlanKey;
        private bool _preferFallback;

        public GameClient(
            IEntityManager entityManager,
            Func<byte[], object> decodeEntityUpdate,
            Func<object, byte[]> encodeCommand,
            Func<IReadOnlyList<byte[]>, byte[]> encodePacket,
            Func<GolemConnectOptions, IGolemTransport> transportFactory,
            IWorldManager worldManager = null,
            Func<byte[], object> decodeWorldUpdate = null,
            IEventManager eventManager = null,
            Func<GolemConnectOptions, bool> transportSupported = null)
        {
            Entities = entityManager ?? throw new ArgumentNullException(nameof(entityManager));
            _decodeEntityUpdate = decodeEntityUpdate ?? throw new ArgumentNullException(nameof(decodeEntityUpdate));
            _encodeCommand = encodeCommand ?? throw new ArgumentNullException(nameof(encodeCommand));
            _encodePacket = encodePacket ?? throw new ArgumentNullException(nameof(encodePacket));
            _transportFactory = transportFactory ?? throw new ArgumentNullException(nameof(transportFactory));
            _transportSupported = transportSupported ?? (_ => true);
            _legacyTransportFactory = false;
            World = worldManager;
            Events = eventManager;
            _decodeWorldUpdate = decodeWorldUpdate;
        }

        /// <summary>
        /// Compatibility constructor for parameterless transport factories.
        /// Compatible with <see cref="Connect(string)"/>. Prefer
        /// <see cref="GameClient(IEntityManager, Func{byte[], object}, Func{object, byte[]}, Func{IReadOnlyList{byte[]}, byte[]}, Func{GolemConnectOptions, IGolemTransport}, IWorldManager, Func{byte[], object}, IEventManager)"/>
        /// so <see cref="Connect(GolemConnectOptions)"/> can select transports and apply ACK options.
        /// Custom options-aware factories own certificate-hash handling; built-in
        /// <c>GolemWebTransportTransport.FromConnectOptions</c> still rejects unsupported hashes.
        /// </summary>
        [Obsolete("Use Func<GolemConnectOptions, IGolemTransport> so Connect(GolemConnectOptions) can select transports and apply ACK/certificate options.")]
        public GameClient(
            IEntityManager entityManager,
            Func<byte[], object> decodeEntityUpdate,
            Func<object, byte[]> encodeCommand,
            Func<IReadOnlyList<byte[]>, byte[]> encodePacket,
            Func<IGolemTransport> transportFactory,
            IWorldManager worldManager = null,
            Func<byte[], object> decodeWorldUpdate = null,
            IEventManager eventManager = null,
            Func<GolemConnectOptions, bool> transportSupported = null)
        {
            Entities = entityManager ?? throw new ArgumentNullException(nameof(entityManager));
            _decodeEntityUpdate = decodeEntityUpdate ?? throw new ArgumentNullException(nameof(decodeEntityUpdate));
            _encodeCommand = encodeCommand ?? throw new ArgumentNullException(nameof(encodeCommand));
            _encodePacket = encodePacket ?? throw new ArgumentNullException(nameof(encodePacket));
            if (transportFactory == null)
            {
                throw new ArgumentNullException(nameof(transportFactory));
            }
            _transportFactory = _ => transportFactory();
            _transportSupported = transportSupported ?? (_ => true);
            _legacyTransportFactory = true;
            World = worldManager;
            Events = eventManager;
            _decodeWorldUpdate = decodeWorldUpdate;
        }

        public IEntityManager Entities { get; }
        public IWorldManager World { get; }
        public IEventManager Events { get; }
        public bool Connected
        {
            get
            {
                lock (_connectionGate)
                {
                    return _transport?.Connected ?? false;
                }
            }
        }

        public string ConnectedTransport
        {
            get
            {
                lock (_connectionGate)
                {
                    return _connectedTransport;
                }
            }
        }
        public event Action ConnectedEvent;
        public event Action<GolemDisconnectInfo> DisconnectedEvent;

        /// <summary>
        /// Connects using the configured transport factory.
        /// Preserves factory behavior for custom parameterless factories (transport selection is not overridden).
        /// </summary>
        public void Connect(string url)
        {
            if (url == null)
            {
                throw new ArgumentNullException(nameof(url));
            }
            Connect(new GolemConnectOptions(url));
        }

        /// <summary>Connects using transport-aware options (URL, transport kind, cert hashes, ACK interval).</summary>
        public void Connect(GolemConnectOptions options)
        {
            if (options == null)
            {
                throw new ArgumentNullException(nameof(options));
            }
            if (_legacyTransportFactory && !string.IsNullOrEmpty(options.Transport))
            {
                throw new InvalidOperationException(
                    "golem-unity: parameterless transport factory cannot honor GolemConnectOptions.Transport; use Func<GolemConnectOptions, IGolemTransport> or Connect(string)");
            }
            ClearQueuedFrames();
            var generation = ReplaceConnection(default, resetAffinity: true);
            GolemUnityLog.Info(
                $"connect transport={(string.IsNullOrEmpty(options.Transport) ? "factory-default" : options.Transport)} url={RedactUrl(options.Url)}");
            var transport = _transportFactory(options) ?? throw new InvalidOperationException("golem-unity: transport factory returned null");
            WireDirectTransport(generation, transport, options);
            lock (_connectionGate)
            {
                if (_connectionGeneration == generation)
                {
                    _pendingTransport = transport;
                }
            }
            try
            {
                transport.Connect(options.Url);
            }
            catch
            {
                StopGeneration(generation, transport);
                throw;
            }
        }

        /// <summary>
        /// Connects through a credential-free primary/fallback config, resolving fresh options
        /// immediately before each physical dial.
        /// </summary>
        public void Connect(
            GolemRealtimeConfig config,
            GolemConnectOptionsResolver resolver,
            CancellationToken cancellationToken = default)
        {
            if (_legacyTransportFactory)
            {
                throw new InvalidOperationException(
                    "golem-unity: parameterless transport factory cannot honor a realtime connection plan; use Func<GolemConnectOptions, IGolemTransport>");
            }
            ValidateConnectionConfig(config);
            var planKey = ConnectionPlanKey(config);
            ClearQueuedFrames();
            var generation = ReplaceConnection(cancellationToken, resetAffinity: false);
            lock (_connectionGate)
            {
                if (_connectionGeneration != generation)
                {
                    return;
                }
                if (!string.Equals(_stickyPlanKey, planKey, StringComparison.Ordinal))
                {
                    _stickyPlanKey = planKey;
                    _preferFallback = false;
                }
            }
            _ = ConnectPlanAsync(generation, planKey, config, resolver);
        }

        public void Disconnect()
        {
            ClearQueuedFrames();
            ReplaceConnection(default, resetAffinity: false);
        }

        private async Task ConnectPlanAsync(
            int generation,
            string planKey,
            GolemRealtimeConfig config,
            GolemConnectOptionsResolver resolver)
        {
            try
            {
                var primary = OptionsFromConfig(config);
                var fallback = config.Fallback == null
                    ? null
                    : new GolemConnectOptions(config.Fallback.Url, config.Fallback.Transport);
                bool preferFallback;
                lock (_connectionGate)
                {
                    preferFallback = _connectionGeneration == generation &&
                                     _preferFallback &&
                                     fallback != null &&
                                     string.Equals(_stickyPlanKey, planKey, StringComparison.Ordinal);
                }

                CandidateResult result;
                if (!preferFallback)
                {
                    result = await ConnectCandidateAsync(
                        generation,
                        planKey,
                        primary,
                        resolver,
                        isFallback: false).ConfigureAwait(false);
                    if (result.Kind == CandidateResultKind.Opened || result.Kind == CandidateResultKind.Canceled)
                    {
                        return;
                    }
                    if (result.Kind == CandidateResultKind.Terminal || fallback == null)
                    {
                        PublishFinalDisconnect(generation, result.Info);
                        return;
                    }
                }

                result = await ConnectCandidateAsync(
                    generation,
                    planKey,
                    fallback,
                    resolver,
                    isFallback: true).ConfigureAwait(false);
                if (result.Kind != CandidateResultKind.Opened && result.Kind != CandidateResultKind.Canceled)
                {
                    PublishFinalDisconnect(generation, result.Info);
                }
            }
            catch (OperationCanceledException)
            {
                // Cancellation aborts the logical attempt without publishing a failure.
            }
            catch (Exception)
            {
                PublishFinalDisconnect(
                    generation,
                    FailureInfo(
                        "connection_plan_failed",
                        config.Transport,
                        config.Url,
                        GolemDisconnectCategory.Unknown));
            }
        }

        /// <summary>
        /// Sends a command on the reliable-unordered datagram lane when available,
        /// or on the shared reliable stream otherwise.
        /// </summary>
        public void Send(object command)
        {
            SendCommand(command, ordered: false);
        }

        /// <summary>
        /// Sends a command on the reliable-ordered datagram lane when available,
        /// or on the shared reliable stream otherwise.
        /// </summary>
        public void SendOrdered(object command)
        {
            SendCommand(command, ordered: true);
        }

        private async Task<CandidateResult> ConnectCandidateAsync(
            int generation,
            string planKey,
            GolemConnectOptions baseOptions,
            GolemConnectOptionsResolver resolver,
            bool isFallback)
        {
            var token = ConnectionToken(generation);
            if (token.IsCancellationRequested)
            {
                return CandidateResult.Canceled();
            }
            bool supported;
            try
            {
                supported = _transportSupported(baseOptions);
            }
            catch (Exception)
            {
                return CandidateResult.Terminal(FailureInfo(
                    "transport_capability_failed",
                    baseOptions.Transport,
                    baseOptions.Url,
                    GolemDisconnectCategory.TransportBackend));
            }
            if (!supported)
            {
                return CandidateResult.Unsupported(new GolemDisconnectInfo(
                    false,
                    reason: "transport_unsupported",
                    transport: baseOptions.Transport,
                    url: RedactUrl(baseOptions.Url),
                    phase: GolemDisconnectPhase.Connect,
                    category: GolemDisconnectCategory.TransportBackend));
            }

            GolemConnectOptions options;
            try
            {
                options = resolver == null
                    ? CloneOptions(baseOptions)
                    : await resolver(CloneOptions(baseOptions), token).ConfigureAwait(false);
                token.ThrowIfCancellationRequested();
                ValidateResolvedOptions(baseOptions, options);
            }
            catch (OperationCanceledException) when (token.IsCancellationRequested)
            {
                return CandidateResult.Canceled();
            }
            catch (Exception)
            {
                return CandidateResult.Terminal(new GolemDisconnectInfo(
                    false,
                    reason: "connection_options_failed",
                    error: new InvalidOperationException("golem-unity: resolving connection options failed"),
                    transport: baseOptions.Transport,
                    url: RedactUrl(baseOptions.Url),
                    phase: GolemDisconnectPhase.Connect,
                    category: GolemDisconnectCategory.Unknown));
            }

            IGolemTransport transport;
            try
            {
                transport = _transportFactory(options);
                if (transport == null)
                {
                    throw new InvalidOperationException("golem-unity: transport factory returned null");
                }
            }
            catch (Exception)
            {
                return CandidateResult.TransportFailed(new GolemDisconnectInfo(
                    false,
                    reason: "transport_factory_failed",
                    error: new InvalidOperationException("golem-unity: transport factory failed"),
                    transport: options.Transport,
                    url: RedactUrl(options.Url),
                    phase: GolemDisconnectPhase.Connect,
                    category: GolemDisconnectCategory.TransportBackend));
            }
            if (token.IsCancellationRequested)
            {
                CloseTransport(transport);
                return CandidateResult.Canceled();
            }

            var attempt = new CandidateAttempt();
            WireCandidateTransport(generation, planKey, transport, options, attempt, isFallback);
            lock (_connectionGate)
            {
                if (_connectionGeneration != generation)
                {
                    attempt.Active = false;
                    CloseTransport(transport);
                    return CandidateResult.Canceled();
                }
                _pendingTransport = transport;
            }

            CancellationTokenRegistration cancellationRegistration;
            try
            {
                cancellationRegistration = token.Register(
                    () => CancelCandidate(generation, transport, attempt));
            }
            catch (ObjectDisposedException)
            {
                DeactivateCandidate(generation, transport, attempt);
                return CandidateResult.Canceled();
            }

            using (cancellationRegistration)
            {
                lock (attempt.DialGate)
                {
                    if (!CandidateCanDial(generation, attempt, token))
                    {
                        DeactivateCandidate(generation, transport, attempt);
                        return CandidateResult.Canceled();
                    }
                    try
                    {
                        transport.Connect(options.Url);
                    }
                    catch (Exception ex)
                    {
                        DeactivateCandidate(generation, transport, attempt);
                        var info = new GolemDisconnectInfo(
                            false,
                            reason: "transport_connect_failed",
                            error: new InvalidOperationException("golem-unity: transport connect failed"),
                            transport: options.Transport,
                            url: RedactUrl(options.Url),
                            phase: GolemDisconnectPhase.Connect,
                            category: GolemDisconnectCategory.TransportBackend);
                        return IsTerminalHandshakeException(ex)
                            ? CandidateResult.Terminal(info)
                            : CandidateResult.TransportFailed(info);
                    }
                }

                if (options.Transport == GolemConnectOptions.TransportWebTransport)
                {
                    using (var timeoutCts = CancellationTokenSource.CreateLinkedTokenSource(token))
                    {
                        var timeout = Task.Delay(WebTransportFallbackTimeoutMilliseconds, timeoutCts.Token);
                        var completed = await Task.WhenAny(attempt.Completion.Task, timeout).ConfigureAwait(false);
                        if (completed == attempt.Completion.Task)
                        {
                            timeoutCts.Cancel();
                        }
                        else if (!token.IsCancellationRequested)
                        {
                            TimeoutCandidate(generation, transport, attempt, options);
                        }
                    }
                }
                var result = await attempt.Completion.Task.ConfigureAwait(false);
                if (result.Kind != CandidateResultKind.Opened)
                {
                    DeactivateCandidate(generation, transport, attempt);
                }
                return result;
            }
        }

        private void WireDirectTransport(int generation, IGolemTransport transport, GolemConnectOptions options)
        {
            transport.ConnectedEvent += () =>
            {
                lock (_connectionGate)
                {
                    if (_connectionGeneration != generation || _pendingTransport != transport)
                    {
                        return;
                    }
                    _pendingTransport = null;
                    _transport = transport;
                    _connectedTransport = options.Transport;
                }
                if (ConnectedEvent != null)
                {
                    EnqueueIfCurrent(generation, transport, () => ConnectedEvent?.Invoke());
                }
            };
            WireMessages(generation, transport);
            transport.DisconnectedEvent += info =>
            {
                if (!DetachCurrentTransport(generation, transport))
                {
                    return;
                }
                EnqueueDisconnectIfCurrent(generation, SanitizeDisconnectInfo(info, options));
            };
        }

        private void WireCandidateTransport(
            int generation,
            string planKey,
            IGolemTransport transport,
            GolemConnectOptions options,
            CandidateAttempt attempt,
            bool isFallback)
        {
            transport.ConnectedEvent += () =>
            {
                lock (_connectionGate)
                {
                    if (_connectionGeneration != generation || !attempt.Active || _pendingTransport != transport)
                    {
                        return;
                    }
                    attempt.Opened = true;
                    _pendingTransport = null;
                    _transport = transport;
                    _connectedTransport = options.Transport;
                    if (isFallback && options.Transport == GolemConnectOptions.TransportWebSocket)
                    {
                        _stickyPlanKey = planKey;
                        _preferFallback = true;
                    }
                    attempt.Completion.TrySetResult(CandidateResult.Opened());
                }
                if (ConnectedEvent != null)
                {
                    EnqueueIfCurrent(generation, transport, () => ConnectedEvent?.Invoke());
                }
            };
            WireMessages(generation, transport);
            transport.DisconnectedEvent += info =>
            {
                bool opened;
                lock (_connectionGate)
                {
                    if (_connectionGeneration != generation || !attempt.Active)
                    {
                        return;
                    }
                    opened = attempt.Opened;
                    attempt.Active = false;
                    if (_pendingTransport == transport)
                    {
                        _pendingTransport = null;
                    }
                    if (_transport == transport)
                    {
                        _transport = null;
                        _connectedTransport = null;
                    }
                    if (!opened)
                    {
                        var safeInfo = SanitizeDisconnectInfo(info, options);
                        var preOpenResult = IsTerminalHandshakeFailure(info)
                            ? CandidateResult.Terminal(safeInfo)
                            : CandidateResult.TransportFailed(safeInfo);
                        attempt.Completion.TrySetResult(preOpenResult);
                    }
                }
                if (!opened)
                {
                    return;
                }
                EnqueueDisconnectIfCurrent(generation, SanitizeDisconnectInfo(info, options));
            };
        }

        private bool CandidateCanDial(
            int generation,
            CandidateAttempt attempt,
            CancellationToken cancellationToken)
        {
            lock (_connectionGate)
            {
                return _connectionGeneration == generation &&
                       attempt.Active &&
                       !cancellationToken.IsCancellationRequested;
            }
        }

        private void CancelCandidate(int generation, IGolemTransport transport, CandidateAttempt attempt)
        {
            lock (attempt.DialGate)
            {
                var shouldClose = false;
                lock (_connectionGate)
                {
                    if (_connectionGeneration != generation || !attempt.Active || attempt.Opened)
                    {
                        return;
                    }
                    attempt.Active = false;
                    if (_pendingTransport == transport)
                    {
                        _pendingTransport = null;
                    }
                    attempt.Completion.TrySetResult(CandidateResult.Canceled());
                    shouldClose = true;
                }
                if (shouldClose)
                {
                    CloseTransport(transport);
                }
            }
        }

        private void TimeoutCandidate(
            int generation,
            IGolemTransport transport,
            CandidateAttempt attempt,
            GolemConnectOptions options)
        {
            var shouldClose = false;
            lock (_connectionGate)
            {
                if (_connectionGeneration != generation || !attempt.Active || attempt.Opened)
                {
                    return;
                }
                attempt.Active = false;
                if (_pendingTransport == transport)
                {
                    _pendingTransport = null;
                }
                attempt.Completion.TrySetResult(CandidateResult.TransportFailed(new GolemDisconnectInfo(
                    false,
                    reason: "webtransport_open_timeout",
                    error: new TimeoutException("golem-unity: WebTransport did not open within 5 seconds"),
                    transport: options.Transport,
                    url: RedactUrl(options.Url),
                    phase: GolemDisconnectPhase.Connect,
                    category: GolemDisconnectCategory.Timeout)));
                shouldClose = true;
            }
            if (shouldClose)
            {
                CloseTransport(transport);
            }
        }

        private void WireMessages(int generation, IGolemTransport transport)
        {
            transport.MessageEvent += bytes => EnqueueIfCurrent(generation, transport, () => HandleMessage(bytes));
            transport.UnreliableStateMessageEvent += bytes => EnqueueIfCurrent(generation, transport, () => HandleCompactStateBatch(bytes));
            transport.ReliableOrderedMessageEvent += bytes => EnqueueIfCurrent(generation, transport, () => HandleCompactStateBatch(bytes));
            transport.EventualStateMessageEvent += bytes => EnqueueIfCurrent(generation, transport, () => HandleCompactStateBatch(bytes));
        }

        private void EnqueueIfCurrent(int generation, IGolemTransport transport, Action action)
        {
            GolemMainThreadDispatcher.Enqueue(() =>
            {
                lock (_connectionGate)
                {
                    if (_connectionGeneration != generation || _transport != transport)
                    {
                        return;
                    }
                }
                action();
            });
        }

        private void EnqueueDisconnectIfCurrent(int generation, GolemDisconnectInfo info)
        {
            GolemMainThreadDispatcher.Enqueue(() =>
            {
                lock (_connectionGate)
                {
                    if (_connectionGeneration != generation)
                    {
                        return;
                    }
                }
                GolemUnityLog.LogDisconnect(info.Transport ?? "unknown", info);
                ClearQueuedFrames();
                DisconnectedEvent?.Invoke(info);
            });
        }

        private void PublishFinalDisconnect(int generation, GolemDisconnectInfo info)
        {
            lock (_connectionGate)
            {
                if (_connectionGeneration != generation || _connectionCts == null || _connectionCts.IsCancellationRequested)
                {
                    return;
                }
                _pendingTransport = null;
                _transport = null;
                _connectedTransport = null;
            }
            EnqueueDisconnectIfCurrent(generation, info);
        }

        private int ReplaceConnection(CancellationToken cancellationToken, bool resetAffinity)
        {
            IGolemTransport active;
            IGolemTransport pending;
            CancellationTokenSource previous;
            int generation;
            lock (_connectionGate)
            {
                generation = ++_connectionGeneration;
                active = _transport;
                pending = _pendingTransport;
                previous = _connectionCts;
                _transport = null;
                _pendingTransport = null;
                _connectedTransport = null;
                _connectionCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
                if (resetAffinity)
                {
                    _stickyPlanKey = null;
                    _preferFallback = false;
                }
            }
            previous?.Cancel();
            previous?.Dispose();
            if (pending != null && pending != active)
            {
                CloseTransport(pending);
            }
            CloseTransport(active);
            return generation;
        }

        private CancellationToken ConnectionToken(int generation)
        {
            lock (_connectionGate)
            {
                return _connectionGeneration == generation && _connectionCts != null
                    ? _connectionCts.Token
                    : new CancellationToken(true);
            }
        }

        private void StopGeneration(int generation, IGolemTransport transport)
        {
            lock (_connectionGate)
            {
                if (_connectionGeneration != generation)
                {
                    return;
                }
                if (_pendingTransport == transport)
                {
                    _pendingTransport = null;
                }
                if (_transport == transport)
                {
                    _transport = null;
                    _connectedTransport = null;
                }
            }
            CloseTransport(transport);
        }

        private void DeactivateCandidate(int generation, IGolemTransport transport, CandidateAttempt attempt)
        {
            lock (_connectionGate)
            {
                attempt.Active = false;
                if (_connectionGeneration == generation && _pendingTransport == transport)
                {
                    _pendingTransport = null;
                }
                if (_connectionGeneration == generation && _transport == transport && !attempt.Opened)
                {
                    _transport = null;
                    _connectedTransport = null;
                }
            }
            CloseTransport(transport);
        }

        private bool DetachCurrentTransport(int generation, IGolemTransport transport)
        {
            lock (_connectionGate)
            {
                if (_connectionGeneration != generation || (_transport != transport && _pendingTransport != transport))
                {
                    return false;
                }
                if (_transport == transport)
                {
                    _transport = null;
                    _connectedTransport = null;
                }
                if (_pendingTransport == transport)
                {
                    _pendingTransport = null;
                }
                return true;
            }
        }

        private static void ValidateConnectionConfig(GolemRealtimeConfig config)
        {
            if (config == null)
            {
                throw new ArgumentNullException(nameof(config));
            }
            if (config.Transport != GolemConnectOptions.TransportWebSocket &&
                config.Transport != GolemConnectOptions.TransportWebTransport)
            {
                throw new ArgumentException("golem-unity: unsupported primary transport", nameof(config));
            }
            if (string.IsNullOrWhiteSpace(config.Url))
            {
                throw new ArgumentException("golem-unity: primary url is required", nameof(config));
            }
            ValidateEndpointUrl(config.Transport, config.Url, nameof(config));
            if (config.Fallback != null &&
                (config.Transport != GolemConnectOptions.TransportWebTransport ||
                 config.Fallback.Transport != GolemConnectOptions.TransportWebSocket ||
                 string.IsNullOrWhiteSpace(config.Fallback.Url)))
            {
                throw new ArgumentException(
                    "golem-unity: fallback must be a non-empty websocket endpoint for a webtransport primary",
                    nameof(config));
            }
            if (config.Fallback != null)
            {
                ValidateEndpointUrl(config.Fallback.Transport, config.Fallback.Url, nameof(config));
            }
        }

        private static void ValidateResolvedOptions(GolemConnectOptions candidate, GolemConnectOptions resolved)
        {
            if (resolved == null)
            {
                throw new InvalidOperationException("golem-unity: connection options resolver returned null");
            }
            if (!string.Equals(candidate.Transport, resolved.Transport, StringComparison.Ordinal))
            {
                throw new InvalidOperationException("golem-unity: connection options resolver changed transport");
            }
            if (string.IsNullOrWhiteSpace(resolved.Url))
            {
                throw new InvalidOperationException("golem-unity: connection options resolver returned an empty url");
            }
            if (!SameEndpointExceptQuery(candidate.Url, resolved.Url))
            {
                throw new InvalidOperationException(
                    "golem-unity: connection options resolver may only change URL query parameters");
            }
            if (candidate.EventualAckIntervalMs != resolved.EventualAckIntervalMs ||
                !EqualHashes(candidate.ServerCertificateHashes, resolved.ServerCertificateHashes))
            {
                throw new InvalidOperationException(
                    "golem-unity: connection options resolver changed endpoint metadata");
            }
        }

        private static void ValidateEndpointUrl(string transport, string url, string parameterName)
        {
            if (!Uri.TryCreate(url, UriKind.Absolute, out var parsed) ||
                !string.IsNullOrEmpty(parsed.UserInfo))
            {
                throw new ArgumentException("golem-unity: realtime endpoint must be an absolute URL", parameterName);
            }
            var validScheme = transport == GolemConnectOptions.TransportWebTransport
                ? string.Equals(parsed.Scheme, Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase)
                : string.Equals(parsed.Scheme, "ws", StringComparison.OrdinalIgnoreCase) ||
                  string.Equals(parsed.Scheme, "wss", StringComparison.OrdinalIgnoreCase);
            if (!validScheme)
            {
                throw new ArgumentException(
                    $"golem-unity: invalid URL scheme for {transport} endpoint",
                    parameterName);
            }
        }

        private static bool SameEndpointExceptQuery(string candidateUrl, string resolvedUrl)
        {
            if (!Uri.TryCreate(candidateUrl, UriKind.Absolute, out var candidate) ||
                !Uri.TryCreate(resolvedUrl, UriKind.Absolute, out var resolved))
            {
                return false;
            }
            return string.Equals(
                       candidate.Scheme,
                       resolved.Scheme,
                       StringComparison.OrdinalIgnoreCase) &&
                   string.Equals(candidate.Host, resolved.Host, StringComparison.OrdinalIgnoreCase) &&
                   candidate.Port == resolved.Port &&
                   string.Equals(candidate.AbsolutePath, resolved.AbsolutePath, StringComparison.Ordinal) &&
                   string.Equals(candidate.UserInfo, resolved.UserInfo, StringComparison.Ordinal) &&
                   string.Equals(candidate.Fragment, resolved.Fragment, StringComparison.Ordinal);
        }

        private static bool EqualHashes(
            IReadOnlyList<GolemCertificateHash> left,
            IReadOnlyList<GolemCertificateHash> right)
        {
            var leftCount = left?.Count ?? 0;
            var rightCount = right?.Count ?? 0;
            if (leftCount != rightCount)
            {
                return false;
            }
            for (var i = 0; i < leftCount; i++)
            {
                if (!string.Equals(left[i].Algorithm, right[i].Algorithm, StringComparison.Ordinal) ||
                    !string.Equals(left[i].Value, right[i].Value, StringComparison.Ordinal))
                {
                    return false;
                }
            }
            return true;
        }

        private static GolemConnectOptions OptionsFromConfig(GolemRealtimeConfig config)
        {
            return new GolemConnectOptions(
                config.Url,
                config.Transport,
                config.ServerCertificateHashes,
                config.EventualAckIntervalMs ?? 0);
        }

        private static GolemConnectOptions CloneOptions(GolemConnectOptions options)
        {
            return new GolemConnectOptions(
                options.Url,
                options.Transport,
                options.ServerCertificateHashes,
                options.EventualAckIntervalMs);
        }

        private static string ConnectionPlanKey(GolemRealtimeConfig config)
        {
            var key = new StringBuilder();
            key.Append(config.Transport).Append('\n').Append(config.Url).Append('\n')
                .Append(config.EventualAckIntervalMs ?? 0).Append('\n');
            if (config.ServerCertificateHashes != null)
            {
                foreach (var hash in config.ServerCertificateHashes)
                {
                    key.Append(hash.Algorithm).Append(':').Append(hash.Value).Append('\n');
                }
            }
            if (config.Fallback != null)
            {
                key.Append("fallback\n").Append(config.Fallback.Transport).Append('\n').Append(config.Fallback.Url);
            }
            return key.ToString();
        }

        private static GolemDisconnectInfo SanitizeDisconnectInfo(
            GolemDisconnectInfo info,
            GolemConnectOptions options)
        {
            return new GolemDisconnectInfo(
                info.WasClean,
                info.Code,
                SanitizeReason(info.Reason),
                info.Error == null ? null : new InvalidOperationException("golem-unity: transport operation failed"),
                string.IsNullOrEmpty(info.Transport) ? options.Transport : info.Transport,
                RedactUrl(string.IsNullOrEmpty(info.Url) ? options.Url : info.Url),
                info.Phase,
                info.Category);
        }

        private static string RedactUrl(string url)
        {
            var redacted = GolemUnityLog.RedactUrl(url);
            if (!string.Equals(redacted, url, StringComparison.Ordinal) || string.IsNullOrEmpty(url))
            {
                return redacted;
            }
            var query = url.IndexOf('?');
            var fragment = url.IndexOf('#');
            var end = query >= 0 ? query : fragment;
            if (fragment >= 0 && (end < 0 || fragment < end))
            {
                end = fragment;
            }
            return end >= 0 ? url.Substring(0, end) : url;
        }

        private static string SanitizeReason(string reason)
        {
            if (string.IsNullOrEmpty(reason))
            {
                return reason;
            }
            if (reason.IndexOf("ticket", StringComparison.OrdinalIgnoreCase) >= 0 ||
                reason.IndexOf("token", StringComparison.OrdinalIgnoreCase) >= 0 ||
                reason.IndexOf('?') >= 0)
            {
                return "transport_operation_failed";
            }
            return reason;
        }

        private static bool IsTerminalHandshakeFailure(GolemDisconnectInfo info)
        {
            if (info.Code == 401 || info.Code == 403 || info.Code == 426)
            {
                return true;
            }
            var text = (info.Reason ?? string.Empty) + " " + ExceptionText(info.Error);
            return MentionsHttpStatus(text, 401) ||
                   MentionsHttpStatus(text, 403) ||
                   MentionsHttpStatus(text, 426) ||
                   MentionsTicketFailure(text) ||
                   text.IndexOf("upgrade required", StringComparison.OrdinalIgnoreCase) >= 0;
        }

        private static bool IsTerminalHandshakeException(Exception error)
        {
            var text = ExceptionText(error);
            return MentionsHttpStatus(text, 401) ||
                   MentionsHttpStatus(text, 403) ||
                   MentionsHttpStatus(text, 426) ||
                   MentionsTicketFailure(text) ||
                   text.IndexOf("upgrade required", StringComparison.OrdinalIgnoreCase) >= 0;
        }

        private static bool MentionsTicketFailure(string text)
        {
            if (string.IsNullOrEmpty(text) ||
                text.IndexOf("ticket", StringComparison.OrdinalIgnoreCase) < 0)
            {
                return false;
            }
            return text.IndexOf("reject", StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("invalid", StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("expired", StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("used", StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("consumed", StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("unauthor", StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("forbid", StringComparison.OrdinalIgnoreCase) >= 0;
        }

        private static bool MentionsHttpStatus(string text, int status)
        {
            if (string.IsNullOrEmpty(text))
            {
                return false;
            }
            var value = status.ToString(System.Globalization.CultureInfo.InvariantCulture);
            var trimmed = text.Trim();
            return string.Equals(trimmed, value, StringComparison.Ordinal) ||
                   text.IndexOf("status " + value, StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("status code '" + value + "'", StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("status code \"" + value + "\"", StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("http " + value, StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("http/1.1 " + value, StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("http/2 " + value, StringComparison.OrdinalIgnoreCase) >= 0 ||
                   text.IndexOf("(" + value + ")", StringComparison.Ordinal) >= 0;
        }

        private static string ExceptionText(Exception error)
        {
            var value = string.Empty;
            for (var current = error; current != null; current = current.InnerException)
            {
                value += " " + current.Message;
            }
            return value;
        }

        private static GolemDisconnectInfo FailureInfo(
            string reason,
            string transport,
            string url,
            GolemDisconnectCategory category)
        {
            return new GolemDisconnectInfo(
                false,
                reason: reason,
                error: new InvalidOperationException("golem-unity: connection attempt failed"),
                transport: transport,
                url: RedactUrl(url),
                phase: GolemDisconnectPhase.Connect,
                category: category);
        }

        private static void CloseTransport(IGolemTransport transport)
        {
            try
            {
                transport?.Close();
            }
            catch
            {
                // A failed candidate must not prevent the next candidate from being attempted.
            }
        }

        private sealed class CandidateAttempt
        {
            public readonly object DialGate = new object();
            public bool Active = true;
            public bool Opened;
            public readonly TaskCompletionSource<CandidateResult> Completion =
                new TaskCompletionSource<CandidateResult>(TaskCreationOptions.RunContinuationsAsynchronously);
        }

        private enum CandidateResultKind
        {
            Opened,
            TransportFailed,
            Unsupported,
            Terminal,
            Canceled
        }

        private sealed class CandidateResult
        {
            private CandidateResult(CandidateResultKind kind, GolemDisconnectInfo info)
            {
                Kind = kind;
                Info = info;
            }

            public CandidateResultKind Kind { get; }
            public GolemDisconnectInfo Info { get; }

            public static CandidateResult Opened() => new CandidateResult(CandidateResultKind.Opened, default);
            public static CandidateResult TransportFailed(GolemDisconnectInfo info) =>
                new CandidateResult(CandidateResultKind.TransportFailed, info);
            public static CandidateResult Unsupported(GolemDisconnectInfo info) =>
                new CandidateResult(CandidateResultKind.Unsupported, info);
            public static CandidateResult Terminal(GolemDisconnectInfo info) =>
                new CandidateResult(CandidateResultKind.Terminal, info);
            public static CandidateResult Canceled() => new CandidateResult(CandidateResultKind.Canceled, default);
        }

        private void Flush()
        {
            _flushScheduled = false;
            if (_queuedFrames.Count == 0)
            {
                return;
            }

            var packet = _encodePacket(_queuedFrames);
            ClearQueuedFrames();
            if (packet.Length > MaxReliableMessageBytes)
            {
                throw new InvalidOperationException($"golem-unity: packet size {packet.Length} exceeds max reliable message {MaxReliableMessageBytes}");
            }
            if (_transport == null || !_transport.Connected)
            {
                if (GolemUnityLog.DebugEnabled)
                {
                    GolemUnityLog.Warn($"dropping queued packet bytes={packet.Length} reason=disconnected");
                }
                return;
            }
            _transport.Send(packet);
        }

        private void SendCommand(object command, bool ordered)
        {
            var transport = _transport;
            if (transport == null || !transport.Connected)
            {
                return;
            }

            var frame = _encodeCommand(command);
            if (transport.MaxDatagramBytes > 0)
            {
                var maxPayloadBytes = transport.MaxDatagramBytes -
                    GolemDatagramProtocol.PacketHeaderBytes -
                    GolemDatagramProtocol.LaneHeaderBytes -
                    GolemDatagramProtocol.ReliableMessageIdBytes;
                var laneName = "reliable unordered";
                if (ordered)
                {
                    maxPayloadBytes -= GolemDatagramProtocol.ReliableOrderedSequenceBytes;
                    laneName = "reliable ordered";
                }
                if (frame.Length > maxPayloadBytes)
                {
                    throw new InvalidOperationException($"golem-unity: encoded {laneName} command size {frame.Length} exceeds max payload {maxPayloadBytes}");
                }
                if (ordered)
                {
                    transport.SendReliableOrdered(frame);
                }
                else
                {
                    transport.SendReliableUnordered(frame);
                }
                return;
            }

            QueueReliableStreamCommand(frame);
        }

        private void QueueReliableStreamCommand(byte[] frame)
        {
            var entrySize = ClientPacketEntrySize(frame);
            if (entrySize > MaxReliableMessageBytes)
            {
                throw new InvalidOperationException($"golem-unity: command size {entrySize} exceeds max reliable message {MaxReliableMessageBytes}");
            }

            if (_queuedBytes > 0 && _queuedBytes + entrySize > MaxReliableMessageBytes)
            {
                Flush();
            }

            _queuedFrames.Add(frame);
            _queuedBytes += entrySize;

            if (!_flushScheduled)
            {
                _flushScheduled = true;
                GolemMainThreadDispatcher.Enqueue(Flush);
            }
        }

        private void ClearQueuedFrames()
        {
            _queuedFrames.Clear();
            _queuedBytes = 0;
            _flushScheduled = false;
        }

        private void HandleMessage(byte[] bytes)
        {
            var reader = new PbReader(bytes);
            while (!reader.Done)
            {
                var tag = reader.Tag();
                switch (tag.Field)
                {
                    case 1:
                        Entities.ApplyUpdate(_decodeEntityUpdate(reader.Bytes()));
                        break;
                    case 2:
                        if (World != null && _decodeWorldUpdate != null)
                        {
                            World.ApplyUpdate(_decodeWorldUpdate(reader.Bytes()));
                        }
                        else
                        {
                            reader.Skip(tag.Wire);
                        }
                        break;
                    case 3:
                        if (Events != null)
                        {
                            Events.ApplyRaw(reader.Bytes());
                        }
                        else
                        {
                            reader.Skip(tag.Wire);
                        }
                        break;
                    default:
                        reader.Skip(tag.Wire);
                        break;
                }
            }
        }

        private void HandleCompactStateBatch(byte[] bytes)
        {
            if (Entities is not ICompactEntityManager compactEntities)
            {
                throw new InvalidOperationException("golem-unity: EntityManager does not support compact state updates; regenerate the C# client code");
            }
            GolemDatagramProtocol.DecodeLengthPrefixedFrames(bytes, compactEntities.ApplyCompactUpdate);
        }

        private static int ClientPacketEntrySize(byte[] frame)
        {
            return 1 + VarintSize(frame.Length) + frame.Length;
        }

        private static int VarintSize(int value)
        {
            var size = 1;
            var v = (uint)value;
            while (v > 0x7f)
            {
                v >>= 7;
                size++;
            }
            return size;
        }
    }
}
