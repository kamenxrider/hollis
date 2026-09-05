"""Offline contract tests for image_generation_live.py.

These tests never launch Hollis, Shortcuts, a local API server, or a model.
"""
from __future__ import annotations

import base64
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
import unittest
from unittest import mock
import zlib


MODULE_PATH = Path(__file__).with_name("image_generation_live.py")
SPEC = importlib.util.spec_from_file_location("image_generation_live", MODULE_PATH)
assert SPEC and SPEC.loader
suite = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = suite
SPEC.loader.exec_module(suite)


def png_bytes(width: int = 4, height: int = 2) -> bytes:
    def chunk(kind: bytes, payload: bytes) -> bytes:
        crc = zlib.crc32(kind + payload) & 0xffffffff
        return len(payload).to_bytes(4, "big") + kind + payload + crc.to_bytes(4, "big")

    # RGBA rows: filter byte followed by opaque red pixels.
    rows = b"".join(b"\x00" + bytes([255, 0, 0, 255]) * width for _ in range(height))
    header = (width.to_bytes(4, "big") + height.to_bytes(4, "big") + bytes([8, 6, 0, 0, 0]))
    return suite.PNG_SIGNATURE + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b"")


class ImageGenerationLiveTests(unittest.TestCase):
    def test_api_user_agent_matches_documented_fetch_contract(self):
        self.assertEqual(suite.UA, "OpenAI File Downloader, XaiImageApiFetch/1.0")

    def test_plan_is_exactly_bounded_and_covers_each_style_twice(self):
        with tempfile.TemporaryDirectory() as directory:
            plan = suite.build_plan(Path(directory))
        self.assertEqual(len(plan), suite.MAX_CALLS)
        style_cases = [case for case in plan if case["kind"] == "cli_style"]
        self.assertEqual(len(style_cases), 12)
        for style in suite.STYLES:
            self.assertEqual(sum(case["style"] == style for case in style_cases), 2)
        self.assertEqual(sum(case["kind"] == "api_generations" for case in plan), 2)
        self.assertEqual(sum(case["kind"] == "cli_chat" for case in plan), 4)
        self.assertEqual(sum(case["kind"] == "chat_completions" for case in plan), 4)
        self.assertEqual(sum(case["kind"] == "responses" for case in plan), 4)
        self.assertEqual(sum(case["kind"] == "cli_interactive" for case in plan), 2)

    def test_api_matrix_uses_stable_explicit_animation_style(self):
        with tempfile.TemporaryDirectory() as directory:
            plan = suite.build_plan(Path(directory))
        api_routes = [case for case in plan if case["kind"] == "api_generations"]
        self.assertEqual({case["style"] for case in api_routes}, {"animation"})
        for kind in ("chat_completions", "responses"):
            conversation_one = [case for case in plan
                                if case["kind"] == kind and case["conversation"] == 1]
            self.assertEqual({case["style"] for case in conversation_one}, {"animation"})

    def test_plan_distributes_each_local_transform_mode_at_least_twice(self):
        with tempfile.TemporaryDirectory() as directory:
            plan = suite.build_plan(Path(directory))
        transforms = [case["options"] for case in plan if case["options"]]
        self.assertGreaterEqual(sum(bool(o.get("aspect_ratio")) and o.get("fit") == "crop" for o in transforms), 2)
        self.assertGreaterEqual(sum(bool(o.get("aspect_ratio")) and o.get("fit") == "pad" for o in transforms), 2)
        self.assertGreaterEqual(sum(bool(o.get("size")) and o.get("fit") == "crop" for o in transforms), 2)
        self.assertGreaterEqual(sum(bool(o.get("size")) and o.get("fit") == "pad" for o in transforms), 2)

    def test_bridge_map_requires_exact_six_safe_names(self):
        bridges = {style: f"Hollis Image {style}" for style in suite.STYLES}
        self.assertEqual(suite.load_bridges(json.dumps(bridges), require_all=True), bridges)
        for invalid in (
            {style: "bridge" for style in suite.STYLES if style != "sketch"},
            {**bridges, "extra": "bridge"},
            {**bridges, "animation": "-bad"},
            {**bridges, "animation": "a\x00b"},
        ):
            with self.assertRaises(suite.SuiteFailure):
                suite.load_bridges(json.dumps(invalid), require_all=True)

    def test_unified_config_isolated_as_single_bridge(self):
        bridges = {style: "Hollis Image - Unified Probe" for style in suite.STYLES}
        with tempfile.TemporaryDirectory() as directory:
            config = suite.write_image_config(Path(directory) / "state", bridges, unified_bridge=bridges["any"])
            self.assertEqual(json.loads(config.read_text()), {"image_bridge": bridges["any"]})
            self.assertEqual(stat.S_IMODE(config.stat().st_mode), 0o600)

    def test_api_followup_replays_original_user_and_assistant_before_new_prompt(self):
        replay = {"role": "assistant", "content": [{"type": "text", "text": "generated"}]}
        history = suite.build_api_conversation_input(2, "Keep the same shape; change its color to blue.",
                                                      "Make a green triangle on white.", replay)
        self.assertEqual([item["role"] for item in history], ["user", "assistant", "user"])
        self.assertEqual(history[0]["content"], "Make a green triangle on white.")
        self.assertIs(history[1], replay)
        with self.assertRaises(suite.SuiteFailure):
            suite.build_api_conversation_input(2, "follow-up", None, replay)

    def test_png_validator_checks_crc_mode_and_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "image.png"
            path.write_bytes(png_bytes())
            path.chmod(0o600)
            metadata = suite.validate_png(path)
            self.assertEqual((metadata["width"], metadata["height"]), (4, 2))
            self.assertEqual(metadata["bytes"], path.stat().st_size)
            path.write_bytes(path.read_bytes()[:-1] + b"x")
            with self.assertRaises(suite.SuiteFailure):
                suite.validate_png(path)

    def test_base64_decoder_writes_private_png_and_rejects_noncanonical(self):
        with tempfile.TemporaryDirectory() as directory:
            encoded = base64.b64encode(png_bytes()).decode("ascii")
            path = Path(directory) / "decoded.png"
            metadata = suite.decode_base64_png(encoded, path)
            self.assertEqual(metadata["bytes"], len(png_bytes()))
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            with self.assertRaises(suite.SuiteFailure):
                suite.decode_base64_png(encoded.rstrip("=") + "=", Path(directory) / "bad.png")

    def test_expected_dimensions_mirror_crop_pad_and_exact_size(self):
        self.assertEqual(suite.expected_dimensions(1024, 1024, {"aspect_ratio": "16:9", "fit": "crop"}), (1024, 576))
        self.assertEqual(suite.expected_dimensions(1024, 1024, {"aspect_ratio": "16:9", "fit": "pad"}), (1821, 1024))
        self.assertEqual(suite.expected_dimensions(1024, 1024, {"size": "640x480", "fit": "crop"}), (640, 480))

    def test_output_processing_metadata_must_match_requested_options_exactly(self):
        suite.assert_output_processing({"size": "640x480", "fit": "pad"}, {"size": "640x480", "fit": "pad"})
        with self.assertRaises(suite.SuiteFailure):
            suite.assert_output_processing({"size": "640x480", "fit": "crop"}, {"size": "640x480", "fit": "pad"})

    def test_api_error_response_is_persisted_without_image_payload(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            plan = suite.build_plan(root)
            case = next(item for item in plan if item["name"] == "api-generations-aspect")
            report_path = root / "report.json"
            harness = suite.LiveHarness(sys.executable, root, {}, 30, {"pacing": {}}, report_path, plan)
            response = {"error": {
                "type": "server_error", "code": "image_generation_failed",
                "message": "bridge rejected request", "b64_json": "do-not-persist",
                "image_url": "data:image/png;base64,do-not-persist",
                "details": {"b64_json": "do-not-persist"},
            }}
            with mock.patch.object(harness, "post", return_value=(502, response)):
                with self.assertRaises(suite.SuiteFailure):
                    harness.run_api_generations(case)
            self.assertEqual(case["error_response"], {
                "http_status": 502,
                "error": {
                    "type": "server_error", "code": "image_generation_failed",
                    "message": "bridge rejected request",
                },
            })
            self.assertNotIn("b64_json", json.dumps(case["error_response"]))
            self.assertNotIn("image_url", json.dumps(case["error_response"]))

    def test_cooldown_is_zero_before_first_call_and_persists_remaining_wait(self):
        self.assertEqual(suite.cooldown_seconds(None, 30, now=100), 0)
        self.assertEqual(suite.cooldown_seconds(100, 30, now=110), 20)
        self.assertEqual(suite.cooldown_seconds(100, 30, now=130), 0)

    def test_plan_mode_does_not_create_report_or_invoke_binary(self):
        output = io.StringIO()
        with mock.patch.object(suite, "invoke") as invoke_mock, contextlib.redirect_stdout(output):
            status = suite.main(["--binary", sys.executable, "--plan", "--case", "cli-style-any-1"])
        self.assertEqual(status, 0)
        invoke_mock.assert_not_called()
        data = json.loads(output.getvalue())
        self.assertEqual(data["selected_cases"], ["cli-style-any-1"])
        self.assertEqual(len(data["plan"]), suite.MAX_CALLS)

    def test_plan_start_filter_keeps_full_matrix_and_selects_suffix(self):
        output = io.StringIO()
        with mock.patch.object(suite, "invoke") as invoke_mock, contextlib.redirect_stdout(output):
            status = suite.main(["--binary", sys.executable, "--plan", "--start", "12"])
        self.assertEqual(status, 0)
        invoke_mock.assert_not_called()
        data = json.loads(output.getvalue())
        self.assertEqual(len(data["plan"]), suite.MAX_CALLS)
        self.assertEqual(data["selected_cases"][0], "api-generations-aspect")

    def test_interval_rejects_nonfinite_values(self):
        for interval in ("nan", "inf", "-inf"):
            with self.subTest(interval=interval), self.assertRaises(SystemExit) as raised:
                suite.main(["--binary", sys.executable, "--plan", "--interval", interval])
            self.assertEqual(raised.exception.code, 2)

    def test_chat_followups_depend_on_prior_text_context(self):
        with tempfile.TemporaryDirectory() as directory:
            plan = suite.build_plan(Path(directory))
        followups = [case for case in plan if case["kind"] == "cli_chat" and case["turn"] == 2]
        self.assertEqual(len(followups), 2)
        self.assertTrue(all(case["prompt"] == "Keep the same shape; change its color to blue."
                             for case in followups))

    def test_turn_two_target_requires_turn_one_before_any_dispatch(self):
        with tempfile.TemporaryDirectory() as directory:
            plan = suite.build_plan(Path(directory))
        turn_two = next(index for index, case in enumerate(plan) if case["name"] == "api-chat_completions-conversation-1-turn-2")
        with self.assertRaises(suite.SuiteFailure):
            suite.validate_selected_dependencies(plan, [turn_two])
        turn_one = turn_two - 1
        suite.validate_selected_dependencies(plan, [turn_one, turn_two])

    def test_dry_run_writes_plan_report_without_provider_calls(self):
        bridges = {style: f"Hollis Image {style}" for style in suite.STYLES}
        with tempfile.TemporaryDirectory() as directory:
            report_dir = Path(directory) / "report"
            with mock.patch.object(suite, "invoke") as invoke_mock:
                status = suite.main(["--binary", sys.executable, "--output", str(report_dir),
                                     "--bridges-json", json.dumps(bridges)])
            self.assertEqual(status, 0)
            invoke_mock.assert_not_called()
            report = json.loads((report_dir / "report.json").read_text())
            self.assertEqual(report["mode"], "dry-run")
            self.assertEqual(report["max_calls"], suite.MAX_CALLS)
            self.assertEqual(stat.S_IMODE((report_dir / "report.json").stat().st_mode), 0o600)

    def test_chat_extractors_require_text_marker_and_replay_content(self):
        with tempfile.TemporaryDirectory() as directory:
            image_path = Path(directory) / "chat.png"
            encoded = base64.b64encode(png_bytes()).decode("ascii")
            content = [
                {"type": "text", "text": "[Hollis generated image]"},
                {"type": "image_url", "image_url": {"url": "data:image/png;base64," + encoded},
                 "native_width": 4, "native_height": 2, "width": 4, "height": 2,
                 "sha256": hashlib.sha256(png_bytes()).hexdigest()},
            ]
            artifact, replay, marker = suite._extract_chat_image(
                {"choices": [{"message": {"content": content}}]}, image_path, {}, responses=False)
            self.assertEqual(marker, "[Hollis generated image]")
            self.assertEqual(replay["role"], "assistant")
            self.assertEqual(artifact["width"], 4)

    def test_interactive_pty_helper_completes_on_command_and_eof(self):
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / "pty-input.txt"
            helper = "import pathlib,sys; pathlib.Path(sys.argv[1]).write_text(sys.stdin.read())"
            result = suite.invoke_interactive(
                [sys.executable, "-c", helper, str(marker)], dict(os.environ), "/image synthetic probe\n", timeout=5
            )
            self.assertEqual(result["exit"], 0, result)
            self.assertFalse(result["timed_out"])
            self.assertEqual(marker.read_text(), "/image synthetic probe\n")


if __name__ == "__main__":
    unittest.main()
