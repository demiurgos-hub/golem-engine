using System;
using System.Collections;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using System.Text.RegularExpressions;

namespace GolemEngine.Unity.Editor
{
    /// <summary>One explicitly selected portable content field; defaults apply only to absent values.</summary>
    public sealed class GolemContentField
    {
        public string Name;
        public string Type;
        public bool Optional;
        public bool HasDefault;
        public object Default;

        public GolemContentField(string name, string type, bool optional = false)
        { Name = name; Type = type; Optional = optional; }

        public GolemContentField WithDefault(object value)
        { HasDefault = true; Default = value; return this; }
    }

    /// <summary>A content record, enum or flags declaration, independent of replication codecs.</summary>
    public sealed class GolemContentType
    {
        public string Name;
        public string Kind = "record";
        public bool Definition;
        public string Entity;
        public readonly List<GolemContentField> Fields = new List<GolemContentField>();
        public readonly List<string> Values = new List<string>();
    }

    /// <summary>Unity provenance is diagnostic identity, separate from the authored definition ID.</summary>
    public sealed class GolemContentSource
    {
        public string Guid;
        public string LocalId;
        public string Path;
        public string Key => Guid + ":" + LocalId;
    }

    /// <summary>Captured authored data, before typed references and defaults have been validated.</summary>
    public sealed class GolemContentDefinition
    {
        public string Id;
        public string Type;
        public string Factory;
        public GolemContentSource Source;
        public IDictionary<string, object> Values = new Dictionary<string, object>(StringComparer.Ordinal);
    }

    /// <summary>A deferred selected-source reference; preparing the document never discovers new sources.</summary>
    public sealed class GolemContentReference
    {
        public readonly string SourceKey;
        public readonly string ExpectedType;
        public GolemContentReference(string sourceKey, string expectedType)
        { SourceKey = sourceKey; ExpectedType = expectedType; }
    }

    /// <summary>An explicitly supplied imported bundle and its expected semantic hash.</summary>
    public sealed class GolemContentImport
    {
        public string Hash;
        public string BundleJson;
    }

    /// <summary>Validated deterministic content source artifacts ready for one Scribe transaction.</summary>
    public sealed class GolemContentPrepared
    {
        public string SchemaJson { get; internal set; }
        public string BundleJson { get; internal set; }
        public IReadOnlyList<string> EntityTypes { get; internal set; }
    }

    /// <summary>Pure bounded content validation and JSON emission; no Unity discovery or filesystem access.</summary>
    public static class GolemContentDocument
    {
        public const string Generator = "Golem Scribe content v1";
        public const string OwnershipPrefix = "{\"generator\":\"Golem Scribe content v1\",";
        public const int MaximumDefinitions = 4096;
        public const int MaximumDepth = 32;
        public const int MaximumCollectionLength = 10000;
        public const int MaximumDocumentBytes = 16 * 1024 * 1024;
        private static readonly Regex Name = new Regex("^[A-Za-z_][A-Za-z0-9_]*$");
        private static readonly Regex Identity = new Regex("^[A-Za-z0-9_][A-Za-z0-9_.-]*/[A-Za-z_][A-Za-z0-9_]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$");
        private static readonly HashSet<string> Scalars = new HashSet<string>(new[]
        { "bool", "string", "int32", "uint32", "int64", "uint64", "float32", "float64" }, StringComparer.Ordinal);

        /// <summary>Recognizes only the exact Scribe content JSON ownership signature.</summary>
        public static bool IsOwned(string text) => text != null && text.StartsWith(OwnershipPrefix, StringComparison.Ordinal);

