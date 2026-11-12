import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest
import sys

sys.dont_write_bytecode = True
from candidate_inputs import prepare, record, verify, write_new


class CandidateInputsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=os.environ.get("TMPDIR") or os.environ.get("TEMP"))
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.lane = self.root / "workspaces/candidate"
        source = self.lane / "library"
        source.mkdir(parents=True)
        (source / "go.mod").write_text("module example.invalid/fixture\n\ngo 1.20\n")
        (source / "go.sum").write_text("original-checksum\n")
        files = [{"path": path.name, "bytes": path.stat().st_size, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()} for path in source.iterdir()]
        (self.lane / "manifest.json").write_text(json.dumps({"sources": {"library": {"files": files}}, "modules": []}))

    def test_external_dependency_updates_leave_original_candidate_intact(self):
        before = prepare(self.lane, self.root / "scratch/modfiles", [("fixture", "library")])
        item = before["modules"]["fixture"]
        Path(item["modfile"]["path"]).write_text("module example.invalid/fixture\n\ngo 1.20\nrequire example.invalid/dependency v1.0.0\n")
        Path(item["sumfile"]["path"]).write_text("candidate-checksum\n")
        after = record(before)
        self.assertTrue(verify(self.lane)["pass"])
        self.assertNotEqual(item["modfile"]["sha256"], after["modules"]["fixture"]["modfile"]["sha256"])
        self.assertNotEqual(item["sumfile"]["sha256"], after["modules"]["fixture"]["sumfile"]["sha256"])

    def test_changed_and_unmanifested_sources_fail(self):
        source = self.lane / "library"
        (source / "go.sum").write_text("modified-checksum\n")
        (source / "injected.go").write_text("package injected\n")
        result = verify(self.lane)
        self.assertFalse(result["pass"])
        self.assertEqual({error["reason"] for error in result["errors"]}, {"manifested source bytes changed", "unmanifested source-tree file"})

    def test_receipts_cannot_overwrite_prior_evidence(self):
        path = self.root / "receipt.json"
        write_new(path, {"pass": True})
        with self.assertRaises(FileExistsError):
            write_new(path, {"pass": False})
        self.assertTrue(json.loads(path.read_text())["pass"])


if __name__ == "__main__":
    unittest.main()
