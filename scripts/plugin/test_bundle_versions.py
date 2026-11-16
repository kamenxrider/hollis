import tempfile
import unittest
from pathlib import Path

import package_runtime


class BundleVersionTests(unittest.TestCase):
    def test_cli_helper_mismatch_is_refused(self):
        with tempfile.TemporaryDirectory() as temporary:
            folder = Path(temporary)
            cli = folder / 'hollis-darwin-arm64'
            helper = folder / 'hollis-native-darwin-arm64'
            helper.write_text('#!/bin/sh\ncase "$1" in\n--version) echo "hollis-native 0.7.1";;\n--protocol-version) echo 4;;\n*) exit 99;;\nesac\n')
            helper.chmod(0o700)
            for version in ('0.4.0', '0.7.1'):
                cli.write_text(f'#!/bin/sh\necho "hollis {version}"\n')
                cli.chmod(0o700)
                if version == '0.4.0':
                    with self.assertRaisesRegex(ValueError, 'mismatch'):
                        package_runtime.verify_versions(folder, '0.7.1')
                else:
                    package_runtime.verify_versions(folder, '0.7.1')
