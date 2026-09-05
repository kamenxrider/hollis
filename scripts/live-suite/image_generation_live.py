#!/usr/bin/env python3
"""Bounded, opt-in live image regression for the Hollis image transport.

The live plan makes at most 28 sequential model calls.  It exercises each of
the six configured Image Playground styles twice, then checks the standalone
HTTP route, text-backed CLI chat continuations, both HTTP conversation routes,
and the interactive ``/image`` command.  Every generated file is retained in
the private report directory for visual review.

No model call is made without ``--live``.  A failed call stops the plan; the
harness never retries or falls back to another bridge.  The default 30 second
cooldown is deliberately conservative for a post-release probe.
"""
from __future__ import annotations

import argparse
import base64
import binascii
import hashlib
import io
import json
import math
import os
from pathlib import Path
import pty
import re
import selectors
import stat
import subprocess
import sys
import time
import urllib.error
import urllib.request
import warnings
import zlib

from PIL import Image

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "image-suite"))
from process_control import _terminate_session, invoke  # noqa: E402


ROOT = Path(__file__).resolve().parents[2]
STYLES = ("any", "animation", "genmoji", "illustration", "sketch", "chatgpt")
MAX_CALLS = 28
DEFAULT_INTERVAL = 30.0
MAX_TIMEOUT = 140.0
SERVICE_ERROR_MARKERS = (
    "rate limit", "rate-limit", "rate_limited", "service unavailable",
    "model unavailable", "try again later", "temporarily unavailable",
)
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
UA = "OpenAI File Downloader, XaiImageApiFetch/1.0"


class SuiteFailure(RuntimeError):
    """A bounded run failure that should be recorded without a traceback."""


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def utc_now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def private_json(path: Path, value: object) -> None:
    """Write a 0600 JSON report atomically."""
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, indent=2, sort_keys=True)
            stream.write("\n")
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
        os.chmod(path, 0o600)
    finally:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass


def _safe_json(value: object, limit: int = 4000) -> object:
    """Keep reports useful without persisting a base64 image response."""
    if isinstance(value, str):
        if len(value) <= limit:
            return value
        return value[:limit] + "...[truncated]"
    if isinstance(value, dict):
        return {str(k): _safe_json(v, limit) for k, v in value.items()
                if k not in {"b64_json", "image_url", "output_image"}}
    if isinstance(value, list):
        return [_safe_json(v, limit) for v in value]
    return value


def safe_error_envelope(status: int, response: object) -> dict[str, object]:
    """Keep only bounded, structured API error fields in the private report.

    Error responses are useful when a provider or bridge refuses a request,
    but the surrounding response must never be copied into the report: it may
    contain an inline image or another large payload.  The common OpenAI error
    fields are sufficient to diagnose a rejected case and are all scalar.
    """
    raw_error = response.get("error") if isinstance(response, dict) else None
    if not isinstance(raw_error, dict):
        return {
            "http_status": int(status),
            "error": {"type": "invalid_error_response", "message": "response omitted a structured error object"},
        }
    safe_error: dict[str, object] = {}
    for key in ("type", "code", "message", "param", "status"):
        if key in raw_error:
            value = raw_error[key]
            if isinstance(value, (str, int, float, bool)) or value is None:
                safe_error[key] = _safe_json(value, limit=2000)
    if not safe_error:
        safe_error["type"] = "unstructured_error"
    return {"http_status": int(status), "error": safe_error}


def load_bridges(raw: str | None, *, require_all: bool) -> dict[str, str]:
    """Load a style-to-Shortcut map from JSON text or a JSON file."""
    if not raw:
        if require_all:
            raise SuiteFailure("--bridges-json is required for a live run")
        return {style: f"<configured:{style}>" for style in STYLES}
    source = Path(raw)
    try:
        content = source.read_text(encoding="utf-8") if source.is_file() else raw
        decoded = json.loads(content)
    except (OSError, json.JSONDecodeError) as exc:
        raise SuiteFailure("--bridges-json must be a JSON object or readable JSON file") from exc
    if not isinstance(decoded, dict):
        raise SuiteFailure("--bridges-json must contain a JSON object")
    if set(decoded) != set(STYLES):
        missing = sorted(set(STYLES) - set(decoded))
        extra = sorted(set(decoded) - set(STYLES))
        detail = []
        if missing:
            detail.append("missing " + ", ".join(missing))
        if extra:
            detail.append("unknown " + ", ".join(extra))
        raise SuiteFailure("--bridges-json must name exactly all six styles (" + "; ".join(detail) + ")")
    result: dict[str, str] = {}
    for style in STYLES:
        value = decoded[style]
        if not isinstance(value, str) or not value.strip() or "\x00" in value or value.lstrip().startswith("-"):
            raise SuiteFailure(f"invalid Shortcut name for style {style}")
        result[style] = value.strip()
    return result


def write_image_config(state: Path, bridges: dict[str, str], *, unified_bridge: str | None = None) -> Path:
    """Create the isolated config consumed through HOLLIS_STATE_DIR."""
    state.mkdir(mode=0o700, parents=True, exist_ok=False)
    config = state / "config.json"
    private_json(config, {"image_bridge": unified_bridge} if unified_bridge else {"image_bridges": bridges})
    return config


