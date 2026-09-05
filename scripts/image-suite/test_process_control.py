"""Real local subprocess cleanup tests; never invokes Hollis or a provider."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

import process_control


FAKE_PARENT = r'''
import json, os, pathlib, subprocess, sys, time
folder = pathlib.Path(os.environ["TMPDIR"])
(folder / "hollis-image-prompt-test.txt").write_text("synthetic private prompt")
child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"], process_group=0)
pathlib.Path(sys.argv[1]).write_text(json.dumps({"parent": os.getpid(), "child": child.pid, "child_session": os.getsid(child.pid), "child_group": os.getpgid(child.pid), "tmpdir": str(folder)}))
child.wait()
'''


class ProcessControlTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.ready = self.root / "ready.json"

    def tearDown(self):
        # This emergency cleanup owns only IDs written by our fake process.
        # It prevents a failed assertion from leaving test sleepers behind.
        if self.ready.exists():
            data = json.loads(self.ready.read_text())
            for name in ("child", "parent"):
                try:
                    if os.getsid(data[name]) == data["parent"]:
                        os.kill(data[name], signal.SIGKILL)
                except ProcessLookupError:
                    pass
        self.temp.cleanup()

    def wait_for_ready(self):
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            try:
                return json.loads(self.ready.read_text())
            except (FileNotFoundError, json.JSONDecodeError):
                time.sleep(.01)
        self.fail("fake parent did not create its child")

    def assert_stopped(self, pid):
        # A just-killed orphan can briefly be a zombie before init reaps it.
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            result = subprocess.run(["ps", "-p", str(pid), "-o", "stat="],
                                    capture_output=True, text=True, timeout=2)
            if not result.stdout.strip() or result.stdout.strip().startswith("Z"):
                return
            time.sleep(.01)
        self.fail(f"owned process {pid} remains running")

    def test_timeout_kills_child_in_distinct_group_and_removes_prompt(self):
        result = process_control.invoke([sys.executable, "-c", FAKE_PARENT, str(self.ready)], timeout=.5)
        data = self.wait_for_ready()
        self.assertTrue(result["timed_out"])
        self.assertNotEqual(result["exit"], 0)
        self.assertEqual(data["child_session"], data["parent"])
        self.assertNotEqual(data["child_group"], data["parent"])
        self.assert_stopped(data["parent"])
        self.assert_stopped(data["child"])
        self.assertFalse(Path(data["tmpdir"]).exists())

    def test_interrupt_kills_child_in_distinct_group_and_removes_prompt(self):
        self.assert_interrupt_cleanup(signal.SIGINT)

    def test_sigterm_kills_child_in_distinct_group_and_removes_prompt(self):
        self.assert_interrupt_cleanup(signal.SIGTERM)

    def assert_interrupt_cleanup(self, interruption):
        # Signal a separate driver, never the unit-test process. The driver
        # invokes the same cleanup path used by a human Ctrl-C in the suite.
        driver = """
import sys
import process_control
try:
    process_control.invoke([sys.executable, '-c', sys.argv[1], sys.argv[2]], timeout=10)
except (KeyboardInterrupt, process_control.InvocationInterrupted):
    print('interrupted after cleanup')
"""
        process = subprocess.Popen([sys.executable, "-B", "-c", driver, FAKE_PARENT, str(self.ready)],
                                   cwd=Path(__file__).parent, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True, start_new_session=True)
        try:
            data = self.wait_for_ready()
            self.assertEqual(os.getsid(data["child"]), data["parent"])
            self.assertNotEqual(os.getpgid(data["child"]), os.getpgid(data["parent"]))
            process.send_signal(interruption)
            stdout, stderr = process.communicate(timeout=8)
            self.assertEqual(process.returncode, 0, stderr)
            self.assertIn("interrupted after cleanup", stdout)
            self.assert_stopped(data["parent"])
            self.assert_stopped(data["child"])
            self.assertFalse(Path(data["tmpdir"]).exists())
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate(timeout=3)

    def test_success_preserves_output_and_removes_private_tmpdir(self):
        original_handler = signal.getsignal(signal.SIGTERM)
        result = process_control.invoke([sys.executable, "-c", "import os; print(os.environ['TMPDIR'])"])
        self.assertEqual(result["exit"], 0)
        self.assertFalse(result["timed_out"])
        self.assertFalse(Path(result["stdout"].strip()).exists())
        self.assertEqual(signal.getsignal(signal.SIGTERM), original_handler)

    def test_failed_inventory_is_an_explicit_error(self):
        with mock.patch.object(process_control.subprocess, "run", side_effect=OSError("unavailable")):
            with self.assertRaisesRegex(RuntimeError, "inventory failed"):
                process_control._session_groups(os.getpid())

    def test_failed_inventory_prevents_spawn(self):
        with mock.patch.object(process_control, "_session_groups", side_effect=RuntimeError("inventory failed")):
            with mock.patch.object(process_control.subprocess, "Popen") as spawn:
                with self.assertRaisesRegex(RuntimeError, "inventory failed"):
                    process_control.invoke(["never-start"])
                spawn.assert_not_called()


if __name__ == "__main__":
    unittest.main()
