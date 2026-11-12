import hashlib
import unittest

from filter_prefab import exclude_instance


GUID = "d" * 32
ASSET = "a" * 32
SOURCE = ("%YAML 1.1\n--- !u!4 &3\nTransform:\n  m_Children:\n  - {fileID: 2}\n  - {fileID: 4}\n"
          "--- !u!4 &4\nTransform:\n  m_Father: {fileID: 3}\n"
          "--- !u!1001 &1\nPrefabInstance:\n  m_TransformParent: {fileID: 3}\n"
          "  m_SourcePrefab: {fileID: 100100000, guid: " + GUID + ", type: 3}\n"
          "--- !u!4 &2 stripped\nTransform:\n  m_PrefabInstance: {fileID: 1}\n"
          "  m_CorrespondingSourceObject: {fileID: 42, guid: " + GUID + ", type: 3}\n").encode()


def spec(data=SOURCE):
    return dict(sourceSHA256=hashlib.sha256(data).hexdigest(), sourceAssetGUID=ASSET,
                excludedSourceGUID=GUID, prefabInstanceFileID="1", strippedTransformFileID="2", parentTransformFileID="3")


class FilterPrefabTests(unittest.TestCase):
    def test_retains_unrelated_geometry_and_variant_removals(self):
        variant = ("m_RemovedComponents:\n- {fileID: 99, guid: " + ASSET + ", type: 3}\n").encode()
        output, evidence = exclude_instance(SOURCE, spec(), [("variant", variant, hashlib.sha256(variant).hexdigest())])
        self.assertIn(b"--- !u!4 &4\nTransform:\n  m_Father: {fileID: 3}\n", output)
        self.assertIn(b"m_Children:\n  - {fileID: 4}\n", output)
        self.assertNotIn(GUID.encode(), output)
        self.assertTrue(evidence["variants"][0]["unchanged"])

    def test_rejects_unexpected_remaining_local_reference(self):
        data = SOURCE.replace(b"  m_Father: {fileID: 3}", b"  m_Father: {fileID: 2}")
        with self.assertRaisesRegex(ValueError, "references"):
            exclude_instance(data, spec(data), [])

    def test_rejects_dangling_variant_removal(self):
        variant = ("m_RemovedComponents:\n- {fileID: 2, guid: " + ASSET + ", type: 3}\n").encode()
        with self.assertRaisesRegex(ValueError, "variant retains"):
            exclude_instance(SOURCE, spec(), [("variant", variant, hashlib.sha256(variant).hexdigest())])

    def test_rejects_changed_source_hash(self):
        with self.assertRaisesRegex(ValueError, "source hash"):
            exclude_instance(SOURCE + b"\n", spec(), [])

    def test_empty_parent_and_crlf_preserved(self):
        data = SOURCE.replace(b"  - {fileID: 4}\n", b"").replace(b"\n", b"\r\n")
        output, _ = exclude_instance(data, spec(data), [])
        self.assertIn(b"  m_Children: []\r\n", output)
        self.assertNotIn(b"\n", output.replace(b"\r\n", b""))


if __name__ == "__main__":
    unittest.main()
