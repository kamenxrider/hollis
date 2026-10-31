"""Real isolated-prefix Homebrew formula lifecycle tests.

These tests execute the actual rendered formula through a disposable Homebrew
prefix cloned from this machine's own Homebrew installation. The bundle is
served by a loopback HTTP server from local fixture assets; no other network is
used, nothing is published, global Homebrew state is never touched, and no
inference or Apple/Shortcuts calls are made.

If Homebrew itself is not installed, the suite reports a skip with that reason
instead of claiming coverage.
"""
import functools
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[2]
BRIDGE_NAMES = [
    'AFM Bridge - Cloud.shortcut',
    'AFM Bridge - Cloud Pro.shortcut',
    'AFM Bridge - On-Device.shortcut',
    'AFM Bridge - ChatGPT.shortcut',
    'Hollis Image - Reference Input v2.shortcut',
]
BUNDLE_MEMBERS = ['LICENSE', 'hollis', 'hollis-bridges.zip', 'hollis-native', 'manifest.json']


def run(command, env=None, timeout=900):
    result = subprocess.run(command, env=env, capture_output=True, text=True, timeout=timeout)
    if result.returncode != 0:
        raise AssertionError(f'command failed: {command}\n{result.stdout}\n{result.stderr}')
    return result.stdout


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def snapshot(root):
    if not root.exists():
        return {}
    return {str(path.relative_to(root)): (
        path.stat().st_mode, path.stat().st_mtime_ns,
        path.read_bytes() if path.is_file() and not path.is_symlink() else None)
        for path in root.rglob('*')}


class LoopbackServer:
    """Serves fixture bundles from localhost only and records every request."""

    def __init__(self, directory):
        self.requests = []
        handler = functools.partial(
            LoggingHandler, directory=str(directory), requests=self.requests)
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    @property
    def port(self):
        return self.server.server_address[1]

    def stop(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=10)


class LoggingHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, requests=None, **kwargs):
        self.requests = requests
        super().__init__(*args, **kwargs)

    def log_message(self, fmt, *args):
        self.requests.append(self.path)


