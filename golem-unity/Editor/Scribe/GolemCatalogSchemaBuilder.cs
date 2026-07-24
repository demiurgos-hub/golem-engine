using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Reflection;
using System.Security.Cryptography;
using System.Text;
using UnityEditor;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>One validated catalog field ready for type schema and data emission.</summary>
    public sealed class GolemCatalogFieldModel
    {
        public string SnakeName;
        public int Tag;
        public string SchemaType;
        public string FieldName;
        public bool IsAssetRef;
        public FieldInfo Field;
    }

    /// <summary>Validated catalog schema model produced from a <see cref="GolemCatalogAttribute"/> type.</summary>
    public sealed class GolemCatalogSchemaModel
    {
        public string TypeName;
        public string SnakeName;
        public string KeyFieldName;
        public string KeySnakeName;
        public string SourceGuid;
        public string TypesRelativePath;
        public string WorldRelativePath;
        public string CatalogDataRelativePath;
        public readonly List<GolemCatalogFieldModel> Fields = new List<GolemCatalogFieldModel>();
        public readonly List<string> Errors = new List<string>();
    }

    /// <summary>One catalog data row with a deterministic sort key.</summary>
    public sealed class GolemCatalogRowModel
    {
        public string SortKey;
        public readonly List<(string snakeName, string yamlValue)> Values = new List<(string, string)>();
    }

    /// <summary>
    /// Builds deterministic catalog type/world/data YAML from attributed ScriptableObject types.
    /// Custom-type tags are direct protobuf field numbers (no entity tag offset).
    /// </summary>
    public static class GolemCatalogSchemaBuilder
    {
        /// <summary>
        /// Builds a catalog schema model from a ScriptableObject type.
        /// Does not scan assets; call <see cref="BuildRows"/> for data values.
        /// When <paramref name="projectRoot"/> is set, artifact paths (including world
        /// <c>source.file</c>) are validated with the shared project-relative containment contract.
        /// </summary>
        public static GolemCatalogSchemaModel Build(
            Type catalogType,
            string typesSchemaDir,
            string worldSchemaDir,
            string projectRoot = null)
        {
            var model = new GolemCatalogSchemaModel();
            if (catalogType == null)
            {
                model.Errors.Add("Catalog type is required.");
                return model;
            }

            if (!typeof(ScriptableObject).IsAssignableFrom(catalogType))
            {
                model.Errors.Add($"Catalog type '{catalogType.Name}' must inherit ScriptableObject.");
                return model;
            }

            var catalogAttr = catalogType.GetCustomAttribute<GolemCatalogAttribute>(inherit: false);
            if (catalogAttr == null)
            {
                model.Errors.Add($"Type '{catalogType.Name}' is missing [GolemCatalog].");
                return model;
            }

            if (!GolemScribeNaming.IsPascalCaseIdentifier(catalogType.Name))
            {
                model.Errors.Add(
                    $"Catalog type name '{catalogType.Name}' must be a PascalCase identifier.");
                return model;
            }

            if (string.IsNullOrWhiteSpace(catalogAttr.Key))
            {
                model.Errors.Add($"Catalog '{catalogType.Name}' must declare a non-empty key field name.");
                return model;
            }

            model.TypeName = catalogType.Name;
            model.SnakeName = GolemScribeNaming.ToSnakeCase(model.TypeName);
            model.KeyFieldName = catalogAttr.Key;
            model.SourceGuid = StableSourceGuid(catalogType);
            model.TypesRelativePath = CombineRelative(typesSchemaDir, GolemScribeNaming.CatalogSchemaFileName(model.TypeName));
            model.WorldRelativePath = CombineRelative(worldSchemaDir, GolemScribeNaming.CatalogSchemaFileName(model.TypeName));
            model.CatalogDataRelativePath =
                GolemScribeConstants.CatalogDataDirectory + "/" + GolemScribeNaming.CatalogDataFileName(model.TypeName);

            if (!string.IsNullOrWhiteSpace(projectRoot))
            {
                if (TryValidateArtifactPath(projectRoot, model.TypesRelativePath, "types schema", model.Errors, out var typesPath))
                {
                    model.TypesRelativePath = typesPath;
                }

                if (TryValidateArtifactPath(projectRoot, model.WorldRelativePath, "world schema", model.Errors, out var worldPath))
                {
                    model.WorldRelativePath = worldPath;
                }

                if (TryValidateArtifactPath(
                        projectRoot, model.CatalogDataRelativePath, "catalog data / source.file", model.Errors,
                        out var dataPath))
                {
                    model.CatalogDataRelativePath = dataPath;
                }
            }

            var seenTags = new Dictionary<int, string>();
            var seenNames = new Dictionary<string, string>(StringComparer.Ordinal);
            FieldInfo keyField = null;

            foreach (var field in EnumerateAttributedFields(catalogType))
            {
                var fieldAttr = field.GetCustomAttribute<GolemFieldAttribute>(inherit: false);
                var assetAttr = field.GetCustomAttribute<GolemAssetRefAttribute>(inherit: false);
                if (fieldAttr == null && assetAttr == null)
                {
                    continue;
                }

                if (fieldAttr != null && assetAttr != null)
                {
                    model.Errors.Add(
                        $"Catalog '{model.TypeName}' field '{field.Name}' cannot use both [GolemField] and [GolemAssetRef].");
                    continue;
                }

                if (!field.IsPublic && field.GetCustomAttribute<SerializeField>() == null)
                {
                    model.Errors.Add(
                        $"Catalog '{model.TypeName}' field '{field.Name}' must be public or [SerializeField].");
                    continue;
                }

                if (field.IsStatic)
                {
                    model.Errors.Add($"Catalog '{model.TypeName}' field '{field.Name}' cannot be static.");
                    continue;
                }

                var tag = fieldAttr != null ? fieldAttr.Tag : assetAttr.Tag;
                var isAssetRef = assetAttr != null;
                string schemaType;

                if (isAssetRef)
                {
                    if (!typeof(UnityEngine.Object).IsAssignableFrom(field.FieldType))
                    {
                        model.Errors.Add(
                            $"Catalog '{model.TypeName}' field '{field.Name}' uses [GolemAssetRef] but type '{field.FieldType}' is not a UnityEngine.Object.");
                        continue;
                    }

                    schemaType = "string";
                }
                else if (!GolemScribeTypes.TryGetSchemaType(field.FieldType, out schemaType))
                {
                    model.Errors.Add(
                        $"Catalog '{model.TypeName}' field '{field.Name}' has unsupported type '{field.FieldType}'. " +
                        "GolemField supports int, uint, long, ulong, float, double, bool, and string only.");
                    continue;
                }

                if (tag < 1)
                {
                    model.Errors.Add($"Catalog '{model.TypeName}' field '{field.Name}': tag is required and must be >= 1.");
                    continue;
                }

                // Direct custom-type tags: proto field number equals tag (no entity offset / revision slot).
                if (seenTags.TryGetValue(tag, out var previousField))
                {
                    model.Errors.Add(
                        $"Catalog '{model.TypeName}': fields '{previousField}' and '{field.Name}' share tag {tag}.");
                    continue;
                }

                var snake = GolemScribeNaming.ToSnakeCase(field.Name);
                // Custom-type fields are a separate protobuf message from the world catalog wrapper
                // (synthetic "items"), so names like "items" are allowed. Reject only names that
                // cannot be emitted as Go/protobuf field identifiers.
                if (!IsValidCustomTypeFieldName(snake))
                {
                    model.Errors.Add(
                        $"Catalog '{model.TypeName}' field '{field.Name}' maps to invalid schema name '{snake}'. " +
                        "Custom-type field names must be non-empty snake_case identifiers starting with a letter.");
                    continue;
                }

                if (seenNames.TryGetValue(snake, out var previousName))
                {
                    model.Errors.Add(
                        $"Catalog '{model.TypeName}': fields '{previousName}' and '{field.Name}' both map to '{snake}'.");
                    continue;
                }

                seenTags[tag] = field.Name;
                seenNames[snake] = field.Name;
                var fieldModel = new GolemCatalogFieldModel
                {
                    SnakeName = snake,
                    Tag = tag,
                    SchemaType = schemaType,
                    FieldName = field.Name,
                    IsAssetRef = isAssetRef,
                    Field = field
                };
                model.Fields.Add(fieldModel);

                if (string.Equals(field.Name, model.KeyFieldName, StringComparison.Ordinal))
                {
                    keyField = field;
                    model.KeySnakeName = snake;
                }
            }

            if (keyField == null)
            {
                model.Errors.Add(
                    $"Catalog '{model.TypeName}': key field '{model.KeyFieldName}' was not found with [GolemField] or [GolemAssetRef].");
            }

            model.Fields.Sort((a, b) => a.Tag.CompareTo(b.Tag));
            return model;
        }

        /// <summary>Emits deterministic custom-type schema YAML.</summary>
        public static string BuildTypeYaml(GolemCatalogSchemaModel model)
        {
            if (model == null)
            {
                throw new ArgumentNullException(nameof(model));
            }

            var lines = new List<string>
            {
                "type: " + GolemYamlWriter.FormatScalar(model.TypeName),
                "fields:"
            };

            // Valid keyed catalogs always include at least the key field. Empty `{}` is defensive for
            // incomplete models/fixtures and is not emitted by a successful CollectCatalogs export.
            if (model.Fields.Count == 0)
            {
                lines.Add("  {}");
            }
            else
            {
                foreach (var field in model.Fields.OrderBy(f => f.Tag))
                {
                    lines.Add("  " + field.SnakeName + ":");
                    lines.Add("    tag: " + GolemYamlWriter.FormatInt(field.Tag));
                    lines.Add("    type: " + GolemYamlWriter.FormatScalar(field.SchemaType));
                }
            }

            return GolemYamlWriter.BuildDocument(lines);
        }

        /// <summary>
        /// Emits deterministic catalog world schema YAML.
        /// Bake generates <c>Load{TypeName}Data</c>; application code must load and store/publish it.
        /// </summary>
        public static string BuildWorldYaml(GolemCatalogSchemaModel model)
        {
            if (model == null)
            {
                throw new ArgumentNullException(nameof(model));
            }

            var lines = new List<string>
            {
                "world: " + GolemYamlWriter.FormatScalar(model.TypeName),
                "source:",
                "  format: catalog",
                "  file: " + GolemYamlWriter.FormatScalar(model.CatalogDataRelativePath),
                "  type: " + GolemYamlWriter.FormatScalar(model.TypeName),
                "  key: " + GolemYamlWriter.FormatScalar(model.KeySnakeName)
            };
            return GolemYamlWriter.BuildDocument(lines);
        }

        /// <summary>
        /// Builds catalog data rows from ScriptableObject instances.
        /// Validates missing/duplicate keys and asset references.
        /// </summary>
        public static void BuildRows(
            GolemCatalogSchemaModel model,
            IEnumerable<ScriptableObject> assets,
            out List<GolemCatalogRowModel> rows,
            out List<string> errors)
        {
            rows = new List<GolemCatalogRowModel>();
            errors = new List<string>();
            if (model == null)
            {
                errors.Add("Catalog model is required.");
                return;
            }

            var keyField = model.Fields.FirstOrDefault(f =>
                string.Equals(f.FieldName, model.KeyFieldName, StringComparison.Ordinal));
            if (keyField == null)
            {
                errors.Add($"Catalog '{model.TypeName}' is missing key field '{model.KeyFieldName}'.");
                return;
            }

            var seenKeys = new Dictionary<string, string>(StringComparer.Ordinal);
            foreach (var asset in assets ?? Array.Empty<ScriptableObject>())
            {
                if (asset == null)
                {
                    continue;
                }

                var assetPath = AssetDatabase.GetAssetPath(asset);
                if (string.IsNullOrEmpty(assetPath))
                {
                    assetPath = asset.name;
                }

                if (!TryReadFieldYaml(keyField, asset, out var keyYaml, out var keyError))
                {
                    errors.Add($"{assetPath}: {keyError}");
                    continue;
                }

                if (string.IsNullOrEmpty(keyYaml) || keyYaml == "\"\"")
                {
                    errors.Add($"{assetPath}: catalog key '{model.KeySnakeName}' is missing or empty.");
                    continue;
                }

                // Canonical formatted key used for duplicate detection (type-stable string form).
                var sortKey = UnwrapScalarForSort(keyYaml);
                if (seenKeys.TryGetValue(sortKey, out var previousPath))
                {
                    errors.Add(
                        $"Catalog '{model.TypeName}': duplicate key '{sortKey}' in '{previousPath}' and '{assetPath}'.");
                    continue;
                }

                var row = new GolemCatalogRowModel { SortKey = sortKey };
                var rowOk = true;
                foreach (var field in model.Fields.OrderBy(f => f.Tag))
                {
                    if (!TryReadFieldYaml(field, asset, out var yamlValue, out var fieldError))
                    {
                        errors.Add($"{assetPath}: {fieldError}");
                        rowOk = false;
                        break;
                    }

                    row.Values.Add((field.SnakeName, yamlValue));
                }

                // Reserve the key only after the full row validates so a bad row cannot poison a later valid asset.
                if (rowOk)
                {
                    seenKeys[sortKey] = assetPath;
                    rows.Add(row);
                }
            }

            rows.Sort((a, b) => CompareCatalogKeys(a.SortKey, b.SortKey, keyField.SchemaType));
        }

        /// <summary>Emits a deterministic YAML sequence-of-maps catalog data document.</summary>
        public static string BuildCatalogDataYaml(IReadOnlyList<GolemCatalogRowModel> rows)
        {
            var lines = new List<string>();
            if (rows == null || rows.Count == 0)
            {
                lines.Add("[]");
                return GolemYamlWriter.BuildDocument(lines);
            }

            foreach (var row in rows)
            {
                var first = true;
                foreach (var (snakeName, yamlValue) in row.Values)
                {
                    if (first)
                    {
                        lines.Add("- " + snakeName + ": " + yamlValue);
                        first = false;
                    }
                    else
                    {
                        lines.Add("  " + snakeName + ": " + yamlValue);
                    }
                }
            }

            return GolemYamlWriter.BuildDocument(lines);
        }

        /// <summary>
        /// Deterministic synthetic 32-hex source ID for class-level catalog artifacts.
        /// This is not a Unity asset GUID: type/world/data artifacts are owned by the catalog
        /// class (many ScriptableObject assets), so identity is derived from the type full name.
        /// </summary>
        public static string StableSourceGuid(Type catalogType)
        {
            var input = "golem-catalog:" + (catalogType?.FullName ?? string.Empty);
            var bytes = Encoding.UTF8.GetBytes(input);
            using (var sha = SHA256.Create())
            {
                var hash = sha.ComputeHash(bytes);
                var builder = new StringBuilder(32);
                for (var i = 0; i < 16; i++)
                {
                    builder.Append(hash[i].ToString("x2", CultureInfo.InvariantCulture));
                }
                return builder.ToString();
            }
        }

        /// <summary>
        /// True when <paramref name="snakeName"/> is a valid custom-type field name for Go/protobuf emission.
        /// Does not reserve world synthetic names such as <c>items</c> (separate message).
        /// </summary>
        public static bool IsValidCustomTypeFieldName(string snakeName)
        {
            if (string.IsNullOrEmpty(snakeName) || !char.IsLetter(snakeName[0]))
            {
                return false;
            }

            for (var i = 0; i < snakeName.Length; i++)
            {
                var c = snakeName[i];
                if (!char.IsLetterOrDigit(c) && c != '_')
                {
                    return false;
                }
            }

            return true;
        }

        /// <summary>
        /// Compares catalog keys for deterministic row ordering. Numeric schema types use numeric order;
        /// duplicate detection still uses the canonical formatted string key.
        /// </summary>
        public static int CompareCatalogKeys(string left, string right, string schemaType)
        {
            left = left ?? string.Empty;
            right = right ?? string.Empty;
            switch (schemaType)
            {
                case "int32":
                case "int64":
                    if (long.TryParse(left, NumberStyles.Integer, CultureInfo.InvariantCulture, out var li) &&
                        long.TryParse(right, NumberStyles.Integer, CultureInfo.InvariantCulture, out var ri))
                    {
                        return li.CompareTo(ri);
                    }
                    break;
                case "uint32":
                case "uint64":
                    if (ulong.TryParse(left, NumberStyles.Integer, CultureInfo.InvariantCulture, out var lu) &&
                        ulong.TryParse(right, NumberStyles.Integer, CultureInfo.InvariantCulture, out var ru))
                    {
                        return lu.CompareTo(ru);
                    }
                    break;
                case "float":
                case "double":
                    if (double.TryParse(left, NumberStyles.Float, CultureInfo.InvariantCulture, out var lf) &&
                        double.TryParse(right, NumberStyles.Float, CultureInfo.InvariantCulture, out var rf))
                    {
                        return lf.CompareTo(rf);
                    }
                    break;
            }

            return string.Compare(left, right, StringComparison.Ordinal);
        }

        private static bool TryValidateArtifactPath(
            string projectRoot,
            string relativePath,
            string label,
            List<string> errors,
            out string normalized)
        {
            if (!GolemScribeArtifacts.TryResolveContainedPath(
                    projectRoot, relativePath, out _, out normalized, out var pathError))
            {
                errors.Add($"Catalog {label} path is not project-relative/contained: {pathError}");
                normalized = null;
                return false;
            }

            return true;
        }

        private static bool TryReadFieldYaml(
            GolemCatalogFieldModel field,
            ScriptableObject asset,
            out string yamlValue,
            out string error)
        {
            yamlValue = null;
            error = null;
            object raw;
            try
            {
                raw = field.Field.GetValue(asset);
            }
            catch (Exception ex)
            {
                error = $"failed to read field '{field.FieldName}': {ex.Message}";
                return false;
            }

            if (field.IsAssetRef)
            {
                if (raw == null)
                {
                    yamlValue = "\"\"";
                    return true;
                }

                var unityObject = raw as UnityEngine.Object;
                if (unityObject == null)
                {
                    error = $"field '{field.FieldName}' asset reference is not a UnityEngine.Object.";
                    return false;
                }

                var path = AssetDatabase.GetAssetPath(unityObject);
                if (string.IsNullOrEmpty(path))
                {
                    error =
                        $"field '{field.FieldName}' asset reference does not resolve to a project asset GUID.";
                    return false;
                }

                var guid = AssetDatabase.AssetPathToGUID(path);
                if (string.IsNullOrEmpty(guid) || guid.Length != 32)
                {
                    error =
                        $"field '{field.FieldName}' asset reference produced an invalid GUID.";
                    return false;
                }

                yamlValue = GolemYamlWriter.FormatScalar(guid.ToLowerInvariant());
                return true;
            }

            if (raw == null)
            {
                if (field.Field.FieldType == typeof(string))
                {
                    yamlValue = "\"\"";
                    return true;
                }

                error = $"field '{field.FieldName}' has a null value.";
                return false;
            }

            if (raw is int i)
            {
                yamlValue = GolemYamlWriter.FormatInt(i);
                return true;
            }

            if (raw is uint ui)
            {
                yamlValue = GolemYamlWriter.FormatUInt(ui);
                return true;
            }

            if (raw is long l)
            {
                yamlValue = GolemYamlWriter.FormatLong(l);
                return true;
            }

            if (raw is ulong ul)
            {
                yamlValue = GolemYamlWriter.FormatULong(ul);
                return true;
            }

            if (raw is float f)
            {
                yamlValue = GolemYamlWriter.FormatFloat(f);
                return true;
            }

            if (raw is double d)
            {
                yamlValue = GolemYamlWriter.FormatDouble(d);
                return true;
            }

            if (raw is bool b)
            {
                yamlValue = GolemYamlWriter.FormatBool(b);
                return true;
            }

            if (raw is string s)
            {
                yamlValue = GolemYamlWriter.FormatScalar(s);
                return true;
            }

            error = $"field '{field.FieldName}' has unsupported runtime value type '{raw.GetType()}'.";
            return false;
        }

        private static string UnwrapScalarForSort(string yamlToken)
        {
            if (GolemYamlWriter.TryParseScalarToken(yamlToken, out var value))
            {
                return value ?? string.Empty;
            }

            return yamlToken ?? string.Empty;
        }

        private static IEnumerable<FieldInfo> EnumerateAttributedFields(Type type)
        {
            for (var current = type; current != null && current != typeof(object); current = current.BaseType)
            {
                if (current == typeof(ScriptableObject) || current == typeof(UnityEngine.Object))
                {
                    break;
                }

                foreach (var field in current.GetFields(
                             BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic |
                             BindingFlags.DeclaredOnly))
                {
                    if (field.GetCustomAttribute<GolemFieldAttribute>(inherit: false) != null ||
                        field.GetCustomAttribute<GolemAssetRefAttribute>(inherit: false) != null)
                    {
                        yield return field;
                    }
                }
            }
        }

        private static string CombineRelative(string directory, string fileName)
        {
            var dir = (directory ?? string.Empty).Replace('\\', '/').Trim('/');
            if (string.IsNullOrEmpty(dir))
            {
                return fileName;
            }
            return dir + "/" + fileName;
        }
    }
}