        /// <summary>Validates a complete selected set and returns independent deterministic source bytes.</summary>
        public static GolemContentPrepared Prepare(IEnumerable<GolemContentType> declarations,
            IEnumerable<GolemContentDefinition> definitions, IEnumerable<GolemContentImport> imports = null)
        {
            var types = new Dictionary<string, GolemContentType>(StringComparer.Ordinal);
            foreach (var type in declarations ?? throw new ArgumentNullException(nameof(declarations)))
            {
                if (type == null || !ValidName(type.Name) || Scalars.Contains(type.Name)) Fail("schema", "type name must be an identifier distinct from scalar names");
                if (types.TryGetValue(type.Name, out var prior))
                {
                    if (GolemContentJson.Write(TypeObject(prior)) != GolemContentJson.Write(TypeObject(type)))
                        Fail(type.Name, "conflicting type declarations");
                }
                else types.Add(type.Name, type);
                if (types.Count > 256) Fail("schema", "type count exceeds 256");
            }
            if (types.Count == 0) Fail("schema", "at least one type is required");
            ValidateTypes(types);
            var rows = (definitions ?? throw new ArgumentNullException(nameof(definitions))).Take(MaximumDefinitions + 1).ToList();
            if (rows.Count > MaximumDefinitions) Fail("definitions", "too many selected definitions");
            var targets = new Dictionary<string, string>(StringComparer.Ordinal);
            var sources = new Dictionary<string, string>(StringComparer.Ordinal);
            var importHashes = new HashSet<string>(StringComparer.Ordinal);
            foreach (var import in imports ?? Array.Empty<GolemContentImport>())
            {
                if (import == null || !Regex.IsMatch(import.Hash ?? "", "^[0-9a-f]{64}$")) Fail("imports", "expected semantic SHA-256 hash is required");
                if (!importHashes.Add(import.Hash)) Fail("imports", "duplicate imported hash " + import.Hash);
                if (importHashes.Count > 64) Fail("imports", "import count exceeds 64");
                var bundle = GolemContentJson.Parse(import.BundleJson) as IDictionary<string, object>;
                if (bundle == null || !bundle.TryGetValue("version", out var version) || !(version is decimal) || (decimal)version != 1)
                    Fail("imports", "expected a version-1 content bundle");
                if (!bundle.TryGetValue("definitions", out var raw) || !(raw is IList<object> entries)) Fail("imports", "definitions array is required");
                else foreach (var entry in entries)
                {
                    var row = entry as IDictionary<string, object>;
                    if (row == null) Fail("imports", "definition must be an object");
                    var id = ReadString(row, "id"); var type = ReadString(row, "type");
                    if (!types.TryGetValue(type, out var declared) || !declared.Definition) Fail(id, "imported definition type must be explicitly declared in this schema");
                    row.TryGetValue("factory", out var factory);
                    if (factory != null && !(factory is string)) Fail(id, "factory key must be a string");
                    ValidateFactory(declared, factory as string, id);
                    AddTarget(targets, id, type, "import");
                    if (row.TryGetValue("source", out var src) && src is IDictionary<string, object> provenance)
                        AddSource(sources, ReadString(provenance, "guid") + ":" + ReadString(provenance, "local_id"), id);
                }
            }
            foreach (var row in rows)
            {
                if (row == null) Fail("definitions", "null definition");
                var path = Location(row);
                if (!types.TryGetValue(row.Type ?? "", out var type) || !type.Definition) Fail(path, "type is not a declared definition: " + row.Type);
                ValidateFactory(type, row.Factory, path);
                AddTarget(targets, row.Id, row.Type, path);
                if (row.Source != null) AddSource(sources, row.Source.Key, row.Id);
            }
            var defaultBudget = new Budget(4 * 1024 * 1024);
            foreach (var type in types.Values)
                foreach (var field in type.Fields.Where(f => f.HasDefault))
                    Normalize(field.Default, field.Type, field.Optional, types, null, sources, type.Name + "." + field.Name + ".default", 0, defaultBudget);
            var normalized = new List<object>();
            var valueBudget = new Budget(MaximumDocumentBytes);
            foreach (var row in rows.OrderBy(d => d.Id, StringComparer.Ordinal))
            {
                var obj = Object("id", row.Id, "type", row.Type, "values",
                    Normalize(row.Values, row.Type, false, types, targets, sources, Location(row), 0, valueBudget));
                if (!string.IsNullOrEmpty(row.Factory)) obj.Add("factory", row.Factory);
                if (row.Source != null) obj.Add("source", Object("guid", row.Source.Guid, "local_id", row.Source.LocalId, "path", row.Source.Path));
                normalized.Add(obj);
            }
            var schema = Object("version", 1, "types", types.Values.OrderBy(t => t.Name, StringComparer.Ordinal).Select(TypeObject).ToList());
            var output = Object("version", 1, "definitions", normalized);
            if (importHashes.Count != 0) output.Add("imports", importHashes.OrderBy(h => h, StringComparer.Ordinal).Select(h => Object("hash", h)).ToList());
            var schemaJson = OwnedJson(schema);
            if (Encoding.UTF8.GetByteCount(schemaJson) > 4 * 1024 * 1024) Fail("schema", "schema exceeds 4 MiB");
            return new GolemContentPrepared
            {
                SchemaJson = schemaJson, BundleJson = OwnedJson(output),
                EntityTypes = types.Values.Where(t => !string.IsNullOrEmpty(t.Entity)).Select(t => t.Entity).Distinct().OrderBy(n => n, StringComparer.Ordinal).ToArray()
            };
        }

