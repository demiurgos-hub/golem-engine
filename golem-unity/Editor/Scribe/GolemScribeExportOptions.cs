using System;
using System.Collections.Generic;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Optional authored identities for projects assembling saved export scenes.
    /// A supplied resolver must return a nonempty stable ID for every requested object.</summary>
    public sealed class GolemScribeExportOptions
    {
        public Func<UnityEngine.Object, string> StableObjectIdentity;
        public Func<UnityEngine.Object, string> StableAssetIdentity;
    }

    internal sealed class GolemScribeIdentityContext
    {
        private readonly GolemScribeExportOptions options;
        private readonly Dictionary<UnityEngine.Object, string> objects = new Dictionary<UnityEngine.Object, string>();
        private readonly Dictionary<UnityEngine.Object, string> assets = new Dictionary<UnityEngine.Object, string>();
        internal GolemScribeIdentityContext(GolemScribeExportOptions options) { this.options = options; }
        internal string Object(Component value, Func<string> fallback) => Resolve(objects, value, options?.StableObjectIdentity, fallback);
        internal string Asset(UnityEngine.Object value, Func<string> fallback) => Resolve(assets, value, options?.StableAssetIdentity, fallback);
        private static string Resolve(Dictionary<UnityEngine.Object, string> cache, UnityEngine.Object value,
            Func<UnityEngine.Object, string> resolver, Func<string> fallback)
        {
            if (!value) throw new InvalidOperationException("Cannot identify a missing source object or asset.");
            if (cache.TryGetValue(value, out var id)) return id;
            id = resolver == null ? fallback() : resolver(value);
            if (string.IsNullOrWhiteSpace(id)) throw new InvalidOperationException("Stable identity resolver returned no identity for " + value.name + ".");
            cache.Add(value, id); return id;
        }
    }
}
