"""Offline checks only: never launches Hollis or a model."""
import json
import os
import re
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import image_suite as suite


class ImageSuiteTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.plan = suite.make_plan(self.root / "fixtures")

    def tearDown(self):
        self.temp.cleanup()

    def result(self, case, response=None, **updates):
        result = {"exit": 0, "timed_out": False, "stderr": "", "stdout": json.dumps({
            "model_requested": case["model"], "model_used": case["model"],
            "response": response if response is not None else "|".join(case["expected"])})}
        result.update(updates)
        return result

    def test_plan_is_bounded_and_respects_model_limits(self):
        self.assertEqual(len(self.plan), 12)
        for model in suite.MODELS:
            self.assertEqual(sum(c["model"] == model for c in self.plan), 4)
        for case in self.plan:
            self.assertLessEqual(len(case["fixtures"]), 1 if case["model"] == "chatgpt" else 2)
            self.assertIn(case["expected"][0], case["prompt"])

    def test_fixture_answers_are_not_in_neutral_prompts_or_names(self):
        for case in self.plan:
            if case["name"].startswith("prompt-pixel-") or case["name"].startswith("document-ocr-"):
                for expected in case["expected"][1:]:
                    self.assertNotIn(expected.lower(), re.findall(r"[a-z0-9]+", case["prompt"].lower()))
                    self.assertNotIn(expected.lower(), Path(case["fixtures"][0]["path"]).name.lower())

    def test_random_markers_and_document_facts_change(self):
        other = suite.make_plan(self.root / "other")
        self.assertFalse({c["expected"][0] for c in self.plan} & {c["expected"][0] for c in other})
        self.assertNotEqual(self.plan[2]["expected"][1], other[2]["expected"][1])

    def test_fixtures_are_private_and_hashes_match(self):
        self.assertEqual((self.root / "fixtures").stat().st_mode & 0o777, 0o700)
        for case in self.plan:
            for fixture in case["fixtures"]:
                path = Path(fixture["path"])
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                self.assertEqual(suite.digest(path), fixture["sha256"])
        self.assertEqual(self.plan[-1]["fixtures"][0]["format"], "JPEG")
        self.assertEqual(self.plan[-1]["fixtures"][0]["width"], 3840)

    def test_matching_prompt_and_pixels(self):
        for case in self.plan:
            result = suite.classify(case, self.result(case))
            self.assertEqual(result["transport"], "PASS")
            self.assertEqual(result["semantic"], "MATCH")
            self.assertFalse(result["stop"])

    def test_negations_and_extra_text_cannot_pass(self):
        case = self.plan[0]
        for response in ("not " + "|".join(case["expected"]), "|".join(case["expected"]) + " maybe", "|".join(reversed(case["expected"]))):
            result = suite.classify(case, self.result(case, response))
            self.assertEqual(result["transport"], "PASS")
            self.assertEqual(result["semantic"], "MISMATCH")
            self.assertFalse(result["stop"])

    def test_failures_stop_live_calls(self):
        case = self.plan[0]
        failures = [self.result(case, exit=7), self.result(case, timed_out=True),
                    self.result(case, stdout="not JSON"), self.result(case, "service unavailable"),
                    self.result(case, stderr="rate limit reported"), self.result(case, "")]
        wrong_model = self.result(case)
        obj = json.loads(wrong_model["stdout"])
        obj["model_used"] = "on-device"
        wrong_model["stdout"] = json.dumps(obj)
        failures.append(wrong_model)
        for result in failures:
            self.assertTrue(suite.classify(case, result)["stop"])

    def test_run_serializes_spaces_and_keeps_private_state(self):
        calls, pauses = [], []
        cases = [self.plan[0], self.plan[3], self.plan[6]]
        report = {"results": [], "stopped_early": False}
        def fake(args, env):
            case = cases[len(calls)]
            calls.append((args, env))
            return self.result(case)
        suite.run_plan("/fresh/hollis", cases, self.root / "state", report,
                       self.root / "report.json", call=fake, sleep=pauses.append)
        self.assertEqual(pauses, [45, 45])
        self.assertEqual(suite.spacing("cloud", "chatgpt"), 15)
        self.assertEqual(len(calls), 3)
        self.assertTrue(all(env["HOLLIS_STATE_DIR"] == str(self.root / "state") for _, env in calls))
        self.assertTrue(all(args.count("respond") == 1 for args, _ in calls))
        self.assertEqual((self.root / "report.json").stat().st_mode & 0o777, 0o600)

    def test_first_transport_failure_prevents_all_later_calls(self):
        calls = []
        report = {"results": [], "stopped_early": False}
        def fake(args, env):
            calls.append(args)
            return self.result(self.plan[0], exit=6)
        suite.run_plan("/fresh/hollis", self.plan, self.root, report,
                       self.root / "report.json", call=fake, sleep=lambda _: self.fail("slept after failure"))
        self.assertEqual(len(calls), 1)
        self.assertTrue(report["stopped_early"])

    def test_preflight_rejects_failures_and_missing_bridges(self):
        for result in (self.result(self.plan[0], exit=1), self.result(self.plan[0], stdout="{}"),
                       self.result(self.plan[0], stdout='{"bridges": []}')):
            with self.assertRaises(RuntimeError):
                suite.check_preflight(result, set(suite.MODELS))
        good = self.result(self.plan[0], stdout=json.dumps({"bridges": [
            {"model": model, "installed": True} for model in suite.MODELS]}))
        suite.check_preflight(good, set(suite.MODELS))

    def test_internal_call_bound_cannot_be_bypassed(self):
        with self.assertRaises(ValueError):
            suite.run_plan("unused", self.plan + [self.plan[0]], self.root,
                           {"results": []}, self.root / "report.json",
                           call=lambda *a, **k: self.fail("called model"))

    def test_service_error_stops_all_later_calls(self):
        calls = []
        report = {"results": [], "stopped_early": False}
        def fake(args, env):
            calls.append(args)
            return self.result(self.plan[0], "Service unavailable. Try again later.")
        suite.run_plan("/fresh/hollis", self.plan, self.root, report,
                       self.root / "report.json", call=fake, sleep=lambda _: self.fail("slept after failure"))
        self.assertEqual(len(calls), 1)
        self.assertTrue(report["stopped_early"])

    def test_interrupted_main_records_stop_and_removes_state(self):
        binary = self.root / "hollis"
        binary.write_text("synthetic binary; never executed")
        original_mkdtemp = tempfile.mkdtemp
        def local_temp(suffix=None, prefix=None, dir=None):
            return original_mkdtemp(suffix=suffix, prefix=prefix, dir=self.root)
        def fake_invoke(args, **kwargs):
            output = "github.com/kamenxrider/hollis/cmd/hollis"
            if "doctor" in args:
                output = json.dumps({"bridges": [
                    {"model": model, "installed": True} for model in suite.MODELS]})
            return {"exit": 0, "timed_out": False, "stdout": output, "stderr": ""}
        with mock.patch.dict(os.environ, HOLLIS_BIN=str(binary)), \
                mock.patch("sys.argv", ["image_suite.py", "--live"]), \
                mock.patch.object(suite.tempfile, "mkdtemp", side_effect=local_temp), \
                mock.patch.object(suite, "invoke", side_effect=fake_invoke), \
                mock.patch.object(suite, "run_plan", side_effect=KeyboardInterrupt):
            self.assertEqual(suite.main(), 1)
        reports = list(self.root.glob("hollis-image-research-*/report.json"))
        self.assertEqual(len(reports), 1)
        report = json.loads(reports[0].read_text())
        self.assertTrue(report["stopped_early"])
        self.assertTrue(report["state_removed"])
        self.assertEqual(report["results"], [])
        self.assertEqual(report["preflight_or_harness_error"], "interrupted by user")


if __name__ == "__main__":
    unittest.main()
