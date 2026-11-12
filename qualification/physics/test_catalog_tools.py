import copy
import unittest

from catalog_signatures import signature
from stage_catalog import dependencies, harvest_states


class CatalogToolsTests(unittest.TestCase):
    def test_dependency_capture_retains_physics_and_override_without_render_requirement(self):
        render, physical, overridden = "a" * 32, "b" * 32, "c" * 32
        text = ("--- !u!33 &1\nMeshFilter:\n  m_Mesh: {fileID: 1, guid: " + render + ", type: 3}\n"
                "--- !u!64 &2\nMeshCollider:\n  m_Mesh: {fileID: 1, guid: " + physical + ", type: 3}\n"
                "--- !u!1001 &3\nPrefabInstance:\n      propertyPath: m_Mesh\n      value: \n      objectReference: {fileID: 1, guid: " + overridden + ", type: 3}\n")
        self.assertEqual(dependencies(text), {physical, overridden})

    def test_empty_harvest_stage_disables_every_full_stage_object(self):
        text = "--- !u!114 &1\nMonoBehaviour:\n  fullStages:\n  - isEmptyStage: 0\n    stageMeshes:\n    - {fileID: 12}\n  emptyStage:\n    isEmptyStage: 1\n    stageMeshes: []\n"
        states = harvest_states(text, "a" * 32)
        self.assertEqual(len(states), 2)
        self.assertTrue(states[0]["Toggles"][0]["Active"])
        self.assertFalse(states[1]["Toggles"][0]["Active"])

    def test_full_signature_canonicalizes_identity_but_preserves_geometry_activation_roles(self):
        source = {"WorldID": "one", "Revision": 1, "Bodies": [{"ID": "body", "Position": {"X": 1}, "Active": True}],
                  "Colliders": [{"ID": "leaf", "BodyID": "body", "SourceID": "leaf", "ParentID": "parent", "AssetID": "asset", "MaterialID": "material", "OwnerID": "", "Solid": True, "Active": True}],
                  "Assets": [{"ID": "asset", "SourceID": "mesh", "Vertices": [{"X": 1}], "Indices": [0, 1, 2]}], "Materials": [{"ID": "material", "StaticFriction": .6}], "Joints": [], "LayerCollisionMasks": [3, 3]}
        resolved = {"Leaves": [{"ID": "leaf"}]}
        expected = signature(source, resolved)
        renamed = copy.deepcopy(source); renamed["WorldID"] = "two"; renamed["Bodies"][0]["ID"] = "otherbody"; renamed["Colliders"][0]["BodyID"] = "otherbody"
        self.assertEqual(signature(renamed, resolved), expected)
        for category, field, value in (("Bodies", "Position", {"X": 1.0000001}), ("Colliders", "Active", False), ("Colliders", "Solid", False), ("Materials", "StaticFriction", .7)):
            changed = copy.deepcopy(source); changed[category][0][field] = value
            self.assertNotEqual(signature(changed, resolved), expected)
        with self.assertRaisesRegex(ValueError, "identity"):
            signature(source, {"Leaves": [{"ID": "otherleaf"}]})


if __name__ == "__main__":
    unittest.main()
