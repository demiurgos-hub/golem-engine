using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Unity component that owns a Golem client connection lifecycle.</summary>
    public abstract class GolemClientBehaviour : MonoBehaviour
    {
        [SerializeField] private string url = "ws://localhost:8080/ws";
        [SerializeField] private bool connectOnStart = true;
        [SerializeField] private bool debugLogging;

        public GameClient Client { get; private set; }
        protected string ConnectionUrl => url;

        protected virtual void Awake()
        {
            GolemMainThreadDispatcher.Ensure();
            GolemUnityLog.DebugEnabled = debugLogging;
            Client = CreateClient();
        }

        protected virtual void Start()
        {
            if (connectOnStart)
            {
                Connect();
            }
        }

        public void Connect()
        {
            GolemUnityLog.Info($"behaviour connect url={GolemUnityLog.RedactUrl(url)}");
            Client.Connect(url);
        }

        public void Disconnect()
        {
            Client?.Disconnect();
        }

        protected virtual void OnDestroy()
        {
            Disconnect();
        }

        protected abstract GameClient CreateClient();
    }
}
