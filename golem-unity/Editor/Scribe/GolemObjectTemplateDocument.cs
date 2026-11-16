using System;
using System.Globalization;
using System.Linq;
using System.Text;

namespace GolemEngine.Unity.Editor
{
    // Wire types mirror golem/objecttemplate Catalog, Template and Shape2D.
    internal sealed class ObjectTemplateShape
    {
        public string ID, Kind, Layer;
        public float OffsetX, OffsetY, Radius, Width, Height;
        public bool Trigger;
    }

    internal sealed class ObjectTemplate
    {
        public string ID, SourceID = "";
        public PhysicsBody[] Bodies = Array.Empty<PhysicsBody>();
        public PhysicsCollider[] Colliders = Array.Empty<PhysicsCollider>();
        public ObjectTemplateShape[] Shapes = Array.Empty<ObjectTemplateShape>();
        public NavigationInstance[] Instances = Array.Empty<NavigationInstance>();
        public NavigationVolume[] Volumes = Array.Empty<NavigationVolume>();
        public NavigationLink[] Links = Array.Empty<NavigationLink>();
    }

    internal sealed class ObjectTemplateCatalog
    {
        public string CatalogID;
        public ulong Revision = 1;
        public int Dimensions;
        public uint[] LayerCollisionMasks = Array.Empty<uint>();
        public PhysicsMaterial[] Materials = Array.Empty<PhysicsMaterial>();
        public ObjectTemplate[] Templates = Array.Empty<ObjectTemplate>();
        public PhysicsGeometryReference Geometry;
    }

    /// <summary>Deterministic Object Template catalog JSON, read strictly by golem/objecttemplate.</summary>
    internal static class GolemObjectTemplateDocument
    {
        internal const string Generator = "Golem Scribe object templates v1";
        internal const int Version = 1;
        internal const string OwnershipPrefix = "{\n  \"Generator\": \"" + Generator + "\",\n";

        internal static bool IsOwned(string text) => text != null && text.StartsWith(OwnershipPrefix, StringComparison.Ordinal);

        internal static string Build(ObjectTemplateCatalog catalog)
        {
            Require(catalog != null && !string.IsNullOrWhiteSpace(catalog.CatalogID) && catalog.Revision > 0, "Object template catalog identity is required.");
            Require(catalog.Dimensions == 2 || catalog.Dimensions == 3, "Object template catalogs must be 2D or 3D.");
            var templates = catalog.Templates.OrderBy(t => t.ID, StringComparer.Ordinal).ToArray();
            for (var i = 0; i < templates.Length; i++)
            {
                Require(!string.IsNullOrWhiteSpace(templates[i].ID) && templates[i].ID.Trim() == templates[i].ID, "Invalid template ID: '" + templates[i].ID + "'.");
                Require(i == 0 || templates[i - 1].ID != templates[i].ID, "Duplicate template ID: " + templates[i].ID);
            }
            var json = new StringBuilder(OwnershipPrefix);
            json.Append("  \"Version\": ").Append(Version.ToString(CultureInfo.InvariantCulture)).Append(",\n");
            json.Append("  \"CatalogID\": "); GolemPhysicsDocument.Quote(json, catalog.CatalogID); json.Append(",\n");
            json.Append("  \"Revision\": ").Append(catalog.Revision.ToString(CultureInfo.InvariantCulture)).Append(",\n");
            json.Append("  \"Dimensions\": ").Append(catalog.Dimensions.ToString(CultureInfo.InvariantCulture)).Append(",\n");
            if (catalog.Dimensions == 3)
            {
                Require(catalog.LayerCollisionMasks.Length == 32, "3D catalogs require 32 layer collision masks.");
                json.Append("  \"LayerCollisionMasks\": "); GolemPhysicsDocument.Write(json, catalog.LayerCollisionMasks); json.Append(",\n");
                json.Append("  \"Materials\": "); GolemPhysicsDocument.Write(json, catalog.Materials.OrderBy(m => m.ID, StringComparer.Ordinal).ToArray()); json.Append(",\n");
                if (catalog.Geometry != null) { json.Append("  \"Geometry\": "); GolemPhysicsDocument.Write(json, catalog.Geometry); json.Append(",\n"); }
            }
            json.Append("  \"Templates\": [");
            for (var i = 0; i < templates.Length; i++)
            {
                json.Append(i == 0 ? "\n    " : ",\n    ");
                WriteTemplate(json, templates[i]);
            }
            json.Append(templates.Length == 0 ? "]\n}\n" : "\n  ]\n}\n");
            Require(Encoding.UTF8.GetByteCount(json.ToString()) <= 512 * 1024 * 1024, "Object template catalog exceeds 512 MiB.");
            return json.ToString();
        }

