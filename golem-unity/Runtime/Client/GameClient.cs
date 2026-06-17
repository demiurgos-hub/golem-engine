using System;
using System.Collections.Generic;

namespace GolemEngine.Unity
{
    /// <summary>Owns a Golem transport connection and routes decoded protocol frames to generated managers.</summary>
    public sealed class GameClient
    {
        public const int MaxReliableMessageBytes = 32000;

        private readonly Func<byte[], object> _decodeEntityUpdate;
        private readonly Func<object, byte[]> _encodeCommand;
        private readonly Func<IReadOnlyList<byte[]>, byte[]> _encodePacket;
        private readonly Func<byte[], object> _decodeWorldUpdate;
        private readonly Func<IGolemTransport> _transportFactory;
        private readonly List<byte[]> _queuedFrames = new List<byte[]>();
        private int _queuedBytes;
        private bool _flushScheduled;
        private IGolemTransport _transport;

        public GameClient(
            IEntityManager entityManager,
            Func<byte[], object> decodeEntityUpdate,
            Func<object, byte[]> encodeCommand,
            Func<IReadOnlyList<byte[]>, byte[]> encodePacket,
            Func<IGolemTransport> transportFactory,
            IWorldManager worldManager = null,
            Func<byte[], object> decodeWorldUpdate = null,
            IEventManager eventManager = null)
        {
            Entities = entityManager ?? throw new ArgumentNullException(nameof(entityManager));
            _decodeEntityUpdate = decodeEntityUpdate ?? throw new ArgumentNullException(nameof(decodeEntityUpdate));
            _encodeCommand = encodeCommand ?? throw new ArgumentNullException(nameof(encodeCommand));
            _encodePacket = encodePacket ?? throw new ArgumentNullException(nameof(encodePacket));
            _transportFactory = transportFactory ?? throw new ArgumentNullException(nameof(transportFactory));
            World = worldManager;
            Events = eventManager;
            _decodeWorldUpdate = decodeWorldUpdate;
        }

        public IEntityManager Entities { get; }
        public IWorldManager World { get; }
        public IEventManager Events { get; }
        public bool Connected => _transport?.Connected ?? false;
        public event Action ConnectedEvent;
        public event Action<GolemDisconnectInfo> DisconnectedEvent;

        public void Connect(string url)
        {
            Disconnect();
            GolemUnityLog.Info($"connect url={GolemUnityLog.RedactUrl(url)}");
            var transport = _transportFactory();
            transport.ConnectedEvent += () => GolemMainThreadDispatcher.Enqueue(() => ConnectedEvent?.Invoke());
            transport.MessageEvent += bytes => GolemMainThreadDispatcher.Enqueue(() => HandleMessage(bytes));
            transport.UnreliableStateMessageEvent += bytes => GolemMainThreadDispatcher.Enqueue(() => HandleCompactStateBatch(bytes));
            transport.ReliableOrderedMessageEvent += bytes => GolemMainThreadDispatcher.Enqueue(() => HandleCompactStateBatch(bytes));
            transport.EventualStateMessageEvent += bytes => GolemMainThreadDispatcher.Enqueue(() => HandleCompactStateBatch(bytes));
            transport.DisconnectedEvent += info => GolemMainThreadDispatcher.Enqueue(() =>
            {
                GolemUnityLog.LogDisconnect(info.Transport ?? "unknown", info);
                ClearQueuedFrames();
                if (_transport == transport)
                {
                    _transport = null;
                }
                DisconnectedEvent?.Invoke(info);
            });
            _transport = transport;
            transport.Connect(url);
        }

        public void Disconnect()
        {
            ClearQueuedFrames();
            _transport?.Close();
            _transport = null;
        }

        public void Send(object command)
        {
            var frame = _encodeCommand(command);
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

        public void SendUnreliable(byte[] bytes)
        {
            var transport = _transport;
            if (transport == null || !transport.Connected || transport.MaxDatagramBytes <= 0)
            {
                return;
            }
            if (bytes == null)
            {
                throw new ArgumentNullException(nameof(bytes));
            }
            if (bytes.Length > transport.MaxDatagramBytes)
            {
                throw new InvalidOperationException($"golem-unity: datagram size {bytes.Length} exceeds max datagram {transport.MaxDatagramBytes}");
            }
            transport.SendUnreliable(bytes);
        }

        public void SendReliableUnordered(object command)
        {
            SendDatagramCommand(command, reliableOrdered: false);
        }

        public void SendReliableOrdered(object command)
        {
            SendDatagramCommand(command, reliableOrdered: true);
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

        private void SendDatagramCommand(object command, bool reliableOrdered)
        {
            var transport = _transport;
            if (transport == null || !transport.Connected || transport.MaxDatagramBytes <= 0)
            {
                return;
            }
            var frame = _encodeCommand(command);
            if (frame.Length > transport.MaxDatagramBytes)
            {
                throw new InvalidOperationException($"golem-unity: encoded datagram command size {frame.Length} exceeds max datagram {transport.MaxDatagramBytes}");
            }
            if (reliableOrdered)
            {
                transport.SendReliableOrdered(frame);
            }
            else
            {
                transport.SendReliableUnordered(frame);
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