class FormulaLifecycle:
    """Builds real fixture assets and a disposable Homebrew prefix."""

    def __init__(self, base, brew_repository):
        self.base = base
        self.assets = base / 'assets'
        self.served = base / 'served'
        self.home = base / 'home'
        self.state = base / 'hollis-state'
        self.cache = base / 'cache'
        self.logs = base / 'logs'
        self.shim = base / 'shim'
        self.shim_log = base / 'shim-calls.log'
        self.tap = base / 'tap'
        self.helper_version = None
        self.server = None
        self.brew_repository = brew_repository
        for folder in (self.assets, self.served, self.home, self.state, self.cache,
                       self.logs, self.shim, self.tap):
            folder.mkdir(parents=True)
        # A valid, strictly parsed Hollis config so any CLI touch is observable
        # as a content change rather than a parse error.
        self.state.joinpath('config.json').write_text('{"default_model":"local"}\n')
        self.state.joinpath('config.json.lock').write_bytes(b'')
        shim = self.shim / 'shortcuts'
        shim.write_text('#!/bin/bash\nprintf \'%s\\n\' "$*" >> "$SHIM_LOG"\nexit 1\n')
        shim.chmod(0o755)

    # ---- fixture building -------------------------------------------------
    def build_fixtures(self, versions):
        self.helper = self.assets / 'hollis-native'
        run(['bash', str(ROOT / 'scripts/build-native.sh'), str(self.helper)])
        self.helper_version = run([str(self.helper), '--version']).split()[-1]
        for version in versions:
            run(['go', 'build', '-C', str(ROOT), '-trimpath', '-ldflags',
                 f'-s -w -X github.com/kamenxrider/hollis/internal/cli.version={version}',
                 '-o', str(self.assets / f'hollis-{version}'), './cmd/hollis'])
        run(['python3', str(ROOT / 'scripts/package-bridges.py'), str(self.assets)])
        self.bridges_zip = self.assets / 'hollis-bridges.zip'
        self.server = LoopbackServer(self.served)
        for version in versions:
            self.build_bundle(version)

    def build_bundle(self, version):
        hashes = {
            'hollis': sha256(self.assets / f'hollis-{version}'),
            'hollis-native': sha256(self.helper),
            'hollis-bridges.zip': sha256(self.bridges_zip),
            'LICENSE': sha256(ROOT / 'LICENSE'),
        }
        manifest = {'version': version, 'native_protocol_version': 1,
                    'native_minimum_macos': '27.0', 'architecture': 'arm64',
                    'sha256': hashes}
        inner = self.assets / f'manifest-{version}.json'
        inner.write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
        bundle = self.served / f'hollis-{version}-darwin-arm64.zip'
        with zipfile.ZipFile(bundle, 'w', zipfile.ZIP_DEFLATED) as archive:
            sources = {'hollis': self.assets / f'hollis-{version}', 'hollis-native': self.helper,
                       'hollis-bridges.zip': self.bridges_zip, 'LICENSE': ROOT / 'LICENSE',
                       'manifest.json': inner}
            for name in BUNDLE_MEMBERS:
                archive.write(sources[name], name)

    def render_formula(self, version):
        template = (ROOT / 'packaging/homebrew/hollis.rb.in').read_text()
        bundle_name = f'hollis-{version}-darwin-arm64.zip'
        published_url = (f'https://github.com/kamenxrider/hollis/releases/download/'
                         f'v{version}/{bundle_name}')
        rendered = (template
                    .replace('{{BUNDLE_NAME}}', bundle_name)
                    .replace('{{VERSION}}', version)
                    .replace('{{BUNDLE_SHA256}}', sha256(self.served / bundle_name))
                    .replace('{{HELPER_VERSION}}', self.helper_version)
                    .replace(published_url,
                             f'http://127.0.0.1:{self.server.port}/{bundle_name}'))
        (self.served / 'hollis.rb').write_text(rendered)

    # ---- disposable Homebrew prefix ---------------------------------------
    def brew_env(self):
        environment = dict(os.environ)
        environment.update(
            HOME=str(self.home),
            HOMEBREW_NO_AUTO_UPDATE='1',
            HOMEBREW_NO_INSTALL_FROM_API='1',
            HOMEBREW_NO_ENV_HINTS='1',
            HOMEBREW_NO_INSTALL_CLEANUP='1',
            HOMEBREW_CACHE=str(self.cache),
            HOMEBREW_LOGS=str(self.logs),
            HOLLIS_STATE_DIR=str(self.state),
            SHIM_LOG=str(self.shim_log),
            PATH=f'{self.shim}{os.pathsep}{environment["PATH"]}',
        )
        return environment

    def brew(self, prefix, *arguments):
        return run([str(prefix / 'bin/brew'), *arguments], env=self.brew_env())

    def build_prefix(self, name):
        prefix = self.base / name
        run(['git', 'clone', '--quiet', '--local', self.brew_repository, str(prefix)])
        vendor = prefix / 'Library/Homebrew/vendor/portable-ruby'
        vendor.mkdir(parents=True)
        vendored = Path(self.brew_repository, 'Library/Homebrew/vendor/portable-ruby')
        for entry in vendored.iterdir():
            if entry.is_dir() and not entry.is_symlink():
                shutil.copytree(entry, vendor / entry.name)
        versions = sorted(entry.name for entry in vendor.iterdir() if entry.is_dir())
        (vendor / 'current').symlink_to(versions[-1])
        (prefix / 'bin/brew').chmod(0o555)
        return prefix

    def build_tap(self, prefix, formula_version='0.4.0'):
        self.render_formula(formula_version)
        (self.tap / 'hollis.rb').write_text((self.served / 'hollis.rb').read_text())
        run(['git', '-C', str(self.tap), 'init', '--quiet'])
        run(['git', '-C', str(self.tap), 'add', '-A'])
        commit = subprocess.run(
            ['git', '-C', str(self.tap), '-c', 'user.email=fixture@example.com',
             '-c', 'user.name=fixture', 'commit', '--quiet', '-m', 'fixture formula'],
            capture_output=True, text=True)
        if commit.returncode != 0 and 'nothing to commit' not in commit.stdout + commit.stderr:
            raise AssertionError(f'tap commit failed\n{commit.stdout}\n{commit.stderr}')
        self.brew(prefix, 'tap', 'fixture/hollis', str(self.tap))

    def commit_formula_to_prefix_tap(self, prefix, version):
        tap_clone = prefix / 'Library/Taps/fixture/homebrew-hollis'
        self.render_formula(version)
        (tap_clone / 'hollis.rb').write_text((self.served / 'hollis.rb').read_text())
        run(['git', '-C', str(tap_clone), 'add', '-A'])
        run(['git', '-C', str(tap_clone), '-c', 'user.email=fixture@example.com',
             '-c', 'user.name=fixture', 'commit', '--quiet', '-m', f'bump {version}'])


class FormulaLifecycleTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        brew = shutil.which('brew') or '/opt/homebrew/bin/brew'
        if not Path(brew).exists():
            raise unittest.SkipTest(
                'BLOCKER: Homebrew is not installed; real formula lifecycle '
                'coverage cannot run in this environment.')
        repository = run([brew, '--repository']).strip()
        cls.base_handle = tempfile.TemporaryDirectory(prefix='hollis-brew-', dir='/var/tmp')
        cls.base = Path(cls.base_handle.name)
        cls.lifecycle = FormulaLifecycle(cls.base, repository)
        cls.lifecycle.build_fixtures(['0.4.0', '0.5.0'])
        cls.prefix = cls.lifecycle.build_prefix('prefix')
        cls.lifecycle.build_tap(cls.prefix, '0.4.0')
        cls.state_before = snapshot(cls.lifecycle.state)
        cls.home_before = snapshot(cls.lifecycle.home)
        cls.lifecycle.brew(cls.prefix, 'install', '--formula', 'fixture/hollis/hollis')

    @classmethod
    def tearDownClass(cls):
        cls.lifecycle.server.stop()
        cls.base_handle.cleanup()

    def opt(self):
        return self.prefix / 'opt/hollis'

    def assert_no_side_effects(self, state_before):
        self.assertEqual(snapshot(self.lifecycle.state), state_before)
        # brew itself records its local tap trust under HOME/.homebrew; every
        # other HOME change, including any Hollis state, must be absent.
        home_after = {path: entry for path, entry in snapshot(self.lifecycle.home).items()
                      if not path.startswith('.homebrew')}
        self.assertEqual(home_after, self.home_before)
        self.assertFalse(self.lifecycle.shim_log.exists())

    def test_010_clean_prefix_install_layout_completions_and_no_state_writes(self):
        libexec = self.opt() / 'libexec/0.4.0'
        self.assertTrue(libexec.is_dir())
        for name in BUNDLE_MEMBERS:
            member = libexec / name
            self.assertTrue(member.is_file() and not member.is_symlink(), name)
        self.assertEqual(run([str(self.prefix / 'bin/hollis'), '--version']).strip(),
                         'hollis 0.4.0')
        self.assertEqual(run([str(libexec / 'hollis-native'), '--version']).strip(),
                         f'hollis-native {self.lifecycle.helper_version}')
        self.assertEqual(run([str(libexec / 'hollis-native'), '--protocol-version']).strip(), '1')
        for relative in ('etc/bash_completion.d/hollis', 'share/zsh/site-functions/_hollis',
                         'share/fish/vendor_completions.d/hollis.fish',
                         'share/hollis/completion/hollis.ps1'):
            self.assertTrue((self.opt() / relative).stat().st_size > 0, relative)
        self.assert_no_side_effects(self.state_before)
        self.assertTrue(all(request.endswith('.zip')
                            for request in self.lifecycle.server.requests))

    def test_020_symlink_invocation_resolves_sibling_helper(self):
        link = self.prefix / 'bin/hollis'
        self.assertTrue(link.is_symlink())
        resolved = link.resolve()
        self.assertEqual(resolved.parent, (self.opt() / 'libexec/0.4.0').resolve())
        sibling = resolved.parent / 'hollis-native'
        self.assertTrue(sibling.is_file())
        self.assertTrue(os.access(sibling, os.X_OK))
        self.lifecycle.brew(self.prefix, 'test', 'fixture/hollis/hollis')

    def test_030_formula_test_rejects_helper_mismatch(self):
        helper = self.opt() / 'libexec/0.4.0/hollis-native'
        original = helper.read_bytes()
        self.addCleanup(helper.write_bytes, original)
        helper.write_text('#!/bin/bash\n[[ "$1" == --version ]] && echo "hollis-native 9.9.9"\n')
        helper.chmod(0o755)
        tampered = subprocess.run(
            [str(self.prefix / 'bin/brew'), 'test', 'fixture/hollis/hollis'],
            env=self.lifecycle.brew_env(), capture_output=True, text=True, timeout=900)
        self.assertNotEqual(tampered.returncode, 0)
        helper.write_bytes(original)
        helper.chmod(0o755)
        self.lifecycle.brew(self.prefix, 'test', 'fixture/hollis/hollis')

    def test_040_upgrade_preserves_conversations_and_config(self):
        environment = self.lifecycle.brew_env()
        run([str(self.prefix / 'bin/hollis'), 'config', 'set', 'model', 'local'], env=environment)
        chats_before = run([str(self.prefix / 'bin/hollis'), 'chats', 'list', '--json'],
                           env=environment)
        state_before = snapshot(self.lifecycle.state)
        self.lifecycle.commit_formula_to_prefix_tap(self.prefix, '0.5.0')
        self.lifecycle.brew(self.prefix, 'upgrade', 'fixture/hollis/hollis')
        self.assertEqual(run([str(self.prefix / 'bin/hollis'), '--version']).strip(),
                         'hollis 0.5.0')
        self.assertTrue((self.prefix / 'opt/hollis/libexec/0.5.0/hollis-native').is_file())
        self.assertEqual(snapshot(self.lifecycle.state), state_before)
        chats_after = run([str(self.prefix / 'bin/hollis'), 'chats', 'list', '--json'],
                          env=environment)
        self.assertEqual(chats_after, chats_before)
        self.assertFalse(self.lifecycle.shim_log.exists())

    def test_050_uninstall_retains_conversations_and_config(self):
        state_before = snapshot(self.lifecycle.state)
        self.lifecycle.brew(self.prefix, 'uninstall', 'fixture/hollis/hollis')
        # Uninstall removes the active keg and its public bin links; a stale
        # pre-upgrade keg may linger until cleanup.
        self.assertFalse((self.prefix / 'Cellar/hollis/0.5.0').exists())
        self.assertFalse((self.prefix / 'bin/hollis').exists())
        self.assertFalse((self.prefix / 'bin/hollis-native').exists())
        self.assertTrue(self.lifecycle.state.joinpath('hollis.db').is_file())
        self.assertTrue(self.lifecycle.state.joinpath('config.json').is_file())
        self.assertEqual(snapshot(self.lifecycle.state), state_before)
        self.assertFalse(self.lifecycle.shim_log.exists())

    def test_100_install_into_prefix_path_with_spaces(self):
        prefix = self.lifecycle.build_prefix('prefix space')
        self.lifecycle.build_tap(prefix, '0.4.0')
        state_before = snapshot(self.lifecycle.state)
        try:
            self.lifecycle.brew(prefix, 'install', '--formula', 'fixture/hollis/hollis')
            self.assertEqual(run([str(prefix / 'bin/hollis'), '--version']).strip(),
                             'hollis 0.4.0')
            self.assertTrue((prefix / 'opt/hollis/libexec/0.4.0/hollis-native').is_file())
            self.assertEqual(snapshot(self.lifecycle.state), state_before)
        finally:
            self.lifecycle.brew(prefix, 'uninstall', 'fixture/hollis/hollis')


if __name__ == '__main__':
    unittest.main()