        private static void WriteTemplate(StringBuilder json, ObjectTemplate template)
        {
            json.Append("{\"ID\":"); GolemPhysicsDocument.Quote(json, template.ID);
            if (!string.IsNullOrEmpty(template.SourceID)) { json.Append(",\"SourceID\":"); GolemPhysicsDocument.Quote(json, template.SourceID); }
            if (template.Bodies.Length > 0)
            { json.Append(",\"Bodies\":"); GolemPhysicsDocument.Write(json, template.Bodies.OrderBy(b => b.ID, StringComparer.Ordinal).ToArray()); }
            if (template.Colliders.Length > 0)
            { json.Append(",\"Colliders\":"); GolemPhysicsDocument.Write(json, template.Colliders.OrderBy(c => c.ID, StringComparer.Ordinal).ToArray()); }
            if (template.Shapes.Length > 0)
            {
                json.Append(",\"Shapes\":[");
                var shapes = template.Shapes.OrderBy(s => s.ID, StringComparer.Ordinal).ToArray();
                for (var i = 0; i < shapes.Length; i++) { if (i > 0) json.Append(','); WriteShape(json, shapes[i]); }
                json.Append(']');
            }
            if (template.Instances.Length > 0 || template.Volumes.Length > 0 || template.Links.Length > 0)
            {
                json.Append(",\"Navigation\":{"); var comma = "";
                if (template.Instances.Length > 0)
                { json.Append("\"Instances\":"); GolemNavigationDocument.Write(json, template.Instances.OrderBy(x => x.ID, StringComparer.Ordinal).ToArray()); comma = ","; }
                if (template.Volumes.Length > 0)
                { json.Append(comma).Append("\"Volumes\":"); GolemNavigationDocument.Write(json, template.Volumes.OrderBy(x => x.ID, StringComparer.Ordinal).ToArray()); comma = ","; }
                if (template.Links.Length > 0)
                {
                    json.Append(comma).Append("\"Links\":");
                    GolemNavigationDocument.Write(json, template.Links.OrderBy(x => x.ID, StringComparer.Ordinal).Select(x => new NavigationLink
                    {
                        ID = x.ID, Action = x.Action ?? "", Revision = x.Revision, Start = x.Start, End = x.End, Radius = x.Radius,
                        Bidirectional = x.Bidirectional, Disabled = x.Disabled, Area = x.Area, Flags = x.Flags,
                        Profiles = x.Profiles.OrderBy(p => p, StringComparer.Ordinal).ToArray()
                    }).ToArray());
                }
                json.Append('}');
            }
            json.Append('}');
        }

        private static void WriteShape(StringBuilder json, ObjectTemplateShape shape)
        {
            json.Append("{\"ID\":"); GolemPhysicsDocument.Quote(json, shape.ID);
            json.Append(",\"Kind\":"); GolemPhysicsDocument.Quote(json, shape.Kind);
            json.Append(",\"OffsetX\":"); Number(json, shape.OffsetX);
            json.Append(",\"OffsetY\":"); Number(json, shape.OffsetY);
            if (shape.Kind == "circle") { json.Append(",\"Radius\":"); Number(json, shape.Radius); }
            else { json.Append(",\"Width\":"); Number(json, shape.Width); json.Append(",\"Height\":"); Number(json, shape.Height); }
            json.Append(",\"Layer\":"); GolemPhysicsDocument.Quote(json, shape.Layer);
            if (shape.Trigger) json.Append(",\"Trigger\":true");
            json.Append('}');
        }

        private static void Number(StringBuilder json, float value) => GolemPhysicsDocument.Write(json, value);

        private static void Require(bool ok, string message) { if (!ok) throw new InvalidOperationException(message); }
    }
}
