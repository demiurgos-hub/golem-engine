using System;
using System.Collections.Generic;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Maps supported C# scalar field types to Golem schema type names.</summary>
    public static class GolemScribeTypes
    {
        private static readonly Dictionary<Type, string> SchemaTypes = new Dictionary<Type, string>
        {
            { typeof(int), "int32" },
            { typeof(uint), "uint32" },
            { typeof(long), "int64" },
            { typeof(ulong), "uint64" },
            { typeof(float), "float" },
            { typeof(double), "double" },
            { typeof(bool), "bool" },
            { typeof(string), "string" }
        };

        /// <summary>Tries to map a C# field type to a Golem entity schema scalar type.</summary>
        public static bool TryGetSchemaType(Type fieldType, out string schemaType)
        {
            return SchemaTypes.TryGetValue(fieldType, out schemaType);
        }

        /// <summary>Returns the lowercase YAML sync value for a <see cref="GolemSync"/> mode.</summary>
        public static string ToSyncYaml(GolemSync sync)
        {
            switch (sync)
            {
                case GolemSync.Tick:
                    return "tick";
                case GolemSync.Once:
                    return "once";
                default:
                    return null;
            }
        }
    }
}
