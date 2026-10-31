"""Time bounds for the legacy Shortcuts discovery and import path.

These tests assert the bound itself, not merely that the script still runs.
A stand-in `shortcuts` binary models the hang shape (a never-exiting child
holding a same-group member); the tests prove the group is killed at the
shipped deadline constants, that the discovery and import contracts answer
honestly, that stdout stays machine-readable, and that a hung step inside
the setup lock does not leave the lock behind. `/usr/bin/shortcuts` itself
is never invoked.
"""
import json
import os
import shlex
import signal
import subprocess
import time
import unittest
from pathlib import Path

import test_plugin as plugin_fixtures

GUARD = 12  # seconds of slack above each shipped deadline


class DiscoveryBoundsTests(unittest.TestCase):
    """Drive setup.sh status/import through the InstallerTests fixture."""

    def setUp(self):
        self.fixture = plugin_fixtures.InstallerTests("runTest")
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.base = self.fixture.base

    def call(self, *args, timeout=30):
        # The stock fixture runner stops at 15 seconds; long-deadline cases
        # need a guard that proves the bound rather than killing the test.
        return subprocess.run(
            ["/bin/bash", str(self.fixture.kit / "scripts" / "setup.sh"), *args],
            env=self.fixture.env, input=None, text=True,
            capture_output=True, timeout=timeout)

    def replace_shortcuts(self, body):
        helper = self.base / "shortcuts"
        helper.write_text(body)
        helper.chmod(0o700)
        bridge = self.fixture.kit / "scripts/bridges.sh"
        bridge.write_text(
            bridge.read_text()
            .replace("/usr/bin/shortcuts", str(helper))
            # open is called with the target file as its first argument;
            # tagging preserves the stand-in's subcommand dispatch.
            .replace("/usr/bin/open", str(helper) + " open")
        )
        return helper

    def test_list_deadline_stops_group_and_reports_unknown(self):
        self.fixture.install()
        pglog_path = self.base / "bounds-pg"
        quoted_pglog = shlex.quote(str(pglog_path))
        self.replace_shortcuts(
            "#!/bin/bash\n"
            "case \"${1:-}\" in\n"
            "  list)\n"
            f"    ps -o pgid= -p $$ | tr -d ' ' >> {quoted_pglog}\n"
            f"    ( sleep 240 ) &\n"
            "    exec sleep 240\n"
            "    ;;\n"
            "  *) exit 97;;\n"
            "esac\n")
        started = time.monotonic()
        result = self.call("status", "cloud", timeout=10 + GUARD)
        elapsed = time.monotonic() - started

        self.assertLess(elapsed, 10 + 6, f'listing hung {elapsed:.1f}s past its 10s bound')
        # Lower bound: the bound must measure real seconds, not poll ticks.
        self.assertGreaterEqual(elapsed, 10 - 1)
        self.assertEqual(result.returncode, 5)
        # Named assertion: the bash "Terminated: 15" job-notification line must
        # never reach the machine-readable stream; stdout stays pure JSON.
        payload = json.loads(result.stdout)
        self.assertEqual(payload["discovery"]["status"], "failed")
        self.assertEqual(payload["discovery"]["exit_code"], 143)
        self.assertIn("Listing Shortcuts did not complete within", payload["message"])
        self.assertIn("no bridge is proven missing", payload["message"])
        self.assertNotIn("Could not list Shortcuts", payload["message"])

        # The group kill took the same-group member, not only the direct child.
        recorded = [int(line) for line in pglog_path.read_text().split() if line]
        self.assertTrue(recorded, "stand-in never recorded its process group")
        pgid = recorded[0]
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            alive = subprocess.run(
                ["/bin/bash", "-c",
                 f"ps -axo pgid= | awk '$1=={pgid}{{c++}} END{{print c+0}}'"],
                capture_output=True, text=True).stdout.strip()
            if alive == "0":
                break
            time.sleep(0.5)
        self.assertEqual(
            alive, "0", f"process group {pgid} survived the bound")

    def test_list_trapping_term_reports_deadline_not_completed(self):
        # A lister that traps TERM and completes without finishing must not
        # fall through to the completed shape: expiry answers unknown even at
        # exit code 0, and absence stays a completed-listing fact only.
        self.fixture.install()
        self.replace_shortcuts(
            "#!/bin/bash\n"
            "case \"${1:-}\" in\n"
            "  list) trap 'echo partial; exit 0' TERM; sleep 240;;\n"
            "  *) exit 97;;\n"
            "esac\n")
        result = self.call("status", "cloud", timeout=10 + GUARD)
        self.assertEqual(result.returncode, 5)
        payload = json.loads(result.stdout)
        self.assertEqual(payload["discovery"]["status"], "failed")
        self.assertEqual(payload["discovery"]["exit_code"], 0)  # the trapped lister's real code
        self.assertIn("Listing Shortcuts did not complete within", payload["message"])
        self.assertEqual(payload["message"].rsplit("within ", 1)[1].split(" ", 1)[0], "10")

    def _start_hung_listing(self):
        # Shared setup for the interruption tests: a lister that records its
        # process group, spawns a same-group member and never exits; the
        # script is started detached. Captures the hung child's group and
        # the reaper's own group (the only script child in another group).
        pglog_path = self.base / "bounds-pg"
        quoted_pglog = shlex.quote(str(pglog_path))
        self.replace_shortcuts(
            "#!/bin/bash\n"
            "case \"${1:-}\" in\n"
            "  list)\n"
            f"    ps -o pgid= -p $$ | tr -d ' ' >> {quoted_pglog}\n"
            f"    ( sleep 240 ) &\n"
            "    exec sleep 240\n"
            "    ;;\n"
            "  *) exit 97;;\n"
            "esac\n")
        proc = subprocess.Popen(
            ["/bin/bash", str(self.fixture.kit / "scripts" / "setup.sh"), "status", "cloud"],
            env=self.fixture.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, start_new_session=True)
        deadline = time.monotonic() + 8
        while not pglog_path.exists() or not pglog_path.read_text().strip():
            self.assertIsNone(proc.poll(), 'script exited before the listing started')
            if time.monotonic() > deadline:
                self.fail('listing never started within 8s')
            time.sleep(0.2)
        child_pgid = int(pglog_path.read_text().split()[0])
        listing = subprocess.run(["/bin/bash", "-c", "ps -axo pid=,ppid=,pgid="],
                                 capture_output=True, text=True).stdout.splitlines()
        children = [
            (int(fields[0]), int(fields[1]), int(fields[2]))
            for fields in (line.split() for line in listing)
            if len(fields) == 3 and fields[1] == str(proc.pid)]
        reapers = [pgid for _, _, pgid in children if pgid != child_pgid]
        self.assertTrue(reapers, "reaper process not spawned beside the hung listing")
        return proc, child_pgid, reapers[0]

    def _assert_group_gone(self, pgid, label):
        deadline = time.monotonic() + 3
        while True:
            alive = subprocess.run(
                ["/bin/bash", "-c",
                 f"ps -axo pgid= | awk '$1=={pgid}{{c++}} END{{print c+0}}'"],
                capture_output=True, text=True).stdout.strip()
            if alive == "0":
                return
            if time.monotonic() > deadline:
                self.fail(f"{label} {pgid} survived the stopped script")
            time.sleep(0.5)

    def _signal_and_wait(self, how, expected_rc):
        proc, child_pgid, reaper_pgid = self._start_hung_listing()
        with proc:  # closes the pipes when done; silences ResourceWarning
            if how == "term-pid":
                os.kill(proc.pid, signal.SIGTERM)
            else:
                # Mid-hang INT at the script's group; the bound children are
                # in their own groups and do not receive it themselves.
                os.killpg(proc.pid, signal.SIGINT)
            self.assertEqual(proc.wait(timeout=5), expected_rc)
        # Both bound groups must be gone: the hung child's and the reaper's.
        self._assert_group_gone(child_pgid, "hung child group")
        self._assert_group_gone(reaper_pgid, "reaper group")

    def test_script_term_pid_mid_hang_kills_bound_group(self):
        # Stopping the script mid-hang with TERM at its pid must not leak the
        # hung child: it lives outside the caller's process group, so
        # bound_run kills it and the reaper from the composed traps.
        self.fixture.install()
        self._signal_and_wait("term-pid", 143)

    def test_script_int_group_mid_hang_kills_bound_group(self):
        # The PR body's cancellation claim, as its own case: INT at the
        # script's group while the listing hangs must exit 130 and leave
        # zero survivors in both bound groups.
        self.fixture.install()
        self._signal_and_wait("int-group", 130)

    def test_list_success_is_unchanged_and_quiet(self):
        self.fixture.install()
        self.replace_shortcuts(
            "#!/bin/bash\n"
            "case \"${1:-}\" in\n"
            "  list) printf 'AFM Bridge - Cloud\\nAFM Bridge - Cloud Pro\\nHollis Image - Reference Input v2\\n'; exit 0;;\n"
            "  *) exit 97;;\n"
            "esac\n")
        result = self.call("status", "all")
        self.assertEqual(result.returncode, 0)
        payload = json.loads(result.stdout)  # stdout parseable despite no bound firing
        self.assertEqual(payload["discovery"]["status"], "completed")
        self.assertEqual(payload["discovery"]["exit_code"], 0)
        self.assertEqual(payload["discovery"]["listed_shortcuts"], 3)
        self.assertEqual(result.stderr, "")

    def test_list_failure_keeps_existing_message(self):
        self.fixture.install()
        self.replace_shortcuts("#!/bin/bash\nexit 6\n")
        result = self.call("status", "cloud")
        self.assertEqual(result.returncode, 5)
        payload = json.loads(result.stdout)
        self.assertEqual(payload["discovery"]["status"], "failed")
        self.assertEqual(payload["discovery"]["exit_code"], 6)
        self.assertIn("Could not list Shortcuts", payload["message"])
        self.assertNotIn("did not complete within", payload["message"])

    def test_list_empty_completed_establishes_absence(self):
        self.fixture.install()
        self.replace_shortcuts("#!/bin/bash\nexit 0\n")
        result = self.call("status", "all")
        self.assertEqual(result.returncode, 0)
        payload = json.loads(result.stdout)
        self.assertEqual(payload["discovery"]["status"], "completed")
        self.assertEqual(payload["discovery"]["listed_shortcuts"], 0)
        self.assertNotIn("did not complete within", result.stdout)
        self.assertNotIn("Could not list Shortcuts", result.stdout)

    def test_sign_deadline_releases_setup_lock(self):
        self.fixture.install()
        self.replace_shortcuts(
            "#!/bin/bash\n"
            "case \"${1:-}\" in\n"
            "  list) printf 'AFM Bridge - Cloud Pro\\n'; exit 0;;\n"
            "  sign) sleep 240;;\n"
            "  *) exit 97;;\n"
            "esac\n")
        started = time.monotonic()
        result = self.call("import", "cloud-pro", timeout=60 + GUARD)
        elapsed = time.monotonic() - started

        self.assertLess(elapsed, 60 + 6, f'signing hung {elapsed:.1f}s past its 60s bound')
        self.assertGreaterEqual(elapsed, 60 - 1)
        self.assertEqual(result.returncode, 10)
        self.assertIn("Signing did not complete within", result.stderr)
        # The point of bounding sign: the setup lock does not outlive a hang.
        self.assertFalse((self.fixture.home / "setup.lock").exists())
        self.assertFalse((self.fixture.home / "imports/pending").exists())

    def test_sign_failure_keeps_passthrough(self):
        self.fixture.install()
        self.replace_shortcuts(
            "#!/bin/bash\n"
            "case \"${1:-}\" in\n"
            "  list) exit 0;;\n"
            "  sign) exit 9;;\n"
            "  *) exit 97;;\n"
            "esac\n")
        result = self.call("import", "cloud-pro")
        self.assertEqual(result.returncode, 9)  # real failure codes stay real
        # The pre-sign user notice is longstanding; no bound text appears.
        self.assertIn("Hollis will open", result.stderr)
        self.assertNotIn("Signing did not complete within", result.stderr)
        self.assertFalse((self.fixture.home / "setup.lock").exists())

    def test_open_deadline_reports_failure_with_pending_parity(self):
        self.fixture.install()
        self.replace_shortcuts(
            "#!/bin/bash\n"
            "case \"${1:-}\" in\n"
            "  list) exit 0;;\n"
            "  sign) for arg; do case \"$prev\" in --output) printf signed > \"$arg\";; esac; prev=$arg; done; exit 0;;\n"
            "  open) sleep 240;;\n"
            "  *) exit 97;;\n"
            "esac\n")
        started = time.monotonic()
        result = self.call("import", "cloud-pro", timeout=30 + GUARD)
        elapsed = time.monotonic() - started

        self.assertLess(elapsed, 30 + 6, f'open hung {elapsed:.1f}s past its 30s bound')
        self.assertGreaterEqual(elapsed, 30 - 1)
        self.assertEqual(result.returncode, 10)
        self.assertIn("could not open the Shortcut", result.stderr)
        # Parity with today's failed-open semantics: pending stays written.
        self.assertEqual((self.fixture.home / "imports/pending").read_text(), "cloud-pro\n")
        self.assertFalse((self.fixture.home / "setup.lock").exists())

    def test_open_success_reports_permission_pending(self):
        self.fixture.install()
        self.replace_shortcuts(
            "#!/bin/bash\n"
            "case \"${1:-}\" in\n"
            "  list) exit 0;;\n"
            "  sign) for arg; do case \"$prev\" in --output) printf signed > \"$arg\";; esac; prev=$arg; done; exit 0;;\n"
            "  open) exit 0;;\n"
            "  *) exit 97;;\n"
            "esac\n")
        result = self.call("import", "cloud-pro")
        self.assertEqual(result.returncode, 0)
        payload = json.loads(result.stdout)
        self.assertEqual(payload["status"], "permission_pending")
        self.assertIn("Add AFM Bridge - Cloud Pro in Shortcuts", payload["message"])
        self.assertEqual((self.fixture.home / "imports/pending").read_text(), "cloud-pro\n")


if __name__ == "__main__":
    unittest.main()
