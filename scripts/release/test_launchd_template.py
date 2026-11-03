"""Check the serve LaunchAgent template offline; nothing is loaded into launchd."""
import ipaddress
from pathlib import Path
import plistlib
import shutil
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
LABEL = 'io.github.kamenxrider.hollis.serve'
TEMPLATE = ROOT / 'packaging/launchd' / (LABEL + '.plist')


class ServeLaunchAgentTemplateTests(unittest.TestCase):
    def setUp(self):
        self.plist = plistlib.loads(TEMPLATE.read_bytes())
        self.args = self.plist['ProgramArguments']

    def flag(self, name):
        self.assertEqual(self.args.count(name), 1, name)
        return self.args[self.args.index(name) + 1]

    @unittest.skipUnless(shutil.which('plutil'), 'plutil is macOS-only')
    def test_plutil_accepts_template(self):
        subprocess.run(['plutil', '-lint', str(TEMPLATE)], check=True, capture_output=True)

    def test_runs_serve_as_absolute_program(self):
        self.assertEqual(self.plist['Label'], LABEL)
        self.assertTrue(self.args[0].startswith('/') and self.args[0].endswith('/hollis'))
        self.assertEqual(self.args[1], 'serve')
        self.assertNotIn('Program', self.plist)

    def test_binds_loopback_only(self):
        host, _, port = self.flag('--addr').rpartition(':')
        self.assertTrue(ipaddress.ip_address(host.strip('[]')).is_loopback)
        self.assertEqual(port, '1978')
        self.assertNotIn('--allow-remote', self.args)
        self.assertNotIn('--no-auth', self.args)

    def test_token_comes_from_a_file_and_never_from_the_plist(self):
        self.assertTrue(self.flag('--token-file').startswith('/'))
        self.assertNotIn('EnvironmentVariables', self.plist)
        self.assertNotIn(b'HOLLIS_API_TOKEN', TEMPLATE.read_bytes())
        self.assertFalse(any(a.startswith('--token=') or a == '--token' for a in self.args))

    def test_restarts_only_after_failure_and_logs_to_user_library(self):
        self.assertIs(self.plist['RunAtLoad'], True)
        self.assertEqual(self.plist['KeepAlive'], {'SuccessfulExit': False})
        self.assertGreaterEqual(self.plist['ThrottleInterval'], 10)
        for key in ('StandardOutPath', 'StandardErrorPath'):
            self.assertEqual(self.plist[key], '/Users/you/Library/Logs/hollis-serve.log')

    def test_docs_match_template(self):
        docs = (ROOT / 'docs/serve.md').read_text()
        self.assertIn('packaging/launchd/' + LABEL + '.plist', docs)
        for command in ('launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/' + LABEL + '.plist',
                        'launchctl bootout gui/$(id -u)/' + LABEL,
                        '~/Library/Logs/hollis-serve.log'):
            self.assertIn(command, docs)
        self.assertIn(self.flag('--token-file').replace('/Users/you', '~').replace(' ', '\\ '), docs)


if __name__ == '__main__':
    unittest.main()
