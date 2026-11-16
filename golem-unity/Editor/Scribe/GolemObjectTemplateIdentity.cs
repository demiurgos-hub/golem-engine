using System;
using System.Collections.Generic;
using System.Globalization;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>
    /// Root-relative identities for one template hierarchy: <c>root/transform</c>, <c>root/child/0/collider/1</c>,
    /// <c>root/child/2/component/Namespace.Type/0</c>. IDs depend only on hierarchy position, so every copy of a
    /// prefab gets the same IDs and they survive reimports and renames.
    /// </summary>
    internal sealed class GolemObjectTemplateIdentity
    {
        private readonly Dictionary<UnityEngine.Object, string> objects = new Dictionary<UnityEngine.Object, string>();

        internal GolemObjectTemplateIdentity(Transform root) => Register(root, "root");

        private void Register(Transform transform, string identity)
        {
            objects[transform] = identity + "/transform";
            Ordinals(transform.GetComponents<Collider>(), identity + "/collider/");
            Ordinals(transform.GetComponents<Collider2D>(), identity + "/collider2d/");
            Ordinals(transform.GetComponents<Rigidbody>(), identity + "/body/");
            for (var index = 0; index < transform.childCount; index++)
                Register(transform.GetChild(index), identity + "/child/" + index.ToString(CultureInfo.InvariantCulture));
        }

        private void Ordinals(Component[] components, string prefix)
        {
            for (var index = 0; index < components.Length; index++) objects[components[index]] = prefix + index.ToString(CultureInfo.InvariantCulture);
        }

        internal string Resolve(UnityEngine.Object value)
        {
            if (objects.TryGetValue(value, out var identity)) return identity;
            if (!(value is Component component) || !objects.TryGetValue(component.transform, out var transform))
                throw new InvalidOperationException(value.name + " is outside the template hierarchy.");
            var ordinal = Array.IndexOf(component.GetComponents(component.GetType()), component);
            identity = transform + "/component/" + component.GetType().FullName + "/" + ordinal.ToString(CultureInfo.InvariantCulture);
            objects.Add(value, identity);
            return identity;
        }
    }
}
