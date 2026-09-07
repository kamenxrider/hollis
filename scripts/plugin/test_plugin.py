"""Provider-free regression tests. Fixtures replace Apple helpers only in temp copies."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest
from unittest.mock import patch
import zipfile

import package_plugin as packaging

ROOT = Path(__file__).resolve().parents[2]


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        self.kit = self.base / "kit"
        shutil.copytree(ROOT / "plugins/hollis", self.kit, ignore=shutil.ignore_patterns("runtime"))
        # CI may be macOS 26. Real hardware classification is tested separately.
        with (self.kit / "scripts/common.sh").open("a") as f:
            f.write('\nplatform() { return 0; }\n')
        self.home = self.base / "runtime"
        self.env = dict(os.environ, HOLLIS_PLUGIN_HOME=str(self.home), HOLLIS_CALLER_PATH="/usr/bin:/bin", HOLLIS_STATE_DIR=str(self.base / "user-state"))
        self.assets = self.kit / "assets/runtime"
        self.assets.mkdir(parents=True)
        self.lock = json.loads((self.kit / "runtime.lock.json").read_text())
        # Keep the synthetic upgrade matrix stable as the production pin moves.
        # Actual locked assets are exercised by the release package acceptance.
        self.lock.update(version="0.3.0", source_ref="refs/tags/v0.3.0",
                         release_url="https://github.com/kamenxrider/hollis/releases/download/v0.3.0")
        self.binary = self.assets / self.lock["binary"]["name"]
        self.binary.write_text('''#!/bin/bash
if [[ -n ${CALL_LOG:-} ]]; then printf '%s\\n' "$*" >> "$CALL_LOG"; fi
case "$1" in
 --version) echo 'hollis 0.3.0';;
 respond) if [[ ${2:-} == --slow-fixture ]]; then touch "$READY_FILE"; exec /bin/sleep 30; fi; cat; exit 7;;
 config) if [[ "$2" == show ]]; then
   if [[ -n ${FIXTURE_CONFIG:-} ]]; then printf '%s\\n' "$FIXTURE_CONFIG"; else echo '{"bridges":{},"image_bridge":""}'; fi
 fi;;
 doctor) echo '{"bridges":[{"model":"cloud","verified":true,"resolved_ref":"Custom Cloud","status":"ok"},{"model":"cloud-pro","verified":false,"resolved_ref":"","status":"missing"}]}'; exit 3;;
 *) exit 0;;
esac
''')
        self.binary.chmod(0o700)
        self.zip = self.assets / self.lock["bridges"]["name"]
        with zipfile.ZipFile(self.zip, "w") as archive:
            for name in self.lock["bridge_files"]:
                archive.writestr(name, "test-only shortcut")
        self.relock()

    def relock(self):
        self.lock["binary"]["sha256"] = packaging.digest(self.binary)
        self.lock["bridges"]["sha256"] = packaging.digest(self.zip)
        (self.kit / "runtime.lock.json").write_text(json.dumps(self.lock))

    def call(self, *args, script="setup.sh", input=None):
        return subprocess.run(["/bin/bash", str(self.kit / "scripts" / script), *args], env=self.env, input=input, text=True, capture_output=True, timeout=15)

    def install(self):
        result = self.call("install")
        self.assertEqual(result.returncode, 0, result.stderr)
        return self.home / "versions/0.3.0"

    def shortcuts(self, output="", code=0):
        helper = self.base / "shortcuts"
        # output is controlled fixture data, never user input.
        helper.write_text(f"#!/bin/bash\nprintf '%s\\n' '{output}'\nexit {code}\n")
        helper.chmod(0o700)
        bridge = self.kit / "scripts/bridges.sh"
        bridge.write_text(bridge.read_text().replace("/usr/bin/shortcuts", str(helper)).replace("/usr/bin/open", str(helper)))

    def test_install_is_private_repeatable_and_preserves_existing_state(self):
        state = Path(self.env["HOLLIS_STATE_DIR"])
        state.mkdir(); (state / "config.json").write_text('{"custom":"preserve"}')
        self.install(); self.install()
        self.assertEqual((self.home / "current").read_text(), "0.3.0\n")
        self.assertEqual(self.home.stat().st_mode & 0o777, 0o700)
        self.assertEqual((state / "config.json").read_text(), '{"custom":"preserve"}')
        self.assertFalse((self.home / "setup.lock").exists())

    def test_corruption_stops_before_install_or_execution(self):
        with self.binary.open("a") as f: f.write("# corruption\n")
        result = self.call("install")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Integrity check failed", result.stderr)
        self.assertFalse((self.home / "current").exists())
        self.assertFalse(list((self.home / "versions").glob(".stage.*")))

    def test_corrupted_installed_runtime_is_not_executed(self):
        version = self.install()
        with (version / "hollis").open("a") as f: f.write("# modified\n")
        self.assertNotEqual(self.call("--version", script="run.sh").returncode, 0)

    def test_archive_traversal_rejected_even_with_trusted_fixture_hash(self):
        with zipfile.ZipFile(self.zip, "a") as archive: archive.writestr("../escape", "no")
        self.relock()
        result = self.call("install")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("exactly the five", result.stderr)
        self.assertFalse((self.home / "current").exists())

    def test_symlink_install_destination_rejected(self):
        other = self.base / "other"; other.mkdir()
        self.home.symlink_to(other, target_is_directory=True)
        self.assertNotEqual(self.call("install").returncode, 0)
        self.assertEqual(list(other.iterdir()), [])

    def test_active_or_interrupted_lock_preserved_until_inspected(self):
        lock = self.home / "setup.lock"; lock.mkdir(parents=True)
        (lock / "pid").write_text("12345")
        result = self.call("install")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("interrupted", result.stderr)
        self.assertEqual((lock / "pid").read_text(), "12345")
        (lock / "pid").unlink(); lock.rmdir()  # Simulate operator-confirmed stale lock recovery.
        self.install()

    def old_version(self, version):
        previous = self.home / "versions" / version
        previous.mkdir(parents=True)
        shutil.copy(self.binary, previous / "hollis")
        lock = dict(self.lock, version=version)
        (previous / "runtime.lock.json").write_text(json.dumps(lock))
        (self.home / "current").write_text(version + "\n")

    def test_denied_lock_creation_is_access_required_not_busy(self):
        self.install()
        with (self.kit / "scripts/common.sh").open("a") as f:
            f.write('\nmkdir() { if [[ "$1" == *.lock ]]; then return 1; fi; /bin/mkdir "$@"; }\n')
        result = self.call("install")
        self.assertEqual(result.returncode, 5, result.stderr)
        self.assertEqual(json.loads(result.stdout)["status"], "access_required")
        self.assertNotIn("interrupted", result.stderr)
        self.assertFalse((self.home / "setup.lock").exists())

    def test_status_runs_concurrently_without_writing_runtime_or_setup_lock(self):
        self.install(); self.shortcuts("Custom Cloud")
        lock = self.home / "setup.lock"; lock.mkdir()
        (lock / "pid").write_text("12345")
        before = {str(p.relative_to(self.home)): p.stat().st_mtime_ns for p in self.home.rglob("*")}
        # A sandbox can permit reading this installation while denying writes.
        with (self.kit / "scripts/common.sh").open("a") as f:
            f.write('\nprivate_dir() { echo "Unexpected runtime write" >&2; exit 99; }\n')
        children = [subprocess.Popen(["/bin/bash", str(self.kit / "scripts/setup.sh"), "status", "cloud"], env=self.env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE) for _ in range(2)]
        for child in children:
            out, err = child.communicate(timeout=15)
            self.assertEqual(child.returncode, 0, err)
            self.assertEqual(json.loads(out)["routes"][0]["status"], "discovered")
        after = {str(p.relative_to(self.home)): p.stat().st_mtime_ns for p in self.home.rglob("*")}
        self.assertEqual(before, after)

    def test_upgrade_and_rollback_retain_both_runtimes(self):
        self.old_version("0.2.0")
        self.install()
        self.assertEqual((self.home / "previous").read_text().strip(), "0.2.0")
        result = self.call("rollback")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.home / "current").read_text().strip(), "0.2.0")
        self.assertTrue((self.home / "versions/0.3.0/hollis").exists())

    def test_newer_managed_version_never_silently_downgraded(self):
        self.old_version("0.4.0")
        result = self.call("install")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["status"], "existing_newer")
        self.assertEqual((self.home / "current").read_text().strip(), "0.4.0")

    def test_newer_path_install_is_preserved_and_bridge_kit_still_available(self):
        path = self.base / "bin"; path.mkdir()
        binary = path / "hollis"; binary.write_text("#!/bin/bash\necho 'hollis 0.4.0'\n"); binary.chmod(0o700)
        self.env["HOLLIS_CALLER_PATH"] = str(path)
        result = self.call("install")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.home / "current").exists())
        self.assertEqual(self.call("path").stdout.strip(), str(binary))
        self.assertTrue((self.home / "versions/0.3.0/hollis-bridges.zip").exists())

    def test_piped_input_stdout_and_failure_exit_survive_launcher(self):
        self.install()
        result = self.call("respond", "--model", "cloud", script="run.sh", input="Text with $() and quotes ' \"\n")
        self.assertEqual(result.returncode, 7, result.stderr)
        self.assertEqual(result.stdout, "Text with $() and quotes ' \"\n")
        self.assertTrue((self.home / "last-call-ended").is_file())
        self.assertFalse((self.home / "inference.lock").exists())

    def test_tier_pacing_and_stored_batch_tier(self):
        self.install()
        log = self.base / "sleeps"
        with (self.kit / "scripts/common.sh").open("a") as f:
            f.write(f"\ndate() {{ echo 1000; }}\nsleep() {{ echo \"$1\" >> '{log}'; }}\n")
        job = self.base / "job.json"
        # A guard second covers the fractional part discarded by date +%s.
        cases = [(["respond", "--model", "cloud"], 6), (["respond", "--model=cloud-pro"], 46), (["chat", "--continue", "id"], 46)]
        for arguments, expected in cases:
            (self.home / "last-call-ended").write_text("1000\n")
            self.call(*arguments, script="run.sh", input="test")
            self.assertEqual(int(log.read_text().splitlines()[-1]), expected)
        for model, expected in [("cloud", 6), ("cloud-pro", 46)]:
            job.write_text(json.dumps({"model": model}))
            self.call("batch", "resume", "--job", str(job), "--max-calls", "1", script="run.sh")
            self.assertEqual(int(log.read_text().splitlines()[-1]), expected)

    def test_cancel_forwards_signal_records_pacing_and_releases_lock(self):
        self.install()
        ready = self.base / "ready"; self.env["READY_FILE"] = str(ready)
        child = subprocess.Popen(["/bin/bash", str(self.kit / "scripts/run.sh"), "respond", "--slow-fixture"], env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            deadline = time.monotonic() + 5
            while not ready.exists() and time.monotonic() < deadline: time.sleep(0.02)
            self.assertTrue(ready.exists())
            child.terminate()
            child.communicate(timeout=5)
            self.assertEqual(child.returncode, 143)
            self.assertTrue((self.home / "last-call-ended").is_file())
            self.assertFalse((self.home / "inference.lock").exists())
        finally:
            if child.poll() is None:
                child.terminate(); child.communicate(timeout=5)

    def test_unrelated_missing_bridge_does_not_disable_selected_cloud(self):
        self.install()
        helper = self.base / "shortcuts"; helper.write_text("#!/bin/bash\necho 'Custom Cloud'\n"); helper.chmod(0o700)
        bridge = self.kit / "scripts/bridges.sh"
        bridge.write_text(bridge.read_text().replace("/usr/bin/shortcuts", str(helper)))
        result = self.call("status", "cloud")
        self.assertEqual(result.returncode, 0, result.stderr)
        data = json.loads(result.stdout)
        self.assertFalse(data["inference_tested"])
        self.assertEqual(data["routes"][0]["status"], "discovered")
        result = self.call("import", "cloud")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.home / "imports/cloud.discovered").read_text().strip(), "Custom Cloud")

    def test_helper_permission_failure_is_unknown_not_missing(self):
        self.install()
        helper = self.base / "shortcuts"; helper.write_text("#!/bin/bash\nexit 1\n"); helper.chmod(0o700)
        bridge = self.kit / "scripts/bridges.sh"
        bridge.write_text(bridge.read_text().replace("/usr/bin/shortcuts", str(helper)))
        result = self.call("status", "cloud")
        self.assertEqual(result.returncode, 5)
        self.assertEqual(json.loads(result.stdout)["status"], "unknown")

    def test_status_identifies_runtime_config_and_discovery_outcome(self):
        self.install()
        self.env["FIXTURE_CONFIG"] = json.dumps({"path": str(self.base / "isolated state/config.json"), "bridges": {}, "image_bridge": ""})
        self.shortcuts()
        result = self.call("status", "image")
        data = json.loads(result.stdout)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(data["config_path"], str(self.base / "isolated state/config.json"))
        self.assertEqual(data["runtime_path"], str(self.home / "versions" / self.lock["version"] / "hollis"))
        self.assertEqual(data["discovery"], {"status": "completed", "exit_code": 0, "listed_shortcuts": 0})
        self.assertEqual(data["routes"][0]["status"], "missing")
        helper = self.base / "shortcuts"
        helper.write_text("#!/bin/bash\necho '/private/path token=CANARY' >&2\nexit 1\n")
        failed = self.call("status", "image")
        data = json.loads(failed.stdout)
        self.assertEqual(data["status"], "unknown")
        self.assertEqual(data["config_path"], str(self.base / "isolated state/config.json"))
        self.assertEqual(data["discovery"], {"status": "failed", "exit_code": 1})
        self.assertNotIn("CANARY", failed.stdout + failed.stderr)

    def test_config_failure_reports_unverified_path_before_discovery(self):
        source = self.binary.read_text().replace('config) if [[ "$2" == show ]]; then',
            'config) echo "/private/customer token=CANARY" >&2; exit 10; if [[ "$2" == show ]]; then')
        self.binary.write_text(source); self.relock(); self.install()
        self.shortcuts()
        result = self.call("status", "image")
        data = json.loads(result.stdout)
        self.assertEqual(result.returncode, 10)
        self.assertEqual(data["status"], "unknown")
        self.assertIsNone(data["config_path"])
        self.assertEqual(data["discovery"], {"status": "not_attempted", "exit_code": None})
        self.assertNotIn("CANARY", result.stdout + result.stderr)

    def test_image_discovery_and_configuration_are_separate(self):
        self.install(); self.shortcuts("Hollis Image - Reference Input v2")
        status = json.loads(self.call("status", "image").stdout)["routes"][0]
        self.assertEqual(status["status"], "discovered")
        self.assertFalse(status["configured"])
        log = self.base / "calls"; self.env["CALL_LOG"] = str(log)
        result = self.call("import", "image")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("config set image-bridge Hollis Image - Reference Input v2", log.read_text())

    def test_missing_custom_bridge_is_preserved(self):
        self.install(); self.shortcuts()
        self.env["FIXTURE_CONFIG"] = '{"bridges":{"cloud-pro":"My private custom bridge"},"image_bridge":""}'
        result = self.call("import", "cloud-pro")
        self.assertEqual(result.returncode, 3)
        self.assertEqual(json.loads(result.stdout)["status"], "action_required")
        self.assertFalse((self.home / "imports").exists())

    def test_pending_import_is_not_reopened(self):
        self.install(); self.shortcuts()
        imports = self.home / "imports"; imports.mkdir()
        (imports / "pending").write_text("image\n")
        result = self.call("import", "image")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["status"], "permission_pending")
        self.assertEqual(list(imports.iterdir()), [imports / "pending"])


class PlatformTests(unittest.TestCase):
    def probe(self, arch, arm, os_version="27.0", virtual="0"):
        common = ROOT / "plugins/hollis/scripts/common.sh"
        command = f'''source '{common}'
uname() {{ if [[ "$1" == -s ]]; then echo Darwin; else echo '{arch}'; fi; }}
sysctl() {{ if [[ "$2" == kern.hv_vmm_present ]]; then echo '{virtual}'; else echo '{arm}'; fi; }}
sw_vers() {{ echo '{os_version}'; }}
platform
'''
        return subprocess.run(["/bin/bash", "-c", command], capture_output=True, text=True)

    def test_native_and_rosetta(self):
        self.assertEqual(self.probe("arm64", "").returncode, 0)
        self.assertEqual(self.probe("x86_64", "1").returncode, 0)

    def test_intel_restricted_and_untested_os_are_distinct(self):
        self.assertEqual(self.probe("x86_64", "0").returncode, 3)
        self.assertEqual(self.probe("x86_64", "").returncode, 5)
        self.assertEqual(self.probe("arm64", "1", "26.5").returncode, 3)
        self.assertEqual(self.probe("arm64", "1", virtual="1").returncode, 3)


class PackagingTests(unittest.TestCase):
    def test_plugin_release_preserves_latest_runtime_for_cli_installation(self):
        workflow = (ROOT / ".github/workflows/plugin-release.yml").read_text()
        publish = workflow.split('gh release create "$GITHUB_REF_NAME"', 1)[1]
        self.assertIn("--latest=false", publish)

    def test_source_contracts(self):
        manifest, lock = packaging.validate_source()
        self.assertEqual(manifest["version"], "0.1.0")
        self.assertEqual(lock["version"], "0.3.3")

    def test_failed_provenance_never_returns_a_receipt(self):
        _, lock = packaging.validate_source()
        with patch.object(packaging.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", "rejected")) as verify:
            with self.assertRaisesRegex(ValueError, "provenance verification failed"):
                packaging.authenticate(Path("fake-binary"), lock)
            command = verify.call_args.args[0]
            self.assertIn("--source-digest", command)
            self.assertIn(lock["source_commit"], command)
            self.assertIn("--deny-self-hosted-runners", command)

    def test_empty_verifier_success_is_rejected(self):
        _, lock = packaging.validate_source()
        with patch.object(packaging.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "[]", "")):
            with self.assertRaisesRegex(ValueError, "Empty provenance"):
                packaging.authenticate(Path("fake"), lock)


if __name__ == "__main__":
    unittest.main()