def _png_dimensions(data: bytes) -> tuple[int, int]:
    """Validate PNG chunk framing/CRCs and return its IHDR dimensions."""
    if len(data) < len(PNG_SIGNATURE) + 12 or data[:8] != PNG_SIGNATURE:
        raise SuiteFailure("output is not a PNG")
    offset = 8
    width = height = 0
    seen_ihdr = seen_idat = seen_iend = False
    while offset < len(data):
        if offset + 12 > len(data):
            raise SuiteFailure("PNG has a truncated chunk")
        length = int.from_bytes(data[offset:offset + 4], "big")
        kind = data[offset + 4:offset + 8]
        end = offset + 12 + length
        if end > len(data):
            raise SuiteFailure("PNG chunk exceeds file length")
        payload = data[offset + 8:offset + 8 + length]
        actual_crc = int.from_bytes(data[offset + 8 + length:end], "big")
        expected_crc = zlib.crc32(kind + payload) & 0xffffffff
        if actual_crc != expected_crc:
            raise SuiteFailure("PNG chunk CRC mismatch")
        if kind == b"IHDR":
            if seen_ihdr or length != 13:
                raise SuiteFailure("PNG has an invalid IHDR")
            width = int.from_bytes(payload[:4], "big")
            height = int.from_bytes(payload[4:8], "big")
            if width < 1 or height < 1 or width * height > 16_000_000:
                raise SuiteFailure("PNG dimensions exceed Hollis bounds")
            seen_ihdr = True
        elif kind == b"IDAT":
            seen_idat = True
        elif kind == b"IEND":
            if length != 0 or seen_iend:
                raise SuiteFailure("PNG has an invalid IEND")
            seen_iend = True
            if end != len(data):
                raise SuiteFailure("PNG has trailing bytes")
            break
        offset = end
    if not (seen_ihdr and seen_idat and seen_iend):
        raise SuiteFailure("PNG is incomplete")
    return width, height


def repository_revision() -> str | None:
    try:
        result = subprocess.run(["git", "-C", str(ROOT), "rev-parse", "HEAD"],
                                capture_output=True, text=True, timeout=5, check=False)
    except (OSError, subprocess.SubprocessError):
        return None
    revision = result.stdout.strip()
    return revision if result.returncode == 0 and revision else None


def validate_png(path: Path, *, expected: dict[str, object] | None = None) -> dict[str, object]:
    """Validate a generated PNG and its 0600 private output mode."""
    if not path.is_file() or path.is_symlink():
        raise SuiteFailure(f"missing or symlinked output: {path}")
    mode = stat.S_IMODE(path.stat().st_mode)
    if mode != 0o600:
        raise SuiteFailure(f"output mode is {mode:o}, expected 600")
    data = path.read_bytes()
    if not data or len(data) > 16 * 1024 * 1024:
        raise SuiteFailure("PNG is empty or exceeds the output bound")
    width, height = _png_dimensions(data)
    # Chunk framing and CRCs above catch transport corruption. Pillow's
    # verify/load pass additionally decodes every scanline, while the explicit
    # dimensions/encoded-byte bounds keep decompression within Hollis's image
    # contract. Do not use a single verify call: verify() does not retain a
    # decoded image, so reopen it for load().
    try:
        with warnings.catch_warnings():
            warnings.simplefilter("error", Image.DecompressionBombWarning)
            with Image.open(io.BytesIO(data)) as image:
                if image.format != "PNG" or image.size != (width, height):
                    raise SuiteFailure("Pillow PNG metadata disagrees with PNG chunks")
                image.verify()
            with Image.open(io.BytesIO(data)) as image:
                image.load()
    except SuiteFailure:
        raise
    except (Image.DecompressionBombError, Image.DecompressionBombWarning, OSError, ValueError) as exc:
        raise SuiteFailure("PNG scanline decoding failed") from exc
    metadata: dict[str, object] = {
        "path": str(path), "bytes": len(data), "width": width,
        "height": height, "sha256": hashlib.sha256(data).hexdigest(),
    }
    if expected:
        for key in ("bytes", "width", "height", "sha256"):
            if key in expected and expected[key] != metadata[key]:
                raise SuiteFailure(f"PNG {key} metadata mismatch")
    return metadata