        private static string OwnedJson(IDictionary<string, object> value)
        {
            var json = GolemContentJson.Write(value);
            var output = OwnershipPrefix + json.Substring(1) + "\n";
            if (Encoding.UTF8.GetByteCount(output) > MaximumDocumentBytes) Fail("document", "output exceeds byte limit");
            return output;
        }

        private static void ValidateTypes(Dictionary<string, GolemContentType> types)
        {
            foreach (var type in types.Values)
            {
                if (type.Fields.Count > 256 || type.Values.Count > 1024) Fail(type.Name, "schema member limit exceeded");
                if (type.Kind != "record" && type.Kind != "enum" && type.Kind != "flags") Fail(type.Name, "unsupported kind " + type.Kind);
                if (type.Kind != "record")
                {
                    if (type.Definition || type.Fields.Count != 0 || type.Entity != null) Fail(type.Name, "enum/flags cannot be a definition or have fields/entity");
                    if (type.Values.Count == 0 || type.Values.Any(v => !ValidName(v)) || type.Values.Distinct(StringComparer.Ordinal).Count() != type.Values.Count)
                        Fail(type.Name, "enum/flags symbols must be nonempty unique identifiers");
                }
                else if (type.Values.Count != 0) Fail(type.Name, "record cannot declare enum symbols");
                if (type.Entity != null && (!type.Definition || !ValidName(type.Entity))) Fail(type.Name, "entity binding requires a definition and an existing entity name");
                var names = new HashSet<string>(StringComparer.Ordinal);
                foreach (var field in type.Fields)
                {
                    if (field == null || !ValidName(field.Name) || !names.Add(field.Name)) Fail(type.Name, "duplicate or invalid field name");
                    ValidateFieldType(field.Type, types, type.Name + "." + field.Name, 0);
                }
            }
            var complete = new HashSet<string>(StringComparer.Ordinal);
            foreach (var type in types.Values)
                InlineDependencies(type.Name, types, new HashSet<string>(StringComparer.Ordinal), complete, 0);
        }

        private static void ValidateFieldType(string type, Dictionary<string, GolemContentType> types, string path, int depth)
        {
            if (depth > 1) Fail(path, "nested lists are unsupported; use a named record");
            if (type == null) Fail(path, "field type is required");
            if (type.StartsWith("[]", StringComparison.Ordinal)) { ValidateFieldType(type.Substring(2), types, path, depth + 1); return; }
            if (type.StartsWith("ref:", StringComparison.Ordinal))
            {
                if (!types.TryGetValue(type.Substring(4), out var target) || !target.Definition) Fail(path, "reference target must be a declared definition");
                return;
            }
            if (types.TryGetValue(type, out var named) && named.Definition) Fail(path, "definition must be referenced using ref:" + type);
            if (!Scalars.Contains(type) && !types.ContainsKey(type)) Fail(path, "unknown or unsupported type " + type);
        }

