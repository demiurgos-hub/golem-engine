using System;
using System.Collections.Generic;
using System.Linq;

namespace GolemEngine.Unity.Editor
{
    internal static class GolemPhysicsBindingValidation
    {
        internal static void Validate(GolemEntitySchemaModel model, int dimensions)
        {
            var roles = new Dictionary<string, GolemEntityVarModel>(StringComparer.Ordinal);
            var rotation = new[] { "orientation_x", "orientation_y", "orientation_z", "orientation_w" };
            var velocity = new[] { "velocity_x", "velocity_y", "velocity_z", "angular_velocity_x", "angular_velocity_y", "angular_velocity_z" };
            foreach (var field in model.Vars.Where(v => !string.IsNullOrEmpty(v.Physics)))
            {
                if (dimensions != 3) model.Errors.Add("Physics bindings require simulation.dimensions: 3.");
                if (field.Physics != "active" && !rotation.Contains(field.Physics) && !velocity.Contains(field.Physics))
                    model.Errors.Add("Unknown physics role '" + field.Physics + "'.");
                if (field.SchemaType != (field.Physics == "active" ? "bool" : "float") || field.SyncYaml != "tick")
                    model.Errors.Add("Physics role '" + field.Physics + "' requires a float (active: bool) field with sync: tick.");
                if (roles.ContainsKey(field.Physics)) model.Errors.Add("Duplicate physics role '" + field.Physics + "'.");
                else roles.Add(field.Physics, field);
            }
            foreach (var group in new[] { rotation, velocity })
                if (group.Any(roles.ContainsKey) && !group.All(roles.ContainsKey))
                    model.Errors.Add("Physics group must bind every component: " + string.Join(", ", group) + ".");
            var methods = new List<string>();
            if (rotation.Any(roles.ContainsKey)) methods.Add("physics_rotation");
            if (velocity.Any(roles.ContainsKey)) methods.Add("physics_velocity");
            if (roles.ContainsKey("active")) methods.Add("physics_active");
            foreach (var field in model.Vars)
                if (methods.Contains(field.SnakeName) || methods.Any(m => field.SnakeName == "set_" + m) ||
                    (!string.IsNullOrEmpty(field.Physics) && new[] { "entity_id", "server", "bind_server" }.Contains(field.SnakeName)))
                    model.Errors.Add("Field '" + field.SnakeName + "' conflicts with generated physics or entity metadata.");
        }
    }
}
