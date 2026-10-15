using System;
using System.Collections.Generic;
using System.Linq;
using System.Reflection;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    internal static class GolemScribeFieldInspection
    {
        internal readonly struct Visibility
        {
            public readonly bool IsSerialized;
            public readonly bool IsStatic;

            public Visibility(bool isSerialized, bool isStatic)
            {
                IsSerialized = isSerialized;
                IsStatic = isStatic;
            }
        }

        internal static IEnumerable<FieldInfo> Enumerate(
            Type type, IReadOnlyCollection<Type> stopBefore, params Type[] attributeTypes)
        {
            for (var current = type; current != null && current != typeof(object); current = current.BaseType)
            {
                if (stopBefore.Contains(current))
                {
                    yield break;
                }

                foreach (var field in current.GetFields(
                             BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic | BindingFlags.DeclaredOnly))
                {
                    if (attributeTypes.Any(attribute => field.IsDefined(attribute, inherit: false)))
                    {
                        yield return field;
                    }
                }
            }
        }

        // This is Scribe's existing visibility contract, not Unity's complete serialization rules.
        internal static Visibility CheckVisibility(FieldInfo field)
        {
            return new Visibility(field.IsPublic || field.GetCustomAttribute<SerializeField>() != null, field.IsStatic);
        }
    }

    internal sealed class GolemScribeFieldClaims
    {
        private readonly Dictionary<int, string> _tags = new Dictionary<int, string>();
        private readonly Dictionary<string, string> _names = new Dictionary<string, string>(StringComparer.Ordinal);

        internal bool CheckTag(int tag, out string previousField) => _tags.TryGetValue(tag, out previousField);
        internal bool CheckName(string name, out string previousField) => _names.TryGetValue(name, out previousField);

        internal void Commit(int tag, string name, string fieldName)
        {
            _tags.Add(tag, fieldName);
            _names.Add(name, fieldName);
        }
    }
}