        private static void InlineDependencies(string name, Dictionary<string, GolemContentType> types, HashSet<string> active, HashSet<string> complete, int depth)
        {
            if (complete.Contains(name)) return;
            if (depth > MaximumDepth || !active.Add(name)) Fail(name, "inline record cycle or excessive nesting; use typed definition references");
            foreach (var field in types[name].Fields)
            {
                var type = field.Type;
                while (type.StartsWith("[]", StringComparison.Ordinal)) type = type.Substring(2);
                if (types.TryGetValue(type, out var nested) && nested.Kind == "record") InlineDependencies(type, types, active, complete, depth + 1);
            }
            active.Remove(name);
            complete.Add(name);
        }

        private static object Normalize(object value, string type, bool optional, Dictionary<string, GolemContentType> types,
            Dictionary<string, string> targets, Dictionary<string, string> sources, string path, int depth, Budget budget)
        {
            if (depth > MaximumDepth) Fail(path, "value nesting exceeds limit");
            budget.Spend(value is string text ? Encoding.UTF8.GetByteCount(text) + 8 : 16, path);
            if (value == null) { if (optional) return null; Fail(path, "required value is null"); }
            if (type.StartsWith("[]", StringComparison.Ordinal))
            {
                if (!(value is IEnumerable sequence) || value is string || value is IDictionary) Fail(path, "expected an array");
                var result = new List<object>();
                foreach (var item in (IEnumerable)value)
                {
                    if (result.Count >= MaximumCollectionLength) Fail(path, "collection exceeds limit");
                    result.Add(Normalize(item, type.Substring(2), false, types, targets, sources, path + "[" + result.Count + "]", depth + 1, budget));
                }
                return result;
            }
            if (type.StartsWith("ref:", StringComparison.Ordinal))
            {
                var expected = type.Substring(4);
                string id;
                if (value is GolemContentReference reference)
                {
                    if (targets == null) Fail(path, "schema defaults require authored reference IDs, not Unity source references");
                    if (reference.ExpectedType != expected) Fail(path, "adapter reference type does not match schema");
                    if (!sources.TryGetValue(reference.SourceKey ?? "", out id)) Fail(path, "referenced source is outside explicit roots/imports: " + reference.SourceKey);
                }
                else id = value as string;
                if (id == null || id.Length > 512 || !Identity.IsMatch(id)) Fail(path, "reference must be a namespace/Type/key identity");
                var kind = id.Split('/')[1];
                if (kind != expected) Fail(path, "reference expects " + expected + ", found " + kind + " at " + id);
                if (targets != null)
                {
                    if (!targets.TryGetValue(id, out var actual)) Fail(path, "missing referenced definition " + id);
                    else if (actual != expected) Fail(path, "reference expects " + expected + ", found " + actual + " at " + id);
                }
                return id;
            }
            if (Scalars.Contains(type)) return Scalar(value, type, path);
            var declaration = types[type];
            if (declaration.Kind == "enum")
            {
                if (!(value is string symbol) || !declaration.Values.Contains(symbol)) Fail(path, "unknown " + type + " enum symbol");
                return value;
            }
            if (declaration.Kind == "flags")
            {
                if (!(value is IEnumerable flags) || value is string) Fail(path, "flags must be an array of symbols");
                var names = new SortedSet<string>(StringComparer.Ordinal);
                foreach (var flag in (IEnumerable)value)
                {
                    if (!(flag is string name) || !declaration.Values.Contains(name) || !names.Add(name)) Fail(path, "unknown or duplicate flag symbol");
                    budget.Spend(Encoding.UTF8.GetByteCount((string)flag) + 8, path);
                }
                return names.ToList();
            }
            if (!(value is IDictionary<string, object> record)) Fail(path, "expected a record");
            var input = (IDictionary<string, object>)value;
            var output = new Dictionary<string, object>(StringComparer.Ordinal);
            foreach (var key in input.Keys)
                if (!declaration.Fields.Any(f => f.Name == key)) Fail(path + "." + key, "unknown field");
            foreach (var field in declaration.Fields)
            {
                if (!input.TryGetValue(field.Name, out var fieldValue))
                {
                    if (field.HasDefault) fieldValue = field.Default;
                    else if (field.Optional) continue;
                    else { Fail(path + "." + field.Name, "required field is absent"); }
                }
                output.Add(field.Name, Normalize(fieldValue, field.Type, field.Optional, types, targets, sources, path + "." + field.Name, depth + 1, budget));
            }
            return output;
        }

