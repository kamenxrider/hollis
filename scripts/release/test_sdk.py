import importlib.util
from pathlib import Path
import shutil
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('verify_sdk.py')
spec = importlib.util.spec_from_file_location('verify_sdk', SCRIPT)
sdk = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sdk)


class SDKProvenanceTests(unittest.TestCase):
    def test_exact_upstream_plus_patch(self):
        self.assertGreater(sdk.verify(), 100)

    def test_changed_added_and_missing_sources_are_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            shutil.copytree(sdk.ROOT / 'third_party', root / 'third_party')
            for name in ('go.mod', 'go.sum'):
                shutil.copyfile(sdk.ROOT / name, root / name)
            source = root / sdk.SOURCE
            target = source / 'mcp/protocol.go'
            original = target.read_bytes()
            target.write_bytes(original + b'\n')
            with self.assertRaisesRegex(ValueError, 'replacement contents'):
                sdk.verify(root)
            target.write_bytes(original)
            extra = source / 'unexpected.txt'
            extra.write_text('unexpected')
            with self.assertRaisesRegex(ValueError, 'replacement contents'):
                sdk.verify(root)
            extra.unlink()
            target.unlink()
            with self.assertRaisesRegex(ValueError, 'replacement contents'):
                sdk.verify(root)


if __name__ == '__main__':
    unittest.main()
