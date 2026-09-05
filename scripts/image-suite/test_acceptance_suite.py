"""Offline checks for acceptance_suite.py; no model or Shortcut calls."""
from __future__ import annotations

import json
import hashlib
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

import acceptance_suite as suite


PNG_FIXTURE = bytes.fromhex(
    "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489"
    "0000000d49444154789c63f8cfc0f01f00050001ff89993d1d"
    "0000000049454e44ae426082"
)


class AcceptanceSuiteTests(unittest.TestCase):
    def test_plan_is_explicit_and_bounded(self):
        value = suite.plan()
        names = [item["name"] for item in value["cases"]]
        self.assertEqual(names[:5], ["interactive-first", "interactive-sequence", "cli-followup", "api-chat-completions", "api-responses"])
        self.assertEqual(sum(item["generations"] for item in value["cases"][:5]), 12)
        self.assertIn("api-reference-generations", names)
        self.assertEqual(sum(item["generations"] for item in value["cases"]), suite.MAX_GENERATIONS)
        self.assertEqual({item["name"]: item["generations"] for item in value["cases"]}, suite.CASE_GENERATIONS)
        self.assertEqual(value["settle_floor_seconds"], 3.0)
        self.assertTrue(value["deferred"])

    def test_queue_rejects_subthree_spacing(self):
        with self.assertRaisesRegex(ValueError, "at least 3"):
            suite.GenerationQueue(2.99, {})

    def test_queue_waits_from_completion_not_start(self):
        report = {}
        queue = suite.GenerationQueue(3, report)
        clock = iter([100.0, 100.5, 103.5])
        with mock.patch.object(suite.time, "monotonic", side_effect=lambda: next(clock)), mock.patch.object(suite.time, "sleep") as sleep:
            queue.completed()
            queue.before_generation("second")
        sleep.assert_called_once()
        self.assertGreaterEqual(sleep.call_args.args[0], 2.4)
        self.assertGreaterEqual(report["generation_gaps"][0]["seconds"], 3.0)

    def test_png_metadata_validates_png_without_pillow(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "one.png"
            # 1x1 opaque PNG, kept inline so this test has no fixture file.
            path.write_bytes(PNG_FIXTURE)
            result = suite.png_metadata(path)
            self.assertEqual((result["width"], result["height"]), (1, 1))

    def test_extract_json_uses_last_object(self):
        self.assertEqual(suite.extract_json("echo\n{\"path\":\"x\"}\n"), {"path": "x"})

    def test_extract_api_image_writes_private_png(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "image.png"
            png = PNG_FIXTURE
            response = {"choices": [{"message": {"role": "assistant", "content": [{"type": "image_url", "image_url": {"url": "data:image/png;base64," + suite.base64.b64encode(png).decode()}}]}}]}
            result = suite.extract_api_image("chat-completions", response, output)
            self.assertEqual(result["png"]["bytes"], len(png))
            self.assertEqual(output.stat().st_mode & 0o777, 0o600)

    def test_standalone_reference_metadata_and_png_checksum(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "image.png"
            png = PNG_FIXTURE
            checksum = hashlib.sha256(png).hexdigest()
            response = {"data": [{"b64_json": suite.base64.b64encode(png).decode(), "sha256": checksum, "reference_image_sent": True, "reference_image_sha256": checksum}]}
            result = suite.extract_standalone_image(response, output)
            self.assertEqual(result["png"]["sha256"], checksum)
            self.assertEqual(suite.validate_standalone_reference_delivery(response, checksum)["reference_image_sent"], True)

    def test_conversation_followup_uses_actual_png_as_final_user_reference(self):
        png_one = PNG_FIXTURE
        png_two = png_one
        first_checksum = hashlib.sha256(png_one).hexdigest()
        image_data = suite.base64.b64encode(png_one).decode()
        second_data = suite.base64.b64encode(png_two).decode()
        for kind in ("chat-completions", "responses"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as folder:
                if kind == "chat-completions":
                    first_response = {"choices": [{"message": {"role": "assistant", "content": [
                        {"type": "text", "text": "first output"},
                        {"type": "image_url", "image_url": {"url": "data:image/png;base64," + image_data}, "sha256": first_checksum},
                    ]}}]}
                    second_response = {"choices": [{"message": {"role": "assistant", "content": [
                        {"type": "text", "text": "A supplied reference image was sent"},
                        {"type": "image_url", "image_url": {"url": "data:image/png;base64," + second_data}, "sha256": first_checksum, "reference_image_sent": True, "reference_image_sha256": first_checksum},
                    ]}}]}
                else:
                    first_response = {"output": [{"role": "assistant", "content": [
                        {"type": "output_text", "text": "first output"},
                        {"type": "output_image", "b64_json": image_data, "sha256": first_checksum},
                    ]}]}
                    second_response = {"output": [{"role": "assistant", "content": [
                        {"type": "output_text", "text": "A supplied reference image was sent"},
                        {"type": "output_image", "b64_json": second_data, "sha256": first_checksum, "reference_image_sent": True, "reference_image_sha256": first_checksum},
                    ]}]}
                requests = []

                def fake_request(base_url, path, payload, token):
                    requests.append((path, payload))
                    return (200, first_response if len(requests) == 1 else second_response)

                with mock.patch.object(suite, "api_request", side_effect=fake_request), mock.patch.object(suite.time, "sleep"):
                    report = {}
                    suite.run_api_followup("http://127.0.0.1:1978", kind, Path(folder), "animation", "", suite.GenerationQueue(3, report), report)
                self.assertEqual(len(requests), 3)
                second_payload = requests[1][1]
                if kind == "chat-completions":
                    final_user = second_payload["messages"][-1]
                    self.assertEqual([part["type"] for part in final_user["content"]], ["text", "image_url"])
                    self.assertEqual(final_user["content"][1]["image_url"]["url"], "data:image/png;base64," + image_data)
                else:
                    final_user = second_payload["input"][-1]
                    self.assertEqual([part["type"] for part in final_user["content"]], ["input_text", "input_image"])
                    self.assertEqual(final_user["content"][1]["image_url"], "data:image/png;base64," + image_data)

                third_payload = requests[2][1]
                history = third_payload["messages" if kind == "chat-completions" else "input"]
                self.assertEqual(history[-1]["role"], "user")
                self.assertEqual(history[-1]["content"], "Keep the same fox and stone, but set the scene in a snowy forest under moonlight.")
                self.assertEqual(history[-2]["role"], "assistant")
                self.assertTrue(any(part["type"] == ("image_url" if kind == "chat-completions" else "output_image") for part in history[-2]["content"]))

    def test_standalone_followup_uses_actual_png_reference(self):
        image_data = suite.base64.b64encode(PNG_FIXTURE).decode()
        checksum = hashlib.sha256(PNG_FIXTURE).hexdigest()
        first_response = {"data": [{"b64_json": image_data, "sha256": checksum}]}
        second_response = {"data": [{
            "b64_json": image_data,
            "sha256": checksum,
            "reference_image_sent": True,
            "reference_image_sha256": checksum,
        }]}
        requests = []

        def fake_request(base_url, path, payload, token):
            requests.append((path, payload))
            return (200, first_response if len(requests) == 1 else second_response)

        with tempfile.TemporaryDirectory() as folder:
            with mock.patch.object(suite, "api_request", side_effect=fake_request), mock.patch.object(suite.time, "sleep"):
                report = {}
                suite.run_api_reference_generations("http://127.0.0.1:1978", Path(folder), "animation", "", suite.GenerationQueue(3, report), report)
            self.assertEqual(len(requests), 2)
            self.assertEqual(requests[0][0], "/v1/images/generations")
            self.assertEqual(requests[1][1]["reference_image"], "data:image/png;base64," + image_data)
            self.assertEqual(report["api_reference_generations"]["reference_input"]["reference_image_sha256"], checksum)

    def test_dynamic_reference_is_deferred_in_plan(self):
        value = suite.plan()
        deferred = " ".join(value["deferred"])
        self.assertIn("--reference-image", deferred)

    def test_pty_runner_uses_a_real_terminal_without_a_model(self):
        result = suite.pty_invoke(
            [sys.executable, "-c", "import sys; print(sys.stdin.readline().strip(), flush=True)"],
            dict(os.environ),
            b"synthetic input\n\x04",
            timeout=5,
        )
        self.assertEqual(result["exit"], 0)
        self.assertIn("synthetic input", result["output"])


if __name__ == "__main__":
    unittest.main()
