using System.Reflection;
using UnityEngine;

namespace GolemEngine.Unity
{
    /// <summary>
    /// Copies generated PosX/PosY and optional PosZ properties into a GameObject transform.
    /// In 3D, PosY is Unity's vertical axis and PosZ is depth.
    /// </summary>
    public sealed class GolemTransformBinding : MonoBehaviour
    {
        [SerializeField] private bool smooth = true;
        [SerializeField] private float lerpSpeed = 12f;

        private object _entity;
        private PropertyInfo _posX;
        private PropertyInfo _posY;
        private PropertyInfo _posZ;

        public void Bind(object entity)
        {
            _entity = entity;
            var type = entity.GetType();
            _posX = type.GetProperty("PosX");
            _posY = type.GetProperty("PosY");
            _posZ = type.GetProperty("PosZ");
            ApplyPosition(true);
        }

        private void Update()
        {
            ApplyPosition(!smooth);
        }

        private void ApplyPosition(bool snap)
        {
            if (_entity == null || _posX == null || _posY == null)
            {
                return;
            }

            var z = _posZ == null
                ? transform.position.z
                : System.Convert.ToSingle(_posZ.GetValue(_entity));
            var target = new Vector3(
                System.Convert.ToSingle(_posX.GetValue(_entity)),
                System.Convert.ToSingle(_posY.GetValue(_entity)),
                z);
            transform.position = snap ? target : Vector3.Lerp(transform.position, target, Time.deltaTime * lerpSpeed);
        }
    }
}
