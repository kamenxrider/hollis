"""Focused provider-free readiness tests for the Hollis bridge wrapper."""

import json
import os
import shlex
import unittest

import test_plugin as plugin_fixtures


class ReadinessTests(unittest.TestCase):
    """Exercise status boundaries while reusing the existing installer fixture."""

    def setUp(self):
        self.fixture = plugin_fixtures.InstallerTests("runTest")
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.base = self.fixture.base
        self.home = self.fixture.home

    def call(self, *args):
        return self.fixture.call(*args)

    def replace_shortcuts(self, script):
        helper = self.base / "shortcuts"
        helper.write_text(script)
        helper.chmod(0o700)
        bridge = self.fixture.kit / "scripts/bridges.sh"
        bridge.write_text(
            bridge.read_text()
            .replace("/usr/bin/shortcuts", str(helper))
            .replace("/usr/bin/open", str(helper))
        )
        return helper

    def configure_external(self):
        external_dir = self.base / "external-bin"
        external_dir.mkdir()
        external = external_dir / "hollis"
        external_log = self.base / "external-calls"
        quoted_log = shlex.quote(str(external_log))
        external.write_text(
            "#!/bin/bash\n"
            f"printf '%s\\n' \"$*\" >> {quoted_log}\n"
            "case \"${1:-}\" in\n"
            "  --version) printf 'hollis 0.4.0\\n' ;;\n"
            "  config) printf '%s\\n' '{\"bridges\":{},\"image_bridge\":\"\"}' ;;\n"
            "  doctor) printf '%s\\n' '{\"bridges\":[{\"model\":\"cloud\",\"verified\":true,\"resolved_ref\":\"External Cloud\",\"status\":\"ok\"}]}' ;;\n"
            "  *) exit 0 ;;\n"
            "esac\n"
        )
        external.chmod(0o700)
        self.fixture.env["HOLLIS_CALLER_PATH"] = str(external_dir)
        shortcut_log = self.base / "shortcuts-calls"
        quoted_shortcuts = shlex.quote(str(shortcut_log))
        self.replace_shortcuts(
            "#!/bin/bash\n"
            f"printf '%s\\n' \"$*\" >> {quoted_shortcuts}\n"
            "printf 'External Cloud\\n'\n"
        )
        return external, external_log, shortcut_log

    def test_absent_runtime_is_setup_required_without_discovery_or_writes(self):
        calls = self.base / "shortcuts-calls"
        quoted_calls = shlex.quote(str(calls))
        self.replace_shortcuts(
            "#!/bin/bash\n"
            f"printf 'called\\n' >> {quoted_calls}\n"
            "exit 97\n"
        )

        result = self.call("status", "cloud")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["status"], "setup_required")
        self.assertEqual(result.stderr, "")
        self.assertFalse(calls.exists(), "status must not run bridge discovery")
        self.assertFalse(self.home.exists(), "status must not create installation state")

    def test_dangling_current_is_corruption_not_setup_required(self):
        self.home.mkdir(mode=0o700)
        current = self.home / "current"
        target = self.base / "missing-current-target"
        current.symlink_to(target)

        result = self.call("status", "cloud")

        self.assertEqual(result.returncode, 10)
        self.assertIn("Expected a regular file", result.stderr)
        self.assertEqual(result.stdout, "")
        self.assertTrue(current.is_symlink())
        self.assertEqual(os.readlink(current), str(target))

    def test_inaccessible_runtime_parent_is_access_required_not_setup_required(self):
        blocked = self.base / "blocked-parent"
        blocked.mkdir(mode=0o700)
        self.fixture.env["HOLLIS_PLUGIN_HOME"] = str(blocked / "runtime")
        blocked.chmod(0)
        self.addCleanup(lambda: blocked.chmod(0o700))

        result = self.call("status", "cloud")

        self.assertEqual(result.returncode, 5)
        self.assertEqual(json.loads(result.stdout)["status"], "access_required")
        # Restore traversal before inspecting the no-write postcondition;
        # older Python versions raise PermissionError for exists() here.
        blocked.chmod(0o700)
        self.assertFalse((blocked / "runtime").exists())

    def test_present_corrupt_binary_still_fails_before_discovery(self):
        version = self.fixture.install()
        with (version / "hollis").open("a") as stream:
            stream.write("# corruption\n")
        calls = self.base / "shortcuts-calls"
        quoted_calls = shlex.quote(str(calls))
        self.replace_shortcuts(
            "#!/bin/bash\n"
            f"printf 'called\\n' >> {quoted_calls}\n"
            "exit 97\n"
        )

        result = self.call("status", "cloud")

        self.assertEqual(result.returncode, 10)
        self.assertIn("Integrity check failed", result.stderr)
        self.assertFalse(calls.exists(), "corrupt runtimes must fail before discovery")
        self.assertEqual((self.home / "current").read_text(), "0.3.0\n")

    def test_present_corrupt_receipt_still_fails(self):
        version = self.fixture.install()
        receipt = version / "runtime.lock.json"
        data = json.loads(receipt.read_text())
        data["version"] = "0.2.0"
        receipt.write_text(json.dumps(data))

        result = self.call("status", "cloud")

        self.assertEqual(result.returncode, 10)
        self.assertIn("does not match its version", result.stderr)
        self.assertEqual((self.home / "current").read_text(), "0.3.0\n")

    def test_discovery_failure_remains_unknown_with_installed_runtime(self):
        self.fixture.install()
        self.replace_shortcuts("#!/bin/bash\nexit 97\n")

        result = self.call("status", "cloud")

        self.assertEqual(result.returncode, 5)
        self.assertEqual(json.loads(result.stdout)["status"], "unknown")
        self.assertTrue((self.home / "current").is_file())

    def test_newer_external_runtime_is_preserved_and_selected(self):
        external, external_log, shortcut_log = self.configure_external()

        result = self.call("status", "cloud")

        self.assertEqual(result.returncode, 0, result.stderr)
        payload = json.loads(result.stdout)
        self.assertFalse(payload["inference_tested"])
        self.assertEqual(payload["routes"][0]["status"], "discovered")
        self.assertFalse(self.home.exists(), "external runtime must not create managed state")
        calls = external_log.read_text()
        self.assertIn("--version", calls)
        self.assertIn("config show", calls)
        self.assertIn("doctor --json", calls)
        self.assertTrue(shortcut_log.exists())

        path = self.call("path")
        self.assertEqual(path.returncode, 0, path.stderr)
        self.assertEqual(path.stdout.strip(), str(external))

    def test_newer_external_runtime_precedes_corrupt_managed_runtime(self):
        version = self.fixture.install()
        with (version / "hollis").open("a") as stream:
            stream.write("# corruption\n")
        external, external_log, _ = self.configure_external()

        result = self.call("status", "cloud")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["routes"][0]["status"], "discovered")
        self.assertEqual((self.home / "current").read_text(), "0.3.0\n")
        self.assertIn("--version", external_log.read_text())
        self.assertTrue(external.is_file())


if __name__ == "__main__":
    unittest.main()
