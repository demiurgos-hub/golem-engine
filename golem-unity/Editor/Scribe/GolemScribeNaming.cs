using System.Text;

namespace GolemEngine.Unity.Editor
{
    /// <summary>Naming helpers aligned with golem-bake schema naming.</summary>
    public static class GolemScribeNaming
    {
        /// <summary>Converts PascalCase or camelCase identifiers to snake_case.</summary>
        public static string ToSnakeCase(string value)
        {
            if (string.IsNullOrEmpty(value))
            {
                return string.Empty;
            }

            var builder = new StringBuilder(value.Length + 8);
            for (var i = 0; i < value.Length; i++)
            {
                var c = value[i];
                if (char.IsUpper(c) && i > 0)
                {
                    builder.Append('_');
                }
                builder.Append(char.ToLowerInvariant(c));
            }
            return builder.ToString();
        }

        /// <summary>Returns true when <paramref name="name"/> is a PascalCase C# identifier.</summary>
        public static bool IsPascalCaseIdentifier(string name)
        {
            if (string.IsNullOrEmpty(name) || !char.IsUpper(name[0]))
            {
                return false;
            }

            for (var i = 0; i < name.Length; i++)
            {
                if (!char.IsLetterOrDigit(name[i]))
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>Returns the entity schema file name for a PascalCase entity name.</summary>
        public static string EntitySchemaFileName(string entityName)
        {
            return ToSnakeCase(entityName) + ".yaml";
        }

        /// <summary>Returns the type/world schema file name for a PascalCase catalog type name.</summary>
        public static string CatalogSchemaFileName(string typeName)
        {
            return ToSnakeCase(typeName) + ".yaml";
        }

        /// <summary>Returns the project-root-relative catalog data file name for a type.</summary>
        public static string CatalogDataFileName(string typeName)
        {
            return ToSnakeCase(typeName) + ".golem.yaml";
        }
    }
}
