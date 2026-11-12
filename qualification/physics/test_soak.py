import importlib.util
import json
from pathlib import Path
import platform
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("physics_soak", Path(__file__).resolve().parents[1] / "physics_soak.py")
soak = importlib.util.module_from_spec(spec)
spec.loader.exec_module(soak)


class SoakEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.source_hash = "a" * 64
        self.results = {"MeasuredSamples": 1, "CheckpointID": "next"}
        self.manifest = {"measurement": "retain complete overrun", "sourceManifestSHA256": self.source_hash,
                         "goarch": "amd64", "goos": platform.system().lower(), "buildInfo": {"Settings": [{"Key": "CGO_ENABLED", "Value": "0"}]}}
        self.save("results.json", self.results)
        self.save("manifest.json", self.manifest)
        (self.directory / "samples.jsonl").write_text('{"Measured":true,"FullTickNS":63000000000}\n', encoding="utf-8")
        checkpoint = self.directory / "checkpoint"
        checkpoint.mkdir()
        data = checkpoint / "data"
        data.write_bytes(b"immutable fixture")
        digest = soak.digest(data)
        data.rename(checkpoint / (digest + ".physics.json"))
        self.save("checkpoint/physics-checkpoint.json", {"CheckpointID": "next", "File": digest + ".physics.json", "SHA256": digest})
        self.save("restart.json", {"checkpointID": "prior", "ready": True})

    def save(self, name, value):
        (self.directory / name).write_text(json.dumps(value), encoding="utf-8")

    def inspect(self):
        return soak.inspect_epoch(self.directory, "prior", self.source_hash)

    def test_retains_slow_terminal_frame(self):
        _, samples = self.inspect()
        self.assertEqual(63_000_000_000, samples[0]["FullTickNS"])

    def test_rejects_wrong_checkpoint_chain_and_corrupted_data(self):
        self.save("restart.json", {"checkpointID": "unrelated", "ready": True})
        with self.assertRaisesRegex(RuntimeError, "preceding checkpoint"):
            self.inspect()
        self.save("restart.json", {"checkpointID": "prior", "ready": True})
        next((self.directory / "checkpoint").glob("*.physics.json")).write_bytes(b"changed")
        with self.assertRaisesRegex(RuntimeError, "data identity mismatch"):
            self.inspect()

    def test_rejects_missing_samples_and_censored_frame_protocol(self):
        self.results["MeasuredSamples"] = 2
        self.save("results.json", self.results)
        with self.assertRaisesRegex(RuntimeError, "incomplete"):
            self.inspect()
        self.manifest["measurement"] = "exclude crossing ticks"
        self.save("manifest.json", self.manifest)
        with self.assertRaisesRegex(RuntimeError, "terminal measurement frames"):
            self.inspect()

    def test_rejects_native_dependency_and_source_mismatch(self):
        self.manifest["buildInfo"]["Settings"][0]["Value"] = "1"
        self.save("manifest.json", self.manifest)
        with self.assertRaisesRegex(RuntimeError, "CGO_ENABLED=0"):
            self.inspect()
        self.manifest["sourceManifestSHA256"] = "b" * 64
        self.save("manifest.json", self.manifest)
        with self.assertRaisesRegex(RuntimeError, "source identity"):
            self.inspect()


if __name__ == "__main__":
    unittest.main()