        private static object Scalar(object value, string type, string path)
        {
            if (type == "string")
            {
                if (!(value is string)) Fail(path, "expected string");
                if (Encoding.UTF8.GetByteCount((string)value) > 1024 * 1024) Fail(path, "string exceeds 1 MiB");
                return value;
            }
            if (type == "bool") { if (value is bool) return value; Fail(path, "expected bool"); }
            if (!(value is byte || value is sbyte || value is short || value is ushort || value is int || value is uint || value is long || value is ulong || value is float || value is double || value is decimal))
                Fail(path, "expected numeric value");
            try
            {
                if (type == "float32" || type == "float64")
                {
                    var number = Convert.ToDouble(value, CultureInfo.InvariantCulture);
                    if (double.IsNaN(number) || double.IsInfinity(number)) Fail(path, "number must be finite");
                    if (type == "float32")
                    {
                        var single = (float)number;
                        if (float.IsInfinity(single)) Fail(path, "number exceeds float32 range");
                        return single;
                    }
                    return number;
                }
                if (value is float || value is double || value is decimal) Fail(path, "integer fields require integer values without numeric coercion");
                var integer = Convert.ToDecimal(value, CultureInfo.InvariantCulture);
                if (integer != decimal.Truncate(integer)) Fail(path, "expected an integer");
                switch (type)
                {
                    case "int32": return checked((int)integer);
                    case "uint32": return checked((uint)integer);
                    case "int64": return checked((long)integer);
                    case "uint64": return checked((ulong)integer);
                }
            }
            catch (OverflowException) { Fail(path, "number exceeds " + type + " range"); }
            throw new InvalidOperationException(path + ": unsupported scalar " + type);
        }

        private static IDictionary<string, object> TypeObject(GolemContentType type)
        {
            var obj = Object("name", type.Name, "kind", type.Kind, "definition", type.Definition);
            if (type.Entity != null) obj.Add("entity", type.Entity);
            if (type.Kind == "record") obj.Add("fields", type.Fields.OrderBy(f => f.Name, StringComparer.Ordinal).Select(f =>
            {
                var field = Object("name", f.Name, "type", f.Type);
                if (f.Optional) field.Add("optional", true);
                if (f.HasDefault) field.Add("default", f.Default);
                return field;
            }).ToList());
            else obj.Add("values", type.Values.OrderBy(v => v, StringComparer.Ordinal).ToList());
            return obj;
        }

        internal static Dictionary<string, object> Object(params object[] values)
        {
            var result = new Dictionary<string, object>(StringComparer.Ordinal);
            for (var i = 0; i < values.Length; i += 2) result.Add((string)values[i], values[i + 1]);
            return result;
        }
        private static bool ValidName(string value) => value != null && Name.IsMatch(value);
        private static void ValidateFactory(GolemContentType type, string factory, string path)
        {
            if (!string.IsNullOrEmpty(type.Entity) && string.IsNullOrEmpty(factory)) Fail(path, "archetype requires a factory key");
            if (factory != null && Encoding.UTF8.GetByteCount(factory) > 512) Fail(path, "factory key exceeds 512 bytes");
        }
        private static string ReadString(IDictionary<string, object> value, string key)
        {
            if (!value.TryGetValue(key, out var result) || !(result is string)) Fail("imports." + key, "string is required");
            return (string)result;
        }
        private static void AddTarget(Dictionary<string, string> targets, string id, string type, string path)
        {
            if (id == null || id.Length > 512 || !Identity.IsMatch(id) || id.Split('/')[1] != type) Fail(path, "ID must be namespace/Type/key and match the declared type");
            if (targets.ContainsKey(id)) Fail(path, "duplicate definition identity " + id);
            targets.Add(id, type);
            if (targets.Count > MaximumDefinitions) Fail(path, "combined selected/imported definitions exceed limit");
        }
        private static void AddSource(Dictionary<string, string> sources, string key, string id)
        {
            if (!Regex.IsMatch(key ?? "", "^[0-9a-f]{32}:-?[0-9]+$")) Fail(id, "invalid GUID/local file ID provenance");
            if (sources.ContainsKey(key)) Fail(id, "source is mapped more than once: " + key);
            sources.Add(key, id);
        }
        private static string Location(GolemContentDefinition row) => row.Source == null ? row.Id : row.Source.Path + " (" + row.Id + ")";
        private sealed class Budget
        {
            private int remaining;
            internal Budget(int maximum) { remaining = maximum; }
            internal void Spend(int amount, string path)
            { remaining -= amount; if (remaining < 0) Fail(path, "expanded content exceeds byte budget"); }
        }
        private static void Fail(string path, string message) => throw new InvalidOperationException(path + ": " + message);
    }

