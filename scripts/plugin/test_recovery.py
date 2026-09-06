"""Provider-free recovery contracts for the Hollis plugin installer.

The shared installer fixture replaces platform checks and runtime assets only in
temporary copies. These tests do not call Apple services or mutate host state.
"""
import json
from pathlib import Path
import shutil
import sys
import unittest


SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

import test_plugin as plugin_fixture


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        plugin_fixture.InstallerTests.setUp(self)

    def call(self, *args, script="setup.sh", input=None):
        return plugin_fixture.InstallerTests.call(
            self, *args, script=script, input=input
        )

    def relock(self):
        plugin_fixture.InstallerTests.relock(self)

    def install(self):
        return plugin_fixture.InstallerTests.install(self)

    def old_version(self, version):
        plugin_fixture.InstallerTests.old_version(self, version)

    def result_json(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)
        data = json.loads(result.stdout)
        self.assertNotIn(str(self.base), data["rollback"]["message"])
        return data

    def assert_rollback(self, data, status, version):
        self.assertEqual(
            set(data["rollback"]), {"status", "version", "message"}
        )
        self.assertEqual(data["rollback"]["status"], status)
        self.assertEqual(data["rollback"]["version"], version)
        self.assertTrue(data["rollback"]["message"])
        self.assertNotIn("/", data["rollback"]["message"])

    def assert_refused_without_changes(
        self, current, previous, state, rollback_version
    ):
        result = self.call("rollback")
        self.assertNotEqual(result.returncode, 0)
        data = json.loads(result.stdout)
        self.assertEqual(data["status"], "action_required")
        self.assert_rollback(data, "unavailable", rollback_version)
        self.assertEqual((self.home / "current").read_text(), current)
        self.assertEqual((self.home / "previous").read_text(), previous)
        self.assertEqual(state.read_text(), "preserve me")

    def test_healthy_upgrade_reports_verified_rollback_and_switches_safely(self):
        self.old_version("0.2.0")
        install = self.result_json(self.call("install"))
        self.assert_rollback(install, "available", "0.2.0")
        check = self.result_json(self.call("check"))
        self.assert_rollback(check, "available", "0.2.0")

        restored = self.result_json(self.call("rollback"))
        self.assertEqual((self.home / "current").read_text(), "0.2.0\n")
        self.assertEqual((self.home / "previous").read_text(), "0.3.0\n")
        self.assert_rollback(restored, "available", "0.3.0")

    def test_corrupt_previous_does_not_block_repair_upgrade_or_get_advertised(self):
        state = Path(self.env["HOLLIS_STATE_DIR"])
        state.mkdir()
        marker = state / "marker"
        marker.write_text("preserve me")
        self.old_version("0.2.0")
        old_binary = self.home / "versions/0.2.0/hollis"
        with old_binary.open("a") as stream:
            stream.write("# pre-existing corruption\n")

        install = self.result_json(self.call("install"))
        self.assertEqual((self.home / "current").read_text(), "0.3.0\n")
        self.assertEqual((self.home / "previous").read_text(), "0.2.0\n")
        self.assertIn("pre-existing corruption", old_binary.read_text())
        self.assert_rollback(install, "unavailable", "0.2.0")
        self.assert_refused_without_changes(
            "0.3.0\n", "0.2.0\n", marker, "0.2.0"
        )

    def test_nonexecutable_previous_is_unavailable_and_never_selected(self):
        state = Path(self.env["HOLLIS_STATE_DIR"])
        state.mkdir()
        marker = state / "marker"
        marker.write_text("preserve me")
        self.old_version("0.2.0")
        old_binary = self.home / "versions/0.2.0/hollis"
        old_binary.chmod(0o600)

        install = self.result_json(self.call("install"))
        self.assert_rollback(install, "unavailable", "0.2.0")
        self.assertEqual(old_binary.stat().st_mode & 0o777, 0o600)
        self.assert_refused_without_changes(
            "0.3.0\n", "0.2.0\n", marker, "0.2.0"
        )

    def test_missing_previous_is_none_but_missing_candidate_is_unavailable(self):
        self.install()
        check = self.result_json(self.call("check"))
        self.assertEqual(check["status"], "runtime_installed")
        self.assert_rollback(check, "none", None)
        path = self.call("path")
        self.assertEqual(path.returncode, 0, path.stderr)
        self.assertEqual(
            path.stdout,
            str(self.home / "versions/0.3.0/hollis") + "\n",
        )

        state = Path(self.env["HOLLIS_STATE_DIR"])
        state.mkdir()
        marker = state / "marker"
        marker.write_text("preserve me")
        (self.home / "previous").write_text("0.2.0\n")
        check = self.result_json(self.call("check"))
        self.assert_rollback(check, "unavailable", "0.2.0")
        self.assert_refused_without_changes(
            "0.3.0\n", "0.2.0\n", marker, "0.2.0"
        )

    def test_symlinked_previous_pointer_and_runtime_are_never_followed(self):
        self.install()
        state = Path(self.env["HOLLIS_STATE_DIR"])
        state.mkdir()
        marker = state / "marker"
        marker.write_text("preserve me")

        outside_pointer = self.base / "outside-previous"
        outside_pointer.write_text("0.2.0\n")
        pointer = self.home / "previous"
        pointer.symlink_to(outside_pointer)
        check = self.result_json(self.call("check"))
        self.assert_rollback(check, "unavailable", None)
        result = self.call("rollback")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(outside_pointer.read_text(), "0.2.0\n")
        self.assertEqual((self.home / "current").read_text(), "0.3.0\n")
        self.assertEqual(marker.read_text(), "preserve me")

        pointer.unlink()
        outside_runtime = self.base / "outside-runtime"
        outside_runtime.mkdir()
        shutil.copy(self.binary, outside_runtime / "hollis")
        old_lock = dict(self.lock, version="0.2.0")
        (outside_runtime / "runtime.lock.json").write_text(json.dumps(old_lock))
        (self.home / "versions/0.2.0").symlink_to(
            outside_runtime, target_is_directory=True
        )
        pointer.write_text("0.2.0\n")
        check = self.result_json(self.call("check"))
        self.assert_rollback(check, "unavailable", "0.2.0")
        self.assert_refused_without_changes(
            "0.3.0\n", "0.2.0\n", marker, "0.2.0"
        )
        self.assertTrue((outside_runtime / "hollis").exists())

    def test_malformed_previous_survives_repeated_install_without_harming_current(self):
        self.install()
        state = Path(self.env["HOLLIS_STATE_DIR"])
        state.mkdir()
        marker = state / "marker"
        marker.write_text("preserve me")
        (self.home / "previous").write_text("not-a-version\n")

        repeated = self.result_json(self.call("install"))
        self.assert_rollback(repeated, "unavailable", None)
        self.assert_refused_without_changes(
            "0.3.0\n", "not-a-version\n", marker, None
        )

    def test_malformed_previous_receipt_is_unavailable_and_preserved(self):
        self.old_version("0.2.0")
        self.install()
        state = Path(self.env["HOLLIS_STATE_DIR"])
        state.mkdir()
        marker = state / "marker"
        marker.write_text("preserve me")
        receipt = self.home / "versions/0.2.0/runtime.lock.json"
        receipt.write_text("{malformed receipt\n")

        check = self.result_json(self.call("check"))
        self.assert_rollback(check, "unavailable", "0.2.0")
        self.assert_refused_without_changes(
            "0.3.0\n", "0.2.0\n", marker, "0.2.0"
        )
        self.assertEqual(receipt.read_text(), "{malformed receipt\n")

    def test_selected_current_still_fails_closed_on_corruption(self):
        current = self.install()
        with (current / "hollis").open("a") as stream:
            stream.write("# selected corruption\n")

        for command in ("check", "path"):
            with self.subTest(command=command):
                result = self.call(command)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Integrity check failed", result.stderr)
                self.assertEqual(result.stdout, "")
                self.assertEqual(
                    (self.home / "current").read_text(), "0.3.0\n"
                )

    def test_dangling_current_is_preserved_and_never_treated_as_absent(self):
        self.home.mkdir(mode=0o700)
        current = self.home / "current"
        target = self.base / "missing-current-target"
        current.symlink_to(target)
        for args in (("check",), ("path",), ("status", "cloud"), ("install",)):
            with self.subTest(args=args):
                result = self.call(*args)
                self.assertEqual(result.returncode, 10)
                self.assertEqual(result.stdout, "")
                self.assertTrue(current.is_symlink())
                self.assertEqual(current.readlink(), target)
                self.assertFalse(target.exists())

    def test_managed_newer_reports_rollback_but_external_newer_output_stays_unmanaged(self):
        self.old_version("0.2.0")
        self.old_version("0.4.0")
        (self.home / "previous").write_text("0.2.0\n")
        managed = self.result_json(self.call("install"))
        self.assertEqual(managed["status"], "existing_newer")
        self.assertEqual((self.home / "current").read_text(), "0.4.0\n")
        self.assert_rollback(managed, "available", "0.2.0")
        managed_check = self.result_json(self.call("check"))
        self.assertEqual(managed_check["status"], "runtime_installed")
        self.assert_rollback(managed_check, "available", "0.2.0")
        managed_path = self.call("path")
        self.assertEqual(managed_path.returncode, 0, managed_path.stderr)
        self.assertEqual(
            managed_path.stdout,
            str(self.home / "versions/0.4.0/hollis") + "\n",
        )

        external_home = self.base / "external-home"
        self.env["HOLLIS_PLUGIN_HOME"] = str(external_home)
        binary_dir = self.base / "bin"
        binary_dir.mkdir()
        external = binary_dir / "hollis"
        external.write_text("#!/bin/bash\necho 'hollis 0.4.0'\n")
        external.chmod(0o700)
        self.env["HOLLIS_CALLER_PATH"] = str(binary_dir)
        install = self.call("install")
        self.assertEqual(install.returncode, 0, install.stderr)
        self.assertNotIn("rollback", json.loads(install.stdout))
        path = self.call("path")
        self.assertEqual(path.returncode, 0, path.stderr)
        self.assertEqual(path.stdout, str(external) + "\n")


if __name__ == "__main__":
    unittest.main()
