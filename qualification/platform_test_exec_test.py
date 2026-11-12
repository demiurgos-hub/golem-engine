"""Exercise containment without confusing Windows path aliases with escapes."""
import os
from pathlib import Path
import tempfile
import unittest

from platform_test_exec import confined


class RetainedBinaryPaths(unittest.TestCase):
    def test_child_and_sibling(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "bin"
            root.mkdir()
            child = root / "package" / "test.exe"
            self.assertEqual(confined(child, root), child.resolve())
            with self.assertRaises(ValueError):
                confined(root.parent / "bin-other" / "test.exe", root)
            with self.assertRaises(ValueError):
                confined(root / ".." / "escaped.exe", root)

    @unittest.skipUnless(os.name == "nt", "Windows extended-length path spelling")
    def test_extended_length_aliases_preserve_containment(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "bin"
            root.mkdir()
            child = root / "package" / "test.exe"
            alias_root = Path("\\\\?\\" + str(root))
            alias_child = Path("\\\\?\\" + str(child))
            self.assertEqual(confined(alias_child, root), confined(child, alias_root))
            with self.assertRaises(ValueError):
                confined(Path("\\\\?\\" + str(root.parent / "escaped.exe")), alias_root)


if __name__ == "__main__":
    unittest.main()