def expected_dimensions(native_width: int, native_height: int, options: dict[str, str]) -> tuple[int, int]:
    """Mirror the bounded crop/pad dimension calculation for assertions."""
    if "size" in options:
        left, right = options["size"].split("x", 1)
        return int(left), int(right)
    ratio = options.get("aspect_ratio")
    if not ratio:
        return native_width, native_height
    left, right = (int(value) for value in ratio.split(":", 1))
    wider = native_width * right > native_height * left
    crop = options.get("fit") == "crop"
    if wider:
        if crop:
            return max(1, native_height * left // right), native_height
        return native_width, max(1, (native_width * right + left - 1) // left)
    if crop:
        return native_width, max(1, native_width * right // left)
    return max(1, (native_height * left + right - 1) // right), native_height


def assert_output_processing(actual: object, expected: dict[str, str]) -> None:
    """Require the transport metadata to describe exactly the requested transform."""
    if not isinstance(actual, dict):
        raise SuiteFailure("image response omitted output_processing metadata")
    normalized_actual = {str(key): str(value) for key, value in actual.items()
                         if value is not None and str(value) != ""}
    normalized_expected = {str(key): str(value) for key, value in expected.items()
                           if value is not None and str(value) != ""}
    if normalized_actual != normalized_expected:
        raise SuiteFailure("output_processing metadata does not match the requested transform")


def decode_base64_png(value: str, path: Path) -> dict[str, object]:
    if not isinstance(value, str) or not value:
        raise SuiteFailure("API response did not contain b64_json")
    try:
        data = base64.b64decode(value, validate=True)
    except (ValueError, binascii.Error) as exc:
        raise SuiteFailure("API response contained invalid base64") from exc
    if base64.b64encode(data).decode("ascii") != value:
        raise SuiteFailure("API response base64 was not canonical")
    path.write_bytes(data)
    os.chmod(path, 0o600)
    return validate_png(path)


def _output_options_args(options: dict[str, str]) -> list[str]:
    args: list[str] = []
    for flag, key in (("--aspect-ratio", "aspect_ratio"), ("--size", "size"), ("--fit", "fit")):
        if key in options:
            args += [flag, options[key]]
    return args


SCENE_PROMPTS = {
    "animation": "A stylized 3D animation of an ancient mechanical sea turtle built from polished brass and gears. A glowing miniature glass greenhouse filled with bioluminescent plants is secured to its shell. The turtle glides peacefully through a deep turquoise ocean trench illuminated by soft sunbeams filtering through the water surface.",
    "illustration": "A flat vector gouache illustration of an artisanal pour-over coffee station from an isometric perspective. Hot water flows smoothly from a copper gooseneck kettle into a white ceramic dripper resting on a glass carafe. The scene sits on a clean, light oak kitchen counter bathed in warm, soft morning window light.",
    "sketch": "An architectural graphite sketch and technical line study of a six-axis robotic arm with carbon-fiber segments and exposed hydraulic lines. The arm terminates in a precision laser tool, poised mid-calibration. Clean drafting linework with fine cross-hatched shading over an off-white background.",
    "genmoji": "A cute, expressive golden-brown croissant character wearing classic black sunglasses with a cheeky grin. Isolated sticker design, thick white die-cut border, solid bright fill, high-contrast emoji icon.",
    "any": "A curious red fox wearing a moss-green scarf sits beside a steaming ceramic teacup in a tiny woodland bookshop. Rounded wooden shelves frame the fox, and warm afternoon sunlight falls across an open book on the table.",
    "chatgpt": "A watercolor illustration of a small timber cabin beside a still alpine lake at sunrise. Snow-capped mountains reflect in the water, a red canoe rests on the pebbled shore, and soft peach light glows through the morning mist.",
}


def build_plan(root: Path, prompt_set: str = "geometry") -> list[dict[str, object]]:
    """Build the complete 28-call plan without touching Shortcuts."""
    root = root.resolve()
    cases: list[dict[str, object]] = []
    subjects = (
        ("red", "circle"), ("blue", "square"), ("green", "triangle"),
        ("orange", "circle"), ("purple", "square"), ("yellow", "triangle"),
    )
    transforms = {
        "any": ({"aspect_ratio": "1:1", "fit": "crop"}, {"size": "512x512", "fit": "crop"}),
        "animation": ({"aspect_ratio": "16:9", "fit": "pad"}, {"size": "512x512", "fit": "pad"}),
        "genmoji": ({"aspect_ratio": "4:3", "fit": "crop"}, {"size": "768x512", "fit": "crop"}),
        "illustration": ({"aspect_ratio": "3:2", "fit": "pad"}, {"size": "768x512", "fit": "pad"}),
        "sketch": ({}, {}), "chatgpt": ({}, {}),
    }
    for style, (color, shape) in zip(STYLES, subjects):
        for repeat, options in enumerate(transforms[style], 1):
            output = root / "cli-styles" / f"{style}-{repeat}.png"
            cases.append({
                "name": f"cli-style-{style}-{repeat}", "kind": "cli_style", "style": style,
                "repeat": repeat, "prompt": f"A simple {color} {shape} on a plain white background. No text, no people.",
                "options": options, "output": str(output), "status": "pending",
            })
    cases += [
        # Apple rejected repeated Any Style prompts in the live probe. Keep
        # the independent HTTP route on the proven explicit Animation style;
        # the six-style CLI cases still exercise Any directly.
        {"name": "api-generations-aspect", "kind": "api_generations", "style": "animation",
         "prompt": "A simple red circle on a plain white background. No text.",
         "options": {"aspect_ratio": "1:1", "fit": "crop"},
         "output": str(root / "api" / "generations-aspect.png"), "status": "pending"},
        {"name": "api-generations-size", "kind": "api_generations", "style": "animation",
         "prompt": "A simple blue square on a plain white background. No text.",
         "options": {"size": "640x480", "fit": "pad"},
         "output": str(root / "api" / "generations-size.png"), "status": "pending"},
    ]
    for conversation in (1, 2):
        for turn in (1, 2):
            cases.append({
                "name": f"cli-chat-conversation-{conversation}-turn-{turn}", "kind": "cli_chat",
                "style": "animation" if conversation == 1 else "illustration", "conversation": conversation,
                "turn": turn, "prompt": ("Make a simple red circle on white, no text." if turn == 1
                                             else "Keep the same shape; change its color to blue."),
                "options": ({"aspect_ratio": "4:3", "fit": "crop"} if turn == 2 else {}),
                "output": str(root / "cli-chat" / f"conversation-{conversation}-turn-{turn}.png"), "status": "pending",
            })
    for endpoint in ("chat_completions", "responses"):
        for conversation in (1, 2):
            for turn in (1, 2):
                cases.append({
                    "name": f"api-{endpoint}-conversation-{conversation}-turn-{turn}", "kind": endpoint,
                "style": "animation" if conversation == 1 else "illustration", "conversation": conversation,
                "turn": turn, "prompt": ("Make a simple green triangle on white, no text." if turn == 1
                                                 else "Keep the same shape; change its color to blue."),
                    "options": ({"size": "640x480", "fit": "pad"} if turn == 2 else {}),
                    "output": str(root / "api" / f"{endpoint}-conversation-{conversation}-turn-{turn}.png"), "status": "pending",
                })
    for index in (1, 2):
        cases.append({
            "name": f"cli-interactive-image-{index}", "kind": "cli_interactive", "style": "animation",
            "prompt": f"A simple {'orange' if index == 1 else 'yellow'} circle on white, no text.",
            "options": {}, "output": str(root / "interactive" / f"image-{index}.png"), "status": "pending",
        })
    if prompt_set == "scenes":
        for case in cases:
            if case.get("turn") == 2:
                case["prompt"] = "Keep the same subjects and objects, but change the setting to a cozy greenhouse lit by golden evening sunlight."
            else:
                case["prompt"] = SCENE_PROMPTS[str(case["style"])]
            case["prompt_set"] = "scenes"
        # Prove the previously working styles before the less reliable routes.
        order = {style: index for index, style in enumerate(("animation", "illustration", "sketch", "genmoji", "any", "chatgpt"))}
        cases[:12] = sorted(cases[:12], key=lambda case: (order[str(case["style"])], int(case["repeat"])))
    elif prompt_set != "geometry":
        raise ValueError("unknown prompt set")
    if len(cases) != MAX_CALLS:
        raise AssertionError(f"image live plan has {len(cases)} calls, expected {MAX_CALLS}")
    return cases


def validate_selected_dependencies(plan: list[dict[str, object]], selected_indices: list[int]) -> None:
    """Reject a targeted turn-2 run before it can consume a provider call."""
    selected = set(selected_indices)
    for index in selected_indices:
        case = plan[index]
        if case.get("turn") != 2:
            continue
        key = (case.get("kind"), case.get("conversation"))
        predecessor = next((candidate for candidate, earlier in enumerate(plan)
                            if earlier.get("kind") == key[0]
                            and earlier.get("conversation") == key[1]
                            and earlier.get("turn") == 1), None)
        if predecessor is None or predecessor not in selected or predecessor >= index:
            raise SuiteFailure(
                f"{case['name']} requires its selected turn-1 case before the provider call"
            )


def sanitize_invocation(result: dict[str, object]) -> dict[str, object]:
    return {key: _safe_json(value) for key, value in result.items()
            if key in {"argv", "exit", "seconds", "timed_out", "stderr", "stdout"}}


def service_error(result: dict[str, object]) -> bool:
    combined = (str(result.get("stdout", "")) + "\n" + str(result.get("stderr", ""))).lower()
    return any(marker in combined for marker in SERVICE_ERROR_MARKERS)


def cooldown_seconds(last_completed_at: float | None, interval: float, now: float | None = None) -> float:
    if last_completed_at is None:
        return 0.0
    current = time.time() if now is None else now
    return max(0.0, interval - (current - last_completed_at))


def _extract_cli_image(data: object, path: Path, options: dict[str, str]) -> dict[str, object]:
    if not isinstance(data, dict):
        raise SuiteFailure("CLI image response was not an object")
    actual_path = Path(str(data.get("path", ""))).resolve()
    if actual_path != path.resolve():
        raise SuiteFailure("CLI image response named the wrong output path")
    native_width = int(data.get("native_width", 0))
    native_height = int(data.get("native_height", 0))
    if native_width < 1 or native_height < 1:
        raise SuiteFailure("CLI image response omitted native dimensions")
    expected_width, expected_height = expected_dimensions(native_width, native_height, options)
    artifact = validate_png(path, expected={
        "bytes": int(data.get("bytes", -1)), "width": int(data.get("width", -1)),
        "height": int(data.get("height", -1)), "sha256": str(data.get("checksum", "")),
    })
    if (artifact["width"], artifact["height"]) != (expected_width, expected_height):
        raise SuiteFailure("CLI image dimensions do not match requested processing")
    assert_output_processing(data.get("output_processing", {}), options)
    return artifact | {"style": data.get("style"), "native_width": native_width, "native_height": native_height,
                        "expected_width": expected_width, "expected_height": expected_height,
                        "output_processing": data.get("output_processing", {})}


def _extract_generation_image(data: object, path: Path, options: dict[str, str]) -> dict[str, object]:
    if not isinstance(data, dict) or not isinstance(data.get("data"), list) or len(data["data"]) != 1:
        raise SuiteFailure("image generation API response did not contain exactly one image")
    item = data["data"][0]
    if not isinstance(item, dict):
        raise SuiteFailure("image generation API image entry was not an object")
    metadata = decode_base64_png(str(item.get("b64_json", "")), path)
    native_width, native_height = int(item.get("native_width", 0)), int(item.get("native_height", 0))
    if native_width < 1 or native_height < 1:
        raise SuiteFailure("image generation API omitted native dimensions")
    expected = expected_dimensions(native_width, native_height, options)
    if (metadata["width"], metadata["height"]) != expected:
        raise SuiteFailure("image generation API dimensions do not match requested processing")
    if int(item.get("width", -1)) != metadata["width"] or int(item.get("height", -1)) != metadata["height"]:
        raise SuiteFailure("image generation API metadata does not match PNG")
    if str(item.get("sha256", "")) != metadata["sha256"]:
        raise SuiteFailure("image generation API checksum does not match PNG")
    assert_output_processing(item.get("output_processing", {}), options)
    return metadata | {"style": item.get("style"), "native_width": native_width,
                       "native_height": native_height, "output_processing": item.get("output_processing", {})}


def _extract_chat_image(data: object, path: Path, options: dict[str, str], *, responses: bool) -> tuple[dict[str, object], object, str]:
    if not isinstance(data, dict):
        raise SuiteFailure("conversation response was not an object")
    if responses:
        output = data.get("output")
        if not isinstance(output, list) or not output:
            raise SuiteFailure("responses API returned no output message")
        content = output[-1].get("content") if isinstance(output[-1], dict) else None
        if not isinstance(content, list):
            raise SuiteFailure("responses API returned no content parts")
        text = next((part.get("text") for part in content if isinstance(part, dict) and part.get("type") == "output_text"), "")
        image = next((part for part in content if isinstance(part, dict) and part.get("type") == "output_image"), None)
        if not isinstance(image, dict) or not text:
            raise SuiteFailure("responses API returned no generated image and text marker")
        metadata = decode_base64_png(str(image.get("b64_json", "")), path)
        replay = {"type": "message", "role": "assistant", "status": "completed", "content": content}
    else:
        choices = data.get("choices")
        message = choices[0].get("message") if isinstance(choices, list) and choices else None
        content = message.get("content") if isinstance(message, dict) else None
        if not isinstance(content, list):
            raise SuiteFailure("chat completions returned no content parts")
        text = next((part.get("text") for part in content if isinstance(part, dict) and part.get("type") == "text"), "")
        image = next((part for part in content if isinstance(part, dict) and part.get("type") == "image_url"), None)
        if not isinstance(image, dict) or not text:
            raise SuiteFailure("chat completions returned no generated image and text marker")
        image_url = image.get("image_url") if isinstance(image.get("image_url"), dict) else {}
        encoded = str(image_url.get("url", ""))
        if not encoded.startswith("data:image/png;base64,"):
            raise SuiteFailure("chat completions image URL was not an inline PNG")
        metadata = decode_base64_png(encoded.removeprefix("data:image/png;base64,"), path)
        replay = {"role": "assistant", "content": content}
    native_width, native_height = int(image.get("native_width", 0)), int(image.get("native_height", 0))
    if native_width < 1 or native_height < 1:
        raise SuiteFailure("conversation API omitted native dimensions")
    expected = expected_dimensions(native_width, native_height, options)
    if (metadata["width"], metadata["height"]) != expected:
        raise SuiteFailure("conversation API dimensions do not match requested processing")
    if int(image.get("width", -1)) != metadata["width"] or int(image.get("height", -1)) != metadata["height"]:
        raise SuiteFailure("conversation API metadata dimensions do not match PNG")
    if str(image.get("sha256", "")) != metadata["sha256"]:
        raise SuiteFailure("conversation API checksum does not match PNG")
    assert_output_processing(image.get("output_processing", {}), options)
    return metadata | {"native_width": native_width, "native_height": native_height,
                       "style": image.get("style"), "output_processing": image.get("output_processing", {})}, replay, str(text)


def build_api_conversation_input(turn: int, prompt: str, original_prompt: str | None, replay: object | None) -> list[object]:
    """Build text history for an API image follow-up, including the original user turn."""
    if turn == 1:
        return [{"role": "user", "content": prompt}]
    if not original_prompt or replay is None:
        raise SuiteFailure("API conversation follow-up lacks original user prompt or assistant replay")
    return [
        {"role": "user", "content": original_prompt},
        replay,
        {"role": "user", "content": prompt},
    ]


class LiveHarness:
    def __init__(self, binary: Path, root: Path, bridges: dict[str, str], interval: float, report: dict[str, object], report_path: Path, plan: list[dict[str, object]]):
        self.binary = binary
        self.root = root
        self.bridges = bridges
        self.interval = interval
        self.report = report
        self.report_path = report_path
        self.plan = plan
        self.last_completed_at = report.get("pacing", {}).get("last_completed_at") if isinstance(report.get("pacing"), dict) else None
        self.last_completed_at = float(self.last_completed_at) if self.last_completed_at is not None else None
        self.server: subprocess.Popen[str] | None = None
        self.address: str | None = None
        self.chat_conversations: dict[int, str] = {}
        self.api_replays: dict[tuple[str, int], object] = {}
        self.api_prompts: dict[tuple[str, int], str] = {}
        self.env = dict(os.environ)
        self.env["HOLLIS_STATE_DIR"] = str(root / "state")
        self.env.pop("HOLLIS_API_TOKEN", None)

    def save(self) -> None:
        self.report["pacing"] = {
            "interval_seconds": self.interval,
            "last_completed_at": self.last_completed_at,
            "next_allowed_at": (self.last_completed_at + self.interval if self.last_completed_at else None),
        }
        private_json(self.report_path, self.report)

    def pace(self) -> None:
        wait = cooldown_seconds(self.last_completed_at, self.interval)
        if wait:
            self.report["pacing"]["waiting_seconds"] = round(wait, 3)
            self.save()
            time.sleep(wait)

    def dispatch(self, index: int, function):
        if int(self.report.get("attempts", 0)) >= MAX_CALLS:
            raise SuiteFailure("live image call budget exhausted")
        case = self.plan[index]
        self.pace()
        self.report["attempts"] = int(self.report.get("attempts", 0)) + 1
        case["status"] = "dispatching"
        case["attempted_at"] = utc_now()
        case["attempt_number"] = self.report["attempts"]
        self.save()  # Persist before the provider call; a crash cannot look like zero work.
        print(f"[{self.report['attempts']}/{MAX_CALLS}] starting {case['name']}", flush=True)
        try:
            value = function(case)
        except KeyboardInterrupt as exc:
            case["status"] = "interrupted"
            case["error"] = "interrupted by user"
            self.report["stop_reason"] = "interrupted by user"
            self.save()
            raise exc
        except BaseException as exc:
            case["status"] = "failed"
            case["error"] = str(exc)
            self.report["stop_reason"] = str(exc)
            self.save()
            print(f"[{case['attempt_number']}/{MAX_CALLS}] {case['name']}: FAILED ({exc})", flush=True)
            raise
        case["status"] = "passed"
        case["completed_at"] = utc_now()
        self.last_completed_at = time.time()
        self.save()
        print(f"[{case['attempt_number']}/{MAX_CALLS}] {case['name']}: PASS", flush=True)
        return value

    def cli(self, args: list[str], timeout: float = MAX_TIMEOUT) -> dict[str, object]:
        return invoke([str(self.binary), *args], env=self.env, timeout=timeout)

    def ensure_server(self) -> str:
        if self.address:
            return self.address
        self.server = subprocess.Popen(
            [str(self.binary), "serve", "--addr", "127.0.0.1:0"],
            env=self.env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, text=True, start_new_session=True,
        )
        assert self.server.stdout is not None
        selector = selectors.DefaultSelector()
        selector.register(self.server.stdout, selectors.EVENT_READ)
        try:
            deadline = time.monotonic() + 25
            banner = ""
            while time.monotonic() < deadline:
                if self.server.poll() is not None:
                    stderr = self.server.stderr.read() if self.server.stderr else ""
                    raise SuiteFailure("local image API failed to start: " + stderr[-1000:])
                if selector.select(timeout=min(1, max(0, deadline - time.monotonic()))):
                    banner = self.server.stdout.readline().strip()
                    break
            match = re.search(r"http://127\.0\.0\.1:\d+", banner)
            if not match:
                raise SuiteFailure("local image API did not announce a loopback listener")
            self.address = match.group(0)
            self.report["api_address"] = self.address
            self.save()
            return self.address
        finally:
            selector.close()

    def post(self, path: str, body: dict[str, object]) -> tuple[int, dict[str, object]]:
        request = urllib.request.Request(
            self.ensure_server() + path, json.dumps(body).encode("utf-8"),
            headers={"Content-Type": "application/json", "User-Agent": UA}, method="POST",
        )
        try:
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=MAX_TIMEOUT) as response:
                status, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, raw = error.code, error.read()
        try:
            decoded = json.loads(raw.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            if status >= 400:
                return status, {"error": {"type": "invalid_error_response", "message": f"HTTP {status} returned a non-JSON error"}}
            raise SuiteFailure(f"HTTP {status} response was not JSON") from exc
        if not isinstance(decoded, dict):
            raise SuiteFailure(f"HTTP {status} response was not an object")
        return status, decoded

    def run_cli_style(self, case: dict[str, object]) -> dict[str, object]:
        output = Path(str(case["output"])); output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        args = ["--json", "--no-input", "image", "generate", str(case["prompt"]), "--style", str(case["style"]),
                "--output", str(output), "--timeout", "120s", *_output_options_args(case["options"])]
        result = self.cli(args)
        case["invocation"] = sanitize_invocation(result)
        if result.get("exit") != 0 or result.get("timed_out") or service_error(result):
            raise SuiteFailure("CLI style generation failed")
        try:
            data = json.loads(str(result.get("stdout", "")))
        except json.JSONDecodeError as exc:
            raise SuiteFailure("CLI style generation returned invalid JSON") from exc
        artifact = _extract_cli_image(data, output, case["options"])
        case["artifact"] = artifact
        return artifact

    def run_api_generations(self, case: dict[str, object]) -> dict[str, object]:
        output = Path(str(case["output"])); output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        body = {"model": "hollis-image", "prompt": case["prompt"], "style": case["style"],
                "n": 1, "response_format": "b64_json", **case["options"]}
        status, data = self.post("/v1/images/generations", body)
        case["request"] = {key: value for key, value in body.items()}
        case["http_status"] = status
        if status != 200:
            case["error_response"] = safe_error_envelope(status, data)
            raise SuiteFailure(f"image generations returned HTTP {status}")
        artifact = _extract_generation_image(data, output, case["options"])
        if artifact.get("style") != case["style"]:
            raise SuiteFailure("image generations response returned the wrong style")
        case["artifact"] = artifact
        return artifact

    def run_cli_chat(self, case: dict[str, object]) -> dict[str, object]:
        output = Path(str(case["output"])); output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        if case["turn"] == 1:
            args = ["--json", "--no-input", "chat", "--generate-image", "--image-style", str(case["style"]),
                    "--output", str(output), str(case["prompt"])]
        else:
            conversation = self.chat_conversations.get(int(case["conversation"]))
            if not conversation:
                raise SuiteFailure("CLI chat continuation has no preceding conversation")
            args = ["--json", "--no-input", "chat", "--continue", conversation, "--generate-image",
                    "--image-style", str(case["style"]), "--output", str(output), str(case["prompt"]),
                    *_output_options_args(case["options"])]
        result = self.cli(args)
        case["invocation"] = sanitize_invocation(result)
        if result.get("exit") != 0 or result.get("timed_out") or service_error(result):
            raise SuiteFailure("CLI chat image generation failed")
        try:
            data = json.loads(str(result.get("stdout", "")))
        except json.JSONDecodeError as exc:
            raise SuiteFailure("CLI chat image generation returned invalid JSON") from exc
        if not data.get("conversation_id"):
            raise SuiteFailure("CLI chat image generation omitted conversation_id")
        if case["turn"] == 1:
            self.chat_conversations[int(case["conversation"])] = str(data["conversation_id"])
        elif data["conversation_id"] != self.chat_conversations.get(int(case["conversation"])):
            raise SuiteFailure("CLI chat continuation changed conversation_id")
        artifact = _extract_cli_image(data, output, case["options"])
        case["artifact"] = artifact
        return artifact

    def run_api_conversation(self, case: dict[str, object], *, responses: bool) -> dict[str, object]:
        output = Path(str(case["output"])); output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        replay_key = ("responses" if responses else "chat_completions", int(case["conversation"]))
        original_prompt = self.api_prompts.get(replay_key)
        replay = self.api_replays.get(replay_key)
        history = build_api_conversation_input(int(case["turn"]), str(case["prompt"]), original_prompt, replay)
        if responses:
            input_value: object = history
            body = {"model": "hollis-image", "input": input_value, "image_generation": {"style": case["style"], **case["options"]}}
            endpoint = "/v1/responses"
        else:
            messages: object = history
            body = {"model": "hollis-image", "messages": messages, "image_generation": {"style": case["style"], **case["options"]}}
            endpoint = "/v1/chat/completions"
        status, data = self.post(endpoint, body)
        case["request"] = {"endpoint": endpoint, "model": "hollis-image", "style": case["style"],
                            "turn": case["turn"], "message_count": len(body.get("input", body.get("messages", []))),
                            "replayed_assistant": case["turn"] == 2,
                            "replayed_original_user": case["turn"] == 2,
                            "followup_depends_on_text_context": case["turn"] == 2}
        case["http_status"] = status
        if status != 200:
            case["error_response"] = safe_error_envelope(status, data)
            raise SuiteFailure(f"{endpoint} returned HTTP {status}")
        artifact, replay, marker = _extract_chat_image(data, output, case["options"], responses=responses)
        if artifact.get("style") != case["style"]:
            raise SuiteFailure("conversation response returned the wrong style")
        if case["turn"] == 1:
            self.api_replays[replay_key] = replay
            self.api_prompts[replay_key] = str(case["prompt"])
        case["marker"] = marker
        case["artifact"] = artifact
        return artifact

    def run_cli_interactive(self, case: dict[str, object]) -> dict[str, object]:
        output = Path(str(case["output"])); output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        result = invoke_interactive([str(self.binary), "chat", "--image-style", str(case["style"]), "--output", str(output)],
                                    self.env, f"/image {case['prompt']}\n", timeout=MAX_TIMEOUT)
        case["invocation"] = sanitize_invocation(result)
        if result.get("exit") != 0 or result.get("timed_out") or service_error(result):
            raise SuiteFailure("interactive /image generation failed")
        artifact = validate_png(output)
        case["artifact"] = artifact
        return artifact

    def run_case(self, index: int) -> object:
        case = self.plan[index]
        kind = case["kind"]
        if kind == "cli_style":
            return self.dispatch(index, self.run_cli_style)
        if kind == "api_generations":
            return self.dispatch(index, self.run_api_generations)
        if kind == "cli_chat":
            return self.dispatch(index, self.run_cli_chat)
        if kind == "chat_completions":
            return self.dispatch(index, lambda current: self.run_api_conversation(current, responses=False))
        if kind == "responses":
            return self.dispatch(index, lambda current: self.run_api_conversation(current, responses=True))
        if kind == "cli_interactive":
            return self.dispatch(index, self.run_cli_interactive)
        raise SuiteFailure(f"unknown live case kind {kind}")

    def shutdown(self) -> None:
        if self.server is None:
            return
        if self.server.poll() is None:
            try:
                stdout, stderr = _terminate_session(self.server, 0)
            except Exception as exc:  # preserve the cleanup uncertainty in the report
                self.report["server_shutdown_error"] = str(exc)
            else:
                self.report["server_shutdown"] = {
                    "exit": self.server.returncode, "stdout": _safe_json(stdout), "stderr": _safe_json(stderr),
                }
        else:
            self.report["server_shutdown"] = {"exit": self.server.returncode}


def invoke_interactive(argv: list[str], env: dict[str, str], input_text: str, *, timeout: float) -> dict[str, object]:
    """Run one interactive chat in a private PTY and own its process group."""
    master, slave = pty.openpty()
    process: subprocess.Popen[bytes] | None = None
    output = bytearray()
    started = time.monotonic()
    timed_out = False
    try:
        process = subprocess.Popen(argv, stdin=slave, stdout=slave, stderr=slave, env=env,
                                   start_new_session=True, close_fds=True)
        os.close(slave)
        slave = -1
        os.set_blocking(master, False)
        os.write(master, input_text.encode("utf-8"))
        os.write(master, b"\x04")
        while True:
            if time.monotonic() - started > timeout:
                timed_out = True
                _terminate_session(process, 0)
                break
            # Use a fresh selector for the PTY so the loop never blocks past
            # the process deadline.
            selector = selectors.DefaultSelector()
            try:
                selector.register(master, selectors.EVENT_READ)
                ready = selector.select(timeout=1)
            finally:
                selector.close()
            if ready:
                try:
                    output.extend(os.read(master, 65536))
                except OSError:
                    pass
            if process.poll() is not None:
                # Drain anything buffered by the PTY before returning.
                try:
                    while True:
                        chunk = os.read(master, 65536)
                        if not chunk:
                            break
                        output.extend(chunk)
                except OSError:
                    pass
                break
    except BaseException:
        if process is not None and process.poll() is None:
            try:
                _terminate_session(process, 0)
            except Exception as cleanup_error:
                raise RuntimeError("interactive process cleanup failed") from cleanup_error
        raise
    finally:
        if process is not None and process.poll() is None:
            try:
                _terminate_session(process, 0)
            except Exception as cleanup_error:
                # A normal return or timeout must report cleanup failure; the
                # caller cannot mistake an unowned child for a pass.
                raise RuntimeError("interactive process cleanup failed") from cleanup_error
        try:
            os.close(master)
        except OSError:
            pass
        if slave >= 0:
            try:
                os.close(slave)
            except OSError:
                pass
    return {"argv": argv, "exit": process.returncode if process is not None else -1,
            "seconds": round(time.monotonic() - started, 3),
            "stdout": output.decode("utf-8", "replace"), "stderr": "", "timed_out": timed_out}


def preflight(binary: Path, env: dict[str, str]) -> dict[str, object]:
    result = invoke([str(binary), "version"], env=env, timeout=20)
    if result.get("exit") != 0 or result.get("timed_out"):
        raise SuiteFailure("binary version preflight failed")
    return sanitize_invocation(result)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path, help="absolute path to the exact Hollis binary under test")
    parser.add_argument("--output", type=Path, help="new private report directory")
    parser.add_argument("--bridges-json", help="JSON object or file mapping all six styles to fixed Shortcut names")
    parser.add_argument("--unified-bridge", help="one parameterized Shortcut used for all six styles")
    parser.add_argument("--prompt-set", choices=("geometry", "scenes"), default="geometry", help="synthetic geometry or realistic scene descriptions")
    parser.add_argument("--interval", type=float, default=DEFAULT_INTERVAL, help="seconds between completed provider calls (minimum 30)")
    parser.add_argument("--live", action="store_true", help="authorize at most 28 real image generation calls")
    parser.add_argument("--plan", action="store_true", help="print the complete offline 28-case matrix without creating a report or calling a model")
    parser.add_argument("--case", action="append", default=[], metavar="ID", help="run only this exact case ID; may be repeated or comma-separated")
    parser.add_argument("--start", type=int, default=0, metavar="INDEX", help="start at this zero-based case index (for a fresh, targeted follow-up)")
    args = parser.parse_args(argv)
    if args.plan and args.live:
        parser.error("--plan and --live are mutually exclusive")
    if not math.isfinite(args.interval) or args.interval < DEFAULT_INTERVAL:
        parser.error("--interval must be at least 30 seconds")
    if args.start < 0 or args.start >= MAX_CALLS:
        parser.error(f"--start must be between 0 and {MAX_CALLS - 1}")
    if not args.binary.is_absolute():
        parser.error("--binary must be an absolute path")
    if not args.binary.is_file():
        parser.error("--binary must name a regular file")
    os.umask(0o077)
    if args.bridges_json and args.unified_bridge:
        parser.error("--bridges-json and --unified-bridge are mutually exclusive")
    if args.unified_bridge:
        unified_bridge = args.unified_bridge.strip()
        if not unified_bridge or "\x00" in unified_bridge or unified_bridge.lstrip().startswith("-"):
            parser.error("--unified-bridge must be a nonempty Shortcut name")
        bridges = {style: unified_bridge for style in STYLES}
    else:
        unified_bridge = None
        bridges = load_bridges(args.bridges_json, require_all=args.live)
    plan_root = args.output.resolve() if args.output else Path("/private/tmp/hollis-image-live-plan")
    plan = build_plan(plan_root, args.prompt_set)
    requested_cases = {item for raw in args.case for item in raw.split(",") if item}
    known_cases = {str(case["name"]) for case in plan}
    unknown_cases = requested_cases - known_cases
    if unknown_cases:
        parser.error("unknown --case ID(s): " + ", ".join(sorted(unknown_cases)))
    selected_indices = [index for index, case in enumerate(plan)
                        if index >= args.start and (not requested_cases or str(case["name"]) in requested_cases)]
    if args.plan:
        print(json.dumps({"max_calls": MAX_CALLS, "interval_seconds": args.interval,
                          "selected_cases": [plan[index]["name"] for index in selected_indices],
                          "plan": plan}, indent=2, sort_keys=True))
        return 0
    if args.output is None:
        parser.error("--output is required unless --plan is used")
    root = args.output.resolve()
    if root.exists():
        parser.error("--output must name a new report directory")
    root.mkdir(mode=0o700, parents=True)
    # Rebuild paths against the actual report directory after the preflight
    # plan-only pass. Unselected cases remain visible in the report as skipped.
    plan = build_plan(root, args.prompt_set)
    if requested_cases:
        selected_indices = [index for index, case in enumerate(plan)
                            if index >= args.start and str(case["name"]) in requested_cases]
    for index, case in enumerate(plan):
        if index not in selected_indices:
            case["status"] = "skipped"
            case["skip_reason"] = "not selected by --case"
    report_path = root / "report.json"
    report: dict[str, object] = {
        "schema_version": 1, "prompt_set": args.prompt_set, "mode": "live" if args.live else "dry-run", "created_at": utc_now(),
        "binary": {"path": str(args.binary.resolve()), "sha256": digest(args.binary)},
        "revision": repository_revision(),
        "max_calls": MAX_CALLS, "attempts": 0, "status": "planned", "bridges": bridges,
        "bridge_mode": "unified" if unified_bridge else "per_style",
        "unified_bridge": unified_bridge,
        "plan": plan, "selected_cases": [plan[index]["name"] for index in selected_indices],
        "pacing": {"interval_seconds": args.interval, "last_completed_at": None},
    }
    private_json(report_path, report)
    print(f"Private report: {report_path}", flush=True)
    if not args.live:
        print(f"Dry run: planned {len(selected_indices)} selected calls ({len(plan)} total cases); no binary or model was invoked.", flush=True)
        return 0

    try:
        validate_selected_dependencies(plan, selected_indices)
    except SuiteFailure as exc:
        report["status"] = "stopped"
        report["stop_reason"] = str(exc)
        private_json(report_path, report)
        print(f"Stopped before provider calls: {exc}", flush=True)
        print(f"Report: {report_path}", flush=True)
        return 1

    state = root / "state"
    write_image_config(state, bridges, unified_bridge=unified_bridge)
    env = dict(os.environ, HOLLIS_STATE_DIR=str(state))
    env.pop("HOLLIS_API_TOKEN", None)
    harness = LiveHarness(args.binary.resolve(), root, bridges, args.interval, report, report_path, plan)
    exit_code = 1
    try:
        report["preflight"] = preflight(args.binary.resolve(), env)
        report["status"] = "running"
        harness.save()
        for index in selected_indices:
            harness.run_case(index)
        report["status"] = "completed"
        report["completed_at"] = utc_now()
        exit_code = 0
    except KeyboardInterrupt:
        report["status"] = "interrupted"
        report["stop_reason"] = "interrupted by user"
        exit_code = 1
    except (SuiteFailure, RuntimeError, OSError, ValueError, json.JSONDecodeError) as exc:
        report["status"] = "stopped"
        report["stop_reason"] = str(exc)
        exit_code = 1
    finally:
        harness.shutdown()
        if "server_shutdown_error" in report:
            report["status"] = "stopped"
            report["stop_reason"] = "owned API server cleanup could not be verified"
            exit_code = 1
        # Remaining cases stay explicitly pending for review; no implicit retry.
        for case in plan:
            case.setdefault("status", "pending")
        harness.save()
        print(f"Report: {report_path}", flush=True)
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
