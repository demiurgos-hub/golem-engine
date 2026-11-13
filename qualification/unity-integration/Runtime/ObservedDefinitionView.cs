using UnityEngine;

namespace Golem.Integration.Fixture
{
    /// <summary>Records the real spawner's binding callback without replacing its generated entity or transform binding.</summary>
    public sealed class ObservedDefinitionView : MonoBehaviour
    {
        public string Presentation;
        public object Entity { get; private set; }
        public Vector3 InitialPosition { get; private set; }
        public int BindCalls { get; private set; }

        public void Bind(object entity)
        {
            Entity = entity;
            InitialPosition = transform.position;
            BindCalls++;
        }
    }
}
