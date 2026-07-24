using System;
using System.Collections.Generic;
using System.Linq;
using System.Reflection;
using UnityEngine;

namespace GolemEngine.Unity.Editor
{
    /// <summary>One validated GolemVar field ready for entity YAML emission.</summary>
    public sealed class GolemEntityVarModel
    {
        public string SnakeName;
        public int Tag;
        public string SchemaType;
        public string SyncYaml;
        public string FieldName;
    }

    /// <summary>Validated entity schema model produced from a <see cref="GolemEntityAttribute"/> type.</summary>
    public sealed class GolemEntitySchemaModel
    {
        public string EntityName;
        public bool Global;
        public bool Persistent = true;
        public string RelativePath;
        public readonly List<GolemEntityVarModel> Vars = new List<GolemEntityVarModel>();
        public readonly List<string> Errors = new List<string>();
    }

    /// <summary>Builds deterministic entity schema YAML from attributed MonoBehaviour types.</summary>
    public static class GolemEntitySchemaBuilder
    {
        private static readonly string[] Reserved2D = { "pos_x", "pos_y", "revision" };
        private static readonly string[] Reserved3D = { "pos_x", "pos_y", "pos_z", "revision" };

        /// <summary>Entity wire-tag offset: reserves entity_id plus position components.</summary>
        public static int EntityTagOffset(int dimensions)
        {
            return dimensions + 1;
        }

        /// <summary>User-slot tag that would collide with reserved revision proto field 1000.</summary>
        public static int ReservedRevisionUserTag(int dimensions)
        {
            return GolemScribeConstants.ReservedRevisionProtoField - EntityTagOffset(dimensions);
        }

        /// <summary>Builds a schema model from a component type. Does not read serialized field values.</summary>
        public static GolemEntitySchemaModel Build(Type componentType, int dimensions, string entitySchemaDir)
        {
            var model = new GolemEntitySchemaModel();
            if (componentType == null)
            {
                model.Errors.Add("Component type is required.");
                return model;
            }

            if (dimensions != 2 && dimensions != 3)
            {
                model.Errors.Add($"simulation.dimensions must be 2 or 3, got {dimensions}.");
                return model;
            }

            var entityAttr = componentType.GetCustomAttribute<GolemEntityAttribute>(inherit: false);
            if (entityAttr == null)
            {
                model.Errors.Add($"Type '{componentType.Name}' is missing [GolemEntity].");
                return model;
            }

            if (!GolemScribeNaming.IsPascalCaseIdentifier(entityAttr.Name))
            {
                model.Errors.Add(
                    $"Entity name '{entityAttr.Name}' on '{componentType.Name}' must be a PascalCase identifier.");
                return model;
            }

            model.EntityName = entityAttr.Name;
            model.Global = entityAttr.Global;
            model.Persistent = entityAttr.Persistent;
            model.RelativePath = CombineRelative(entitySchemaDir, GolemScribeNaming.EntitySchemaFileName(model.EntityName));

            var reserved = dimensions == 3 ? Reserved3D : Reserved2D;
            var seenTags = new Dictionary<int, string>();
            var seenNames = new Dictionary<string, string>(StringComparer.Ordinal);

            foreach (var field in EnumerateAttributedFields(componentType))
            {
                var varAttr = field.GetCustomAttribute<GolemVarAttribute>(inherit: false);
                if (varAttr == null)
                {
                    continue;
                }

                if (!field.IsPublic && field.GetCustomAttribute<SerializeField>() == null)
                {
                    model.Errors.Add(
                        $"Entity '{model.EntityName}' field '{field.Name}' must be public or [SerializeField] to use [GolemVar].");
                    continue;
                }

                if (field.IsStatic)
                {
                    model.Errors.Add($"Entity '{model.EntityName}' field '{field.Name}' cannot be static.");
                    continue;
                }

                if (!GolemScribeTypes.TryGetSchemaType(field.FieldType, out var schemaType))
                {
                    model.Errors.Add(
                        $"Entity '{model.EntityName}' field '{field.Name}' has unsupported type '{field.FieldType}'. " +
                        "GolemVar supports int, uint, long, ulong, float, double, bool, and string only.");
                    continue;
                }

                var syncYaml = GolemScribeTypes.ToSyncYaml(varAttr.Sync);
                if (syncYaml == null)
                {
                    model.Errors.Add($"Entity '{model.EntityName}' field '{field.Name}' has invalid GolemSync value.");
                    continue;
                }

                if (varAttr.Tag < 1)
                {
                    model.Errors.Add($"Entity '{model.EntityName}' field '{field.Name}': tag is required and must be >= 1.");
                    continue;
                }

                if (varAttr.Tag + EntityTagOffset(dimensions) == GolemScribeConstants.ReservedRevisionProtoField)
                {
                    model.Errors.Add(
                        $"Entity '{model.EntityName}' field '{field.Name}': tag {varAttr.Tag} maps to reserved revision proto field 1000 " +
                        $"(dimensions={dimensions}, offset={EntityTagOffset(dimensions)}).");
                    continue;
                }

                if (seenTags.TryGetValue(varAttr.Tag, out var previousField))
                {
                    model.Errors.Add(
                        $"Entity '{model.EntityName}': fields '{previousField}' and '{field.Name}' share tag {varAttr.Tag}.");
                    continue;
                }

                var snake = GolemScribeNaming.ToSnakeCase(field.Name);
                if (Array.IndexOf(reserved, snake) >= 0)
                {
                    model.Errors.Add(
                        $"Entity '{model.EntityName}' field '{field.Name}' maps to reserved var name '{snake}'.");
                    continue;
                }

                if (seenNames.TryGetValue(snake, out var previousName))
                {
                    model.Errors.Add(
                        $"Entity '{model.EntityName}': fields '{previousName}' and '{field.Name}' both map to '{snake}'.");
                    continue;
                }

                seenTags[varAttr.Tag] = field.Name;
                seenNames[snake] = field.Name;
                model.Vars.Add(new GolemEntityVarModel
                {
                    SnakeName = snake,
                    Tag = varAttr.Tag,
                    SchemaType = schemaType,
                    SyncYaml = syncYaml,
                    FieldName = field.Name
                });
            }

            model.Vars.Sort((a, b) => a.Tag.CompareTo(b.Tag));
            return model;
        }

