using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>Base component for a GameObject bound to one generated synced entity.</summary>
    public abstract class GolemEntityView<TSynced> : MonoBehaviour where TSynced : class
    {
        public TSynced Entity { get; private set; }

        public void Bind(TSynced entity)
        {
            Entity = entity;
            OnBound(entity);
        }

        public void ApplyUpdate()
        {
            if (Entity != null)
            {
                OnSynced(Entity);
            }
        }

        public void Unbind()
        {
            var entity = Entity;
            Entity = null;
            OnRemoved(entity);
        }

        protected virtual void OnBound(TSynced entity) {}
        protected virtual void OnSynced(TSynced entity) {}
        protected virtual void OnRemoved(TSynced entity) {}
    }
}
