"""Build cross-target helper without executing it on the build host."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

BUILD = Path(__file__).resolve().parents[1] / 'build-native.sh'

class NativeBuildTests(unittest.TestCase):
    def test_sdk_build_does_not_execute_target_binary(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            xcrun = root / 'xcrun'
            xcrun.write_text('''#!/bin/bash
set -eu
case "$*" in
  *--show-sdk-version*) echo "${FIXTURE_SDK:-27.0}";;
  *--show-sdk-path*) echo /synthetic-sdk;;
  *swiftc*)
    while [[ $# -gt 0 ]]; do
      if [[ "$1" == -o ]]; then shift; output=$1; break; fi
      shift
    done
    printf '#!/bin/sh\\nexit 91\\n' > "$output"
    chmod 700 "$output";;
  *vtool*) echo 'platform MACOS; minos 27.0; sdk 27.0';;
  *) exit 92;;
esac
''')
            xcrun.chmod(0o700)
            signer = root / 'codesign'
            signer.write_text('#!/bin/sh\nexit 0\n')
            signer.chmod(0o700)
            env = {**os.environ, 'PATH': str(root) + os.pathsep + os.environ['PATH']}
            result = subprocess.run(['bash', str(BUILD), str(root/'helper')], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue((root/'helper').is_file())
            self.assertIn('minos 27.0', result.stdout)
            env['FIXTURE_SDK'] = '26.0'
            result = subprocess.run(['bash', str(BUILD), str(root/'wrong-sdk-helper')], env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((root/'wrong-sdk-helper').exists())