    /// <summary>Strict bounded JSON for content sources; dictionary keys are emitted in ordinal order.</summary>
    internal static class GolemContentJson
    {
        internal static string Write(object value)
        {
            var output = new StringBuilder(); WriteValue(output, value, 0); return output.ToString();
        }
        private static void WriteValue(StringBuilder output, object value, int depth)
        {
            if (depth > GolemContentDocument.MaximumDepth) throw new InvalidOperationException("JSON nesting exceeds limit");
            if (value == null) { output.Append("null"); return; }
            if (value is string text) { Quote(output, text); return; }
            if (value is bool boolean) { output.Append(boolean ? "true" : "false"); return; }
            if (value is IDictionary<string, object> obj)
            {
                output.Append('{'); var first = true;
                foreach (var pair in obj.OrderBy(p => p.Key, StringComparer.Ordinal))
                { if (!first) output.Append(','); first = false; Quote(output, pair.Key); output.Append(':'); WriteValue(output, pair.Value, depth + 1); }
                output.Append('}'); return;
            }
            if (value is IEnumerable array)
            {
                output.Append('['); var count = 0;
                foreach (var item in array)
                { if (count >= GolemContentDocument.MaximumCollectionLength) throw new InvalidOperationException("JSON collection exceeds limit"); if (count++ != 0) output.Append(','); WriteValue(output, item, depth + 1); }
                output.Append(']'); return;
            }
            if (value is float single)
            { if (float.IsNaN(single) || float.IsInfinity(single)) throw new InvalidOperationException("JSON requires finite numbers"); output.Append(single.ToString("R", CultureInfo.InvariantCulture)); return; }
            if (value is double number)
            { if (double.IsNaN(number) || double.IsInfinity(number)) throw new InvalidOperationException("JSON requires finite numbers"); output.Append(number.ToString("R", CultureInfo.InvariantCulture)); return; }
            if (value is byte || value is sbyte || value is short || value is ushort || value is int || value is uint || value is long || value is ulong || value is decimal)
            { output.Append(Convert.ToString(value, CultureInfo.InvariantCulture)); return; }
            throw new InvalidOperationException("Unsupported portable JSON value " + value.GetType());
        }
        private static void Quote(StringBuilder output, string value)
        {
            output.Append('"');
            foreach (var c in value)
            {
                switch (c)
                {
                    case '"': output.Append("\\\""); break;
                    case '\\': output.Append("\\\\"); break;
                    case '\n': output.Append("\\n"); break;
                    case '\r': output.Append("\\r"); break;
                    case '\t': output.Append("\\t"); break;
                    default: if (c < 32 || char.IsSurrogate(c)) output.Append("\\u" + ((int)c).ToString("x4")); else output.Append(c); break;
                }
            }
            output.Append('"');
        }
        internal static object Parse(string json)
        {
            if (json == null || Encoding.UTF8.GetByteCount(json) > GolemContentDocument.MaximumDocumentBytes) throw new InvalidOperationException("Content JSON exceeds byte limit");
            var parser = new Parser(json); var value = parser.Value(0); parser.White();
            if (parser.Position != json.Length) throw new InvalidOperationException("Trailing content JSON");
            return value;
        }
        private sealed class Parser
        {
            private readonly string text;
            internal int Position;
            internal Parser(string text) { this.text = text; }
            internal void White() { while (Position < text.Length && (text[Position] == ' ' || text[Position] == '\t' || text[Position] == '\r' || text[Position] == '\n')) Position++; }
            private bool Take(char c) { White(); if (Position < text.Length && text[Position] == c) { Position++; return true; } return false; }
            private void Need(char c) { if (!Take(c)) throw new InvalidOperationException("Expected '" + c + "' at JSON offset " + Position); }
            internal object Value(int depth)
            {
                if (depth > GolemContentDocument.MaximumDepth) throw new InvalidOperationException("JSON nesting exceeds limit");
                White(); if (Position >= text.Length) throw new InvalidOperationException("Truncated JSON");
                if (text[Position] == '"') return String();
                if (Take('{'))
                {
                    var obj = new Dictionary<string, object>(StringComparer.Ordinal); if (Take('}')) return obj;
                    do { White(); var key = String(); Need(':'); if (obj.ContainsKey(key)) throw new InvalidOperationException("Duplicate JSON key " + key); obj.Add(key, Value(depth + 1)); if (obj.Count > GolemContentDocument.MaximumCollectionLength) throw new InvalidOperationException("JSON object exceeds limit"); } while (Take(','));
                    Need('}'); return obj;
                }
                if (Take('['))
                {
                    var list = new List<object>(); if (Take(']')) return list;
                    do { if (list.Count >= GolemContentDocument.MaximumCollectionLength) throw new InvalidOperationException("JSON array exceeds limit"); list.Add(Value(depth + 1)); } while (Take(','));
                    Need(']'); return list;
                }
                foreach (var literal in new[] { "true", "false", "null" })
                    if (string.CompareOrdinal(text, Position, literal, 0, literal.Length) == 0) { Position += literal.Length; return literal == "null" ? null : (object)(literal == "true"); }
                var start = Position;
                while (Position < text.Length && "-+0123456789.eE".IndexOf(text[Position]) >= 0) Position++;
                var token = text.Substring(start, Position - start);
                if (!Regex.IsMatch(token, "^-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?$")) throw new InvalidOperationException("Invalid JSON number at " + start);
                // Decimal rounds tiny fractional/exponent inputs to zero; use it only for exact integers.
                if (token.IndexOfAny(new[] { '.', 'e', 'E' }) < 0 && decimal.TryParse(token, NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture, out var exact)) return exact;
                if (double.TryParse(token, NumberStyles.Float, CultureInfo.InvariantCulture, out var number) && !double.IsInfinity(number)) return number;
                throw new InvalidOperationException("JSON number exceeds range");
            }
            private string String()
            {
                Need('"'); var result = new StringBuilder();
                while (Position < text.Length)
                {
                    var c = text[Position++]; if (c == '"') return result.ToString();
                    if (c < 32) throw new InvalidOperationException("Control character in JSON string");
                    if (c != '\\') { result.Append(c); continue; }
                    if (Position == text.Length) break;
                    c = text[Position++];
                    switch (c)
                    {
                        case '"': case '\\': case '/': result.Append(c); break;
                        case 'b': result.Append('\b'); break;
                        case 'f': result.Append('\f'); break;
                        case 'n': result.Append('\n'); break;
                        case 'r': result.Append('\r'); break;
                        case 't': result.Append('\t'); break;
                        case 'u':
                            if (Position + 4 > text.Length || !ushort.TryParse(text.Substring(Position, 4), NumberStyles.HexNumber, CultureInfo.InvariantCulture, out var scalar)) throw new InvalidOperationException("Invalid JSON unicode escape");
                            result.Append((char)scalar); Position += 4; break;
                        default: throw new InvalidOperationException("Invalid JSON escape");
                    }
                }
                throw new InvalidOperationException("Unterminated JSON string");
            }
        }
    }
}