        /// <summary>Emits deterministic entity schema YAML for a validated model.</summary>
        public static string BuildYaml(GolemEntitySchemaModel model)
        {
            if (model == null)
            {
                throw new ArgumentNullException(nameof(model));
            }

            var lines = new List<string>
            {
                "entity: " + GolemYamlWriter.FormatScalar(model.EntityName)
            };

            if (model.Global)
            {
                lines.Add("global: true");
            }

            if (!model.Persistent)
            {
                lines.Add("persistent: false");
            }

            lines.Add("vars:");
            if (model.Vars.Count == 0)
            {
                lines.Add("  {}");
            }
            else
            {
                foreach (var variable in model.Vars.OrderBy(v => v.Tag))
                {
                    lines.Add("  " + variable.SnakeName + ":");
                    lines.Add("    tag: " + GolemYamlWriter.FormatInt(variable.Tag));
                    lines.Add("    type: " + GolemYamlWriter.FormatScalar(variable.SchemaType));
                    lines.Add("    sync: " + GolemYamlWriter.FormatScalar(variable.SyncYaml));
                }
            }

            return GolemYamlWriter.BuildDocument(lines);
        }

        private static IEnumerable<FieldInfo> EnumerateAttributedFields(Type type)
        {
            for (var current = type; current != null && current != typeof(object); current = current.BaseType)
            {
                if (current == typeof(MonoBehaviour) || current == typeof(Behaviour) || current == typeof(Component))
                {
                    break;
                }

                foreach (var field in current.GetFields(BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic | BindingFlags.DeclaredOnly))
                {
                    if (field.GetCustomAttribute<GolemVarAttribute>(inherit: false) != null)
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
