#!/usr/bin/env python3
"""Bounded, opt-in acceptance checks for image conversations.

The default mode prints a plan and never starts Hollis.  ``run`` is deliberately
small: it exercises one PTY session, one CLI conversation, and/or one HTTP
conversation at a time.  The root agent owns all live invocations; this module
only supplies the runner and its offline checks.

Every live generation is serialized under the same lock used by image_suite.py
and waits for ``--settle-seconds`` *after the previous generation completes*.
The floor is three seconds so a caller cannot accidentally hammer Image
Playground.  The runner records the observed gap in its private report.
"""
from __future__ import annotations

import argparse
import base64
import binascii
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import sys
import tempfile
import time
from typing import Any
import urllib.error
import urllib.request
import zlib

from process_control import invoke


MIN_SETTLE_SECONDS = 3.0
DEFAULT_SETTLE_SECONDS = 3.0
DEFAULT_STYLE = "animation"
LOCK_NAME = f"hollis-image-research-{os.getuid()}.lock"
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
PNG_CHANNELS = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}
ADAM7_PASSES = ((0, 0, 8, 8), (4, 0, 8, 8), (0, 4, 4, 8), (2, 0, 4, 4), (0, 2, 2, 4), (1, 0, 2, 2), (0, 1, 1, 2))
CASE_GENERATIONS = {
    "interactive-first": 1,
    "interactive-sequence": 3,
    "cli-followup": 2,
    "api-chat-completions": 3,
    "api-responses": 3,
    "api-reference-generations": 2,
    "dynamic-reference": 2,
}
MAX_GENERATIONS = 16

PROMPTS = {
    "interactive": "A small orange ceramic lighthouse on a blue shoreline at sunrise, no text.",
    "cli": "A brass sea turtle carrying a tiny glass greenhouse in a calm turquoise ocean, no text.",
    "cli_revision": "Keep the same turtle and greenhouse, but move them into a warm sunlit conservatory.",
    "cli_revision_2": "Keep the same subjects, but change the setting to a snowy pine forest under blue moonlight.",
    "api": "A red fox beside a mossy stone in a quiet woodland, no text.",
    "api_revision": "Keep the fox and stone, but change the setting to a misty mountain meadow.",
}


class AcceptanceError(RuntimeError):
    """A check could not establish its acceptance condition."""


def utc_now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def private_json(path: Path, data: Any) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(data, stream, indent=2)
        stream.write("\n")
    os.replace(tmp, path)
    os.chmod(path, 0o600)


def extract_json(stdout: str) -> dict[str, Any]:
    """Decode the last JSON object, tolerating terminal echo around it."""
    candidates = [line.strip() for line in stdout.splitlines() if line.strip()]
    for candidate in reversed(candidates):
        try:
            value = json.loads(candidate)
        except ValueError:
            continue
        if isinstance(value, dict):
            return value
    raise AcceptanceError(f"no JSON object in command output: {stdout[-400:]!r}")


def png_metadata(path: Path) -> dict[str, Any]:
    """Validate PNG chunks and fully decompress scanlines without Pillow."""
    if not path.is_file():
        raise AcceptanceError(f"expected output was not created: {path}")
    data = path.read_bytes()
    if len(data) < 33 or data[:8] != PNG_SIGNATURE:
        raise AcceptanceError(f"output is not a PNG: {path}")
    offset = 8
    width = height = bit_depth = color_type = interlace = None
    idat = bytearray()
    saw_ihdr = saw_idat = saw_iend = False
    while offset < len(data):
        if len(data) - offset < 12:
            raise AcceptanceError(f"PNG has a truncated chunk: {path}")
        chunk_len = struct.unpack(">I", data[offset:offset + 4])[0]
        chunk_start = offset + 8
        chunk_end = chunk_start + chunk_len
        if chunk_end + 4 > len(data):
            raise AcceptanceError(f"PNG chunk exceeds file length: {path}")
        chunk_type = data[offset + 4:offset + 8]
        chunk_data = data[chunk_start:chunk_end]
        expected_crc = struct.unpack(">I", data[chunk_end:chunk_end + 4])[0]
        if (binascii.crc32(chunk_type + chunk_data) & 0xffffffff) != expected_crc:
            raise AcceptanceError(f"PNG chunk CRC is invalid: {path}")
        if chunk_type == b"IHDR":
            if saw_ihdr or chunk_len != 13:
                raise AcceptanceError(f"PNG has an invalid IHDR: {path}")
            width, height, bit_depth, color_type, compression, filter_method, interlace = struct.unpack(">IIBBBBB", chunk_data)
            if compression != 0 or filter_method != 0 or color_type not in PNG_CHANNELS:
                raise AcceptanceError(f"PNG uses unsupported encoding parameters: {path}")
            valid_depths = {0: (1, 2, 4, 8, 16), 2: (8, 16), 3: (1, 2, 4, 8), 4: (8, 16), 6: (8, 16)}
            if bit_depth not in valid_depths[color_type] or interlace not in (0, 1):
                raise AcceptanceError(f"PNG uses invalid bit depth or interlace mode: {path}")
            saw_ihdr = True
        elif chunk_type == b"IDAT":
            if not saw_ihdr or saw_iend:
                raise AcceptanceError(f"PNG has IDAT in an invalid position: {path}")
            idat.extend(chunk_data)
            saw_idat = True
        elif chunk_type == b"IEND":
            if chunk_len != 0 or not saw_ihdr or not saw_idat or saw_iend:
                raise AcceptanceError(f"PNG has an invalid IEND: {path}")
            saw_iend = True
        offset = chunk_end + 4
        if saw_iend:
            if offset != len(data):
                raise AcceptanceError(f"PNG has trailing bytes after IEND: {path}")
            break
    if not saw_ihdr or not saw_idat or not saw_iend or width is None or height is None or bit_depth is None or color_type is None or interlace is None:
        raise AcceptanceError(f"PNG is missing required chunks: {path}")
    if width < 1 or height < 1:
        raise AcceptanceError(f"PNG dimensions are invalid: {path}")

    def row_bytes(row_width: int) -> int:
        return (row_width * PNG_CHANNELS[color_type] * bit_depth + 7) // 8

    expected_raw = 0
    pass_shapes = ((width, height),) if interlace == 0 else tuple(
        (max(0, (width - x_start + x_step - 1) // x_step), max(0, (height - y_start + y_step - 1) // y_step))
        for x_start, y_start, x_step, y_step in ADAM7_PASSES
    )
    for pass_width, pass_height in pass_shapes:
        if pass_width and pass_height:
            expected_raw += pass_height * (1 + row_bytes(pass_width))
    try:
        decompressor = zlib.decompressobj()
        raw = decompressor.decompress(bytes(idat)) + decompressor.flush()
    except zlib.error as exc:
        raise AcceptanceError(f"PNG IDAT stream cannot be decompressed: {path}") from exc
    if not decompressor.eof or decompressor.unused_data or len(raw) != expected_raw:
        raise AcceptanceError(f"PNG scanline stream is incomplete or has unexpected length: {path}")
    cursor = 0
    for pass_width, pass_height in pass_shapes:
        if not pass_width or not pass_height:
            continue
        row_size = 1 + row_bytes(pass_width)
        for _ in range(pass_height):
            if raw[cursor] > 4:
                raise AcceptanceError(f"PNG uses an invalid scanline filter: {path}")
            cursor += row_size
    return {"path": str(path), "bytes": len(data), "width": width, "height": height, "sha256": hashlib.sha256(data).hexdigest()}


def ensure_output_absent(path: Path) -> None:
    if path.exists():
        raise AcceptanceError(f"test output already exists; use a fresh directory: {path}")
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)


def parse_conversation_id(text: str) -> str:
    match = re.search(r"conversation_id:\s*([^\s\r\n]+)", text)
    if not match:
        raise AcceptanceError(f"interactive output did not expose conversation_id: {text[-500:]!r}")
    return match.group(1)


def validate_cli_image(result: dict[str, Any], output: Path) -> dict[str, Any]:
    if result.get("timed_out") or result.get("exit") != 0:
        raise AcceptanceError(f"Hollis failed with exit={result.get('exit')}: {result.get('stderr', '')[-500:]}")
    metadata = extract_json(result.get("stdout", ""))
    if metadata.get("path") != str(output):
        raise AcceptanceError(f"CLI metadata path mismatch: {metadata!r}")
    artifact = png_metadata(output)
    if metadata.get("bytes") != artifact["bytes"] or metadata.get("width") != artifact["width"] or metadata.get("height") != artifact["height"]:
        raise AcceptanceError(f"CLI metadata does not match PNG: {metadata!r} vs {artifact!r}")
    return {"metadata": metadata, "png": artifact}


class GenerationQueue:
    """Serialize live generations and enforce a completion-to-start gap."""

    def __init__(self, settle_seconds: float, report: dict[str, Any]):
        if settle_seconds < MIN_SETTLE_SECONDS:
            raise ValueError(f"settle_seconds must be at least {MIN_SETTLE_SECONDS:g}")
        self.settle_seconds = settle_seconds
        self.report = report
        self.last_completed: float | None = None

    def before_generation(self, name: str) -> float:
        if self.last_completed is None:
            return 0.0
        remaining = self.settle_seconds - (time.monotonic() - self.last_completed)
        if remaining > 0:
            time.sleep(remaining)
        gap = time.monotonic() - self.last_completed
        self.report.setdefault("generation_gaps", []).append({"before": name, "seconds": round(gap, 3)})
        return gap

    def completed(self) -> None:
        self.last_completed = time.monotonic()


def pty_invoke(argv: list[str], env: dict[str, str], input_bytes: bytes, timeout: float = 180.0) -> dict[str, Any]:
    """Run an interactive command under a real terminal and collect its output."""
    started = time.monotonic()
    pid, master = pty.fork()
    if pid == 0:
        os.execvpe(argv[0], argv, env)
    output = bytearray()
    timed_out = False
    try:
        os.write(master, input_bytes)
        deadline = started + timeout
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                timed_out = True
                break
            ready, _, _ = select.select([master], [], [], min(1.0, remaining))
            if not ready:
                continue
            try:
                chunk = os.read(master, 65536)
            except OSError as exc:
                if exc.errno == errno.EIO:
                    break
                raise
            if not chunk:
                break
            output.extend(chunk)
    finally:
        if timed_out:
            try:
                os.killpg(pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                os.killpg(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        os.close(master)
    _, status = os.waitpid(pid, 0)
    exit_code = os.waitstatus_to_exitcode(status)
    return {
        "argv": argv,
        "exit": exit_code,
        "timed_out": timed_out,
        "seconds": round(time.monotonic() - started, 3),
        "output": output.decode("utf-8", errors="replace"),
    }


def _pty_read_until(master: int, output: bytearray, needle: bytes, deadline: float) -> None:
    """Read one PTY until a product completion marker or timeout."""
    while needle not in output:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise AcceptanceError(f"interactive process did not emit {needle!r}")
        ready, _, _ = select.select([master], [], [], min(1.0, remaining))
        if not ready:
            continue
        try:
            chunk = os.read(master, 65536)
        except OSError as exc:
            if exc.errno == errno.EIO:
                raise AcceptanceError("interactive process closed its PTY before the completion marker") from exc
            raise
        if not chunk:
            raise AcceptanceError("interactive process closed its PTY before the completion marker")
        output.extend(chunk)


def run_interactive_sequence(binary: Path, state: Path, output: Path, style: str, queue: GenerationQueue, report: dict[str, Any]) -> None:
    """Exercise three sequential /image turns, waiting between completions.

    The current CLI deliberately chooses fresh ``-2`` and ``-3`` paths after
    the first explicit destination.  Verifying the paths from the PTY output
    catches both accidental overwrite and a follow-up that never reached the
    Shortcut.  This case remains safe if that behavior is not present: it
    fails with the exact emitted path and leaves the report for diagnosis.
    """
    output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    expected = [output, output.with_name(output.stem + "-2" + output.suffix), output.with_name(output.stem + "-3" + output.suffix)]
    for path in expected:
        ensure_output_absent(path)
    env = dict(os.environ, HOLLIS_STATE_DIR=str(state))
    argv = [str(binary), "chat", "--image-style", style, "--output", str(output)]
    started = time.monotonic()
    pid, master = pty.fork()
    if pid == 0:
        os.execvpe(argv[0], argv, env)
    collected = bytearray()
    markers: list[str] = []
    try:
        for index, (path, prompt_key) in enumerate(zip(expected, ("cli", "cli_revision", "cli_revision_2"))):
            queue.before_generation(f"interactive-sequence-{index + 1}")
            os.write(master, (f"/image {PROMPTS[prompt_key]}\n").encode())
            _pty_read_until(master, collected, ("Saved PNG to " + str(path)).encode(), started + 600)
            markers.append(str(path))
            png = png_metadata(path)
            report.setdefault("interactive_sequence", {}).setdefault("images", []).append(png)
            queue.completed()
        os.write(master, b"\x04")
        # Drain until the child exits.  PTY EIO is the normal macOS EOF signal.
        while True:
            ready, _, _ = select.select([master], [], [], 1)
            if not ready:
                if time.monotonic() - started > 600:
                    raise AcceptanceError("interactive sequence did not exit after Ctrl-D")
                continue
            try:
                chunk = os.read(master, 65536)
            except OSError as exc:
                if exc.errno == errno.EIO:
                    break
                raise
            if not chunk:
                break
            collected.extend(chunk)
    except BaseException:
        try:
            os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        raise
    finally:
        os.close(master)
    _, status = os.waitpid(pid, 0)
    exit_code = os.waitstatus_to_exitcode(status)
    if exit_code != 0:
        raise AcceptanceError(f"interactive sequence exited {exit_code}: {collected.decode('utf-8', errors='replace')[-800:]}")
    report["interactive_sequence"]["invocation"] = {"argv": argv, "exit": exit_code, "seconds": round(time.monotonic() - started, 3), "output": collected.decode("utf-8", errors="replace")}
    report["interactive_sequence"]["paths"] = markers
    if markers != [str(path) for path in expected]:
        raise AcceptanceError(f"interactive sequence paths were not deterministic: {markers!r}")


def run_interactive_first(binary: Path, state: Path, output: Path, style: str, queue: GenerationQueue, report: dict[str, Any]) -> None:
    ensure_output_absent(output)
    queue.before_generation("interactive-first")
    env = dict(os.environ, HOLLIS_STATE_DIR=str(state))
    argv = [str(binary), "chat", "--image-style", style, "--output", str(output)]
    result = pty_invoke(argv, env, (f"/image {PROMPTS['interactive']}\n\x04").encode())
    queue.completed()
    report["interactive"] = {"invocation": result, "png": png_metadata(output) if output.exists() else None}
    if result["timed_out"] or result["exit"] != 0:
        raise AcceptanceError(f"interactive /image failed: {result['output'][-800:]}")
    if "Saved PNG to " + str(output) not in result["output"]:
        raise AcceptanceError(f"interactive /image did not report its output: {result['output'][-800:]}")
    report["interactive"]["conversation_id"] = parse_conversation_id(result["output"])


def run_cli_followup(binary: Path, state: Path, output_dir: Path, style: str, queue: GenerationQueue, report: dict[str, Any], references: list[str] | None = None) -> None:
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    first = output_dir / "cli-first.png"
    second = output_dir / "cli-followup.png"
    ensure_output_absent(first)
    ensure_output_absent(second)
    reference_sha256: str | None = None
    if references:
        reference = Path(references[0])
        if not reference.is_absolute() or not reference.is_file():
            raise AcceptanceError(f"reference image must be an existing absolute file: {reference}")
        reference_sha256 = hashlib.sha256(reference.read_bytes()).hexdigest()
    env = dict(os.environ, HOLLIS_STATE_DIR=str(state))
    first_argv = [str(binary), "--json", "--no-input", "chat", "--generate-image", "--image-style", style, "--output", str(first), PROMPTS["cli"]]
    if references:
        first_argv += ["--image-reference", references[0]]
    queue.before_generation("cli-first")
    first_result = invoke(first_argv, env=env, timeout=180)
    queue.completed()
    first_check = validate_cli_image(first_result, first)
    first_meta = extract_json(first_result["stdout"])
    if references and (first_meta.get("reference_image_sent") is not True or first_meta.get("reference_sha256") != reference_sha256):
        raise AcceptanceError(f"CLI first turn did not report the supplied reference image: {first_meta!r}")
    conversation_id = str(first_meta.get("conversation_id", ""))
    if not conversation_id:
        raise AcceptanceError("CLI image response omitted conversation_id")

    followup_argv = [str(binary), "--json", "--no-input", "chat", "--continue", conversation_id, "--generate-image", "--image-style", style, "--output", str(second), PROMPTS["cli_revision"]]
    if references:
        followup_argv += ["--image-reference", "auto"]
    queue.before_generation("cli-followup")
    followup_result = invoke(followup_argv, env=env, timeout=180)
    queue.completed()
    followup_check = validate_cli_image(followup_result, second)
    followup_meta = extract_json(followup_result["stdout"])
    if followup_meta.get("reference_image_sent") is not True or followup_meta.get("reference_sha256") != first_check["png"]["sha256"]:
        raise AcceptanceError(f"CLI follow-up did not report automatic reference reuse: {followup_meta!r}")
    case_key = "dynamic_reference" if references else "cli_followup"
    report[case_key] = {
        "first": {"invocation": first_result, **first_check},
        "followup": {"invocation": followup_result, **followup_check},
        "conversation_id": conversation_id,
    }
    if reference_sha256:
        report[case_key]["reference_input"] = {"path": references[0], "sha256": reference_sha256, "mode": "explicit-then-auto"}


def api_request(base_url: str, path: str, payload: dict[str, Any], token: str, timeout: float = 180.0) -> tuple[int, dict[str, Any]]:
    body = json.dumps(payload).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    request = urllib.request.Request(base_url.rstrip("/") + path, data=body, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            data = json.loads(response.read())
            return response.status, data
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", errors="replace")
        try:
            data = json.loads(raw)
        except ValueError:
            data = {"raw": raw}
        return exc.code, data


def api_image_part(kind: str, data: dict[str, Any]) -> dict[str, Any]:
    try:
        if kind == "chat-completions":
            parts = data["choices"][0]["message"]["content"]
            return next(part for part in parts if part.get("type") == "image_url")
        if kind == "responses":
            parts = data["output"][0]["content"]
            return next(part for part in parts if part.get("type") == "output_image")
        raise ValueError(f"unsupported API kind {kind!r}")
    except (KeyError, IndexError, StopIteration, TypeError) as exc:
        raise AcceptanceError(f"{kind} response did not contain an image part: {data!r}") from exc


def api_text_part(kind: str, data: dict[str, Any]) -> str:
    try:
        if kind == "chat-completions":
            parts = data["choices"][0]["message"]["content"]
            return next(part["text"] for part in parts if part.get("type") == "text")
        if kind == "responses":
            parts = data["output"][0]["content"]
            return next(part["text"] for part in parts if part.get("type") == "output_text")
        raise ValueError(f"unsupported API kind {kind!r}")
    except (KeyError, IndexError, StopIteration, TypeError) as exc:
        raise AcceptanceError(f"{kind} response did not contain an output text part") from exc


def inline_data_url(path: Path) -> str:
    raw = path.read_bytes()
    return "data:image/png;base64," + base64.b64encode(raw).decode("ascii")


def validate_reference_delivery(kind: str, response: dict[str, Any], expected_sha256: str) -> dict[str, Any]:
    part = api_image_part(kind, response)
    if part.get("reference_image_sent") is not True:
        raise AcceptanceError(f"{kind} follow-up did not report reference_image_sent=true")
    if part.get("reference_image_sha256") != expected_sha256:
        raise AcceptanceError(
            f"{kind} reference checksum mismatch: {part.get('reference_image_sha256')!r} != {expected_sha256!r}"
        )
    marker = api_text_part(kind, response)
    if "supplied reference image was sent" not in marker:
        raise AcceptanceError(f"{kind} follow-up marker did not record reference delivery: {marker!r}")
    return {"reference_image_sent": True, "reference_image_sha256": expected_sha256}


def extract_api_image(kind: str, data: dict[str, Any], output: Path) -> dict[str, Any]:
    try:
        image = api_image_part(kind, data)
        if kind == "chat-completions":
            encoded = image["image_url"]["url"].split(",", 1)[1]
        else:
            encoded = image["b64_json"]
        raw = base64.b64decode(encoded, validate=True)
    except (binascii.Error, KeyError, IndexError, StopIteration, ValueError, TypeError) as exc:
        raise AcceptanceError(f"{kind} response did not contain a valid generated image: {data!r}") from exc
    ensure_output_absent(output)
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(raw)
    png = png_metadata(output)
    if image.get("sha256") and image["sha256"] != png["sha256"]:
        raise AcceptanceError(f"{kind} response SHA-256 does not match returned PNG")
    return {"response": data, "png": png}


def standalone_image_part(data: dict[str, Any]) -> dict[str, Any]:
    try:
        part = data["data"][0]
        if not isinstance(part, dict):
            raise TypeError("image data part is not an object")
        return part
    except (KeyError, IndexError, TypeError) as exc:
        raise AcceptanceError(f"images response did not contain an image part: {data!r}") from exc


def extract_standalone_image(data: dict[str, Any], output: Path) -> dict[str, Any]:
    try:
        image = standalone_image_part(data)
        raw = base64.b64decode(image["b64_json"], validate=True)
    except (binascii.Error, KeyError, ValueError, TypeError) as exc:
        raise AcceptanceError(f"images response did not contain valid generated image data: {data!r}") from exc
    ensure_output_absent(output)
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(raw)
    png = png_metadata(output)
    if image.get("sha256") and image["sha256"] != png["sha256"]:
        raise AcceptanceError("images response SHA-256 does not match returned PNG")
    return {"response": data, "png": png}


def validate_standalone_reference_delivery(response: dict[str, Any], expected_sha256: str) -> dict[str, Any]:
    image = standalone_image_part(response)
    if image.get("reference_image_sent") is not True:
        raise AcceptanceError("images follow-up did not report reference_image_sent=true")
    if image.get("reference_image_sha256") != expected_sha256:
        raise AcceptanceError(
            f"images reference checksum mismatch: {image.get('reference_image_sha256')!r} != {expected_sha256!r}"
        )
    return {"reference_image_sent": True, "reference_image_sha256": expected_sha256}


def run_api_followup(base_url: str, kind: str, output_dir: Path, style: str, token: str, queue: GenerationQueue, report: dict[str, Any]) -> None:
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    first = output_dir / (kind + "-first.png")
    second = output_dir / (kind + "-followup.png")
    if kind == "chat-completions":
        path = "/v1/chat/completions"
        first_messages: Any = [{"role": "user", "content": PROMPTS["api"]}]
        def payload(messages: Any) -> dict[str, Any]:
            return {"model": "hollis-image", "messages": messages, "image_generation": {"style": style}}
    elif kind == "responses":
        path = "/v1/responses"
        first_messages = [{"role": "user", "content": PROMPTS["api"]}]
        def payload(messages: Any) -> dict[str, Any]:
            return {"model": "hollis-image", "input": messages, "image_generation": {"style": style}}
    else:
        raise ValueError(f"unsupported API kind {kind!r}")

    queue.before_generation(kind + "-first")
    first_status, first_response = api_request(base_url, path, payload(first_messages), token)
    queue.completed()
    if first_status != 200:
        raise AcceptanceError(f"{kind} first turn HTTP {first_status}: {first_response!r}")
    first_check = extract_api_image(kind, first_response, first)

    # Send the actual first PNG in the final user message.  The assistant
    # replay is intentionally text-only so the server sees one unambiguous
    # final-user reference and the harness proves bytes were transported.
    reference_url = inline_data_url(first)
    first_text = api_text_part(kind, first_response)
    if kind == "chat-completions":
        assistant = {"role": "assistant", "content": [{"type": "text", "text": first_text}]}
        second_messages = first_messages + [
            assistant,
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": PROMPTS["api_revision"]},
                    {"type": "image_url", "image_url": {"url": reference_url}},
                ],
            },
        ]
    else:
        assistant = {
            "role": "assistant",
            "type": "message",
            "status": "completed",
            "content": [{"type": "output_text", "text": first_text, "annotations": []}],
        }
        second_messages = first_messages + [
            assistant,
            {
                "role": "user",
                "content": [
                    {"type": "input_text", "text": PROMPTS["api_revision"]},
                    {"type": "input_image", "image_url": reference_url},
                ],
            },
        ]
    queue.before_generation(kind + "-followup")
    second_status, second_response = api_request(base_url, path, payload(second_messages), token)
    queue.completed()
    if second_status != 200:
        raise AcceptanceError(f"{kind} follow-up HTTP {second_status}: {second_response!r}")
    second_check = extract_api_image(kind, second_response, second)
    reference_check = validate_reference_delivery(kind, second_response, first_check["png"]["sha256"])
    third = output_dir / (kind + "-third.png")
    second_assistant = second_response["choices"][0]["message"] if kind == "chat-completions" else second_response["output"][0]
    # Retain the preceding final-user reference plus a newer assistant image.
    # The latest assistant must win when the final user sends text only.
    third_messages = second_messages + [second_assistant, {"role": "user", "content": "Keep the same fox and stone, but set the scene in a snowy forest under moonlight."}]
    queue.before_generation(kind + "-third")
    third_status, third_response = api_request(base_url, path, payload(third_messages), token)
    queue.completed()
    if third_status != 200:
        raise AcceptanceError(f"{kind} third turn HTTP {third_status}: {third_response!r}")
    third_check = extract_api_image(kind, third_response, third)
    validate_reference_delivery(kind, third_response, second_check["png"]["sha256"])
    report["api_" + kind.replace("-", "_")] = {
        "third": {"status": third_status, **third_check},
        "first": {"status": first_status, **first_check},
        "followup": {"status": second_status, **second_check},
        "reference_input": {"format": "final-user-inline-png", **reference_check},
    }


def run_api_reference_generations(base_url: str, output_dir: Path, style: str, token: str, queue: GenerationQueue, report: dict[str, Any]) -> None:
    """Exercise /v1/images/generations with the actual first PNG as a reference."""
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    first = output_dir / "images-generations-first.png"
    second = output_dir / "images-generations-reference.png"
    first_payload = {
        "model": "hollis-image",
        "prompt": PROMPTS["api"],
        "style": style,
        "n": 1,
        "response_format": "b64_json",
    }
    queue.before_generation("api-reference-generations-first")
    first_status, first_response = api_request(base_url, "/v1/images/generations", first_payload, token)
    queue.completed()
    if first_status != 200:
        raise AcceptanceError(f"images first turn HTTP {first_status}: {first_response!r}")
    first_check = extract_standalone_image(first_response, first)
    reference_url = inline_data_url(first)
    second_payload = {
        "model": "hollis-image",
        "prompt": PROMPTS["api_revision"],
        "style": style,
        "n": 1,
        "response_format": "b64_json",
        "reference_image": reference_url,
    }
    queue.before_generation("api-reference-generations-reference")
    second_status, second_response = api_request(base_url, "/v1/images/generations", second_payload, token)
    queue.completed()
    if second_status != 200:
        raise AcceptanceError(f"images reference turn HTTP {second_status}: {second_response!r}")
    second_check = extract_standalone_image(second_response, second)
    reference_check = validate_standalone_reference_delivery(second_response, first_check["png"]["sha256"])
    report["api_reference_generations"] = {
        "first": {"status": first_status, **first_check},
        "reference": {"status": second_status, **second_check},
        "reference_input": {"format": "reference_image-inline-png", **reference_check},
    }


def plan() -> dict[str, Any]:
    return {
        "schema_version": 1,
        "created_at": utc_now(),
        "settle_floor_seconds": MIN_SETTLE_SECONDS,
        "cases": [
            {"name": "interactive-first", "generations": 1, "purpose": "Run /image through a real PTY and verify output plus conversation ID."},
            {"name": "interactive-sequence", "generations": 3, "purpose": "Run three sequential /image turns, verify fresh -2/-3 outputs, and preserve the conversation."},
            {"name": "cli-followup", "generations": 2, "purpose": "Generate once through chat, then continue with a new output path."},
            {"name": "api-chat-completions", "generations": 3, "purpose": "Send the first PNG as a final-user image_url, then replay the latest assistant image on turn three."},
            {"name": "api-responses", "generations": 3, "purpose": "Send the first PNG as a final-user input_image, then replay the latest assistant image on turn three."},
            {"name": "api-reference-generations", "generations": 2, "purpose": "Send the first PNG as reference_image to /v1/images/generations."},
            {"name": "dynamic-reference", "generations": 2, "purpose": "Attach a generated PNG to a fresh CLI image request, then follow up with automatic reference reuse."},
        ],
        "deferred": [
            "The dynamic-reference case requires --reference-image pointing to a local PNG/JPEG; it tests chat --image-reference path/auto.",
            "Visual continuity is a human review requirement; valid PNG and successful transport do not prove subject preservation.",
            "ChatGPT-style Shortcuts behavior remains a separate bridge qualification and is not silently folded into these cases.",
        ],
    }


def run(args: argparse.Namespace) -> int:
    binary = Path(args.binary)
    state = Path(args.state_dir)
    output_dir = Path(args.output_dir)
    if not binary.is_absolute() or not binary.is_file():
        raise AcceptanceError("--binary must be an absolute path to a built Hollis binary")
    if not state.is_absolute() or not output_dir.is_absolute():
        raise AcceptanceError("--state-dir and --output-dir must be absolute private paths")
    state.mkdir(mode=0o700, parents=True, exist_ok=True)
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    generation_count = sum(CASE_GENERATIONS[case] for case in args.case)
    if generation_count > MAX_GENERATIONS:
        raise AcceptanceError(f"selected cases contain {generation_count} generations; maximum is {MAX_GENERATIONS}")
    report_path = Path(args.report) if args.report else output_dir / "acceptance-report.json"
    report: dict[str, Any] = {"schema_version": 1, "created_at": utc_now(), "mode": "live", "binary": str(binary), "cases": args.case, "results": [], "status": "running", "settle_seconds": args.settle_seconds}
    private_json(report_path, report)
    lock_path = Path(args.lock_path) if args.lock_path else Path(tempfile.gettempdir()) / LOCK_NAME
    with open(lock_path, "a+") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise AcceptanceError(f"live queue is busy: {lock_path}") from exc
        queue = GenerationQueue(args.settle_seconds, report)
        env = dict(os.environ, HOLLIS_STATE_DIR=str(state))
        del env  # run helpers construct this again; make the state intent visible here.
        try:
            for case in args.case:
                if case == "interactive-first":
                    run_interactive_first(binary, state, output_dir / "interactive.png", args.style, queue, report)
                elif case == "interactive-sequence":
                    run_interactive_sequence(binary, state, output_dir / "interactive-sequence.png", args.style, queue, report)
                elif case == "cli-followup":
                    run_cli_followup(binary, state, output_dir / "cli", args.style, queue, report)
                elif case in ("api-chat-completions", "api-responses"):
                    if not args.api_base_url:
                        raise AcceptanceError(f"{case} requires --api-base-url")
                    run_api_followup(args.api_base_url, "chat-completions" if case == "api-chat-completions" else "responses", output_dir / "api", args.style, os.environ.get("HOLLIS_API_TOKEN", ""), queue, report)
                elif case == "api-reference-generations":
                    if not args.api_base_url:
                        raise AcceptanceError(f"{case} requires --api-base-url")
                    run_api_reference_generations(args.api_base_url, output_dir / "api-reference-generations", args.style, os.environ.get("HOLLIS_API_TOKEN", ""), queue, report)
                elif case == "dynamic-reference":
                    if not args.reference_image:
                        report.setdefault("deferred", []).append("dynamic-reference was selected without --reference-image")
                    else:
                        run_cli_followup(binary, state, output_dir / "dynamic-reference", args.style, queue, report, [args.reference_image])
                else:
                    raise AcceptanceError(f"unknown case {case!r}")
                private_json(report_path, report)
            report["status"] = "completed"
        except BaseException as exc:
            report["status"] = "failed"
            report["error"] = str(exc)
            private_json(report_path, report)
            raise
        finally:
            private_json(report_path, report)
    print(report_path)
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    show_plan = sub.add_parser("plan", help="print the bounded plan; never starts Hollis")
    show_plan.add_argument("--json", action="store_true", help="emit JSON")
    live = sub.add_parser("run", help="run explicitly selected live cases")
    live.add_argument("--binary", required=True)
    live.add_argument("--state-dir", required=True)
    live.add_argument("--output-dir", required=True)
    live.add_argument("--report")
    live.add_argument("--api-base-url")
    live.add_argument("--style", default=DEFAULT_STYLE)
    live.add_argument("--settle-seconds", type=float, default=DEFAULT_SETTLE_SECONDS)
    live.add_argument("--lock-path")
    live.add_argument("--reference-image", help="absolute PNG/JPEG reference for the dynamic-reference case")
    live.add_argument("--case", action="append", required=True, choices=["interactive-first", "interactive-sequence", "cli-followup", "api-chat-completions", "api-responses", "api-reference-generations", "dynamic-reference"])
    args = parser.parse_args()
    os.umask(0o077)
    try:
        if args.command == "plan":
            value = plan()
            print(json.dumps(value, indent=2) if args.json else "\n".join(f"{item['name']}: {item['purpose']}" for item in value["cases"]))
            return 0
        return run(args)
    except (AcceptanceError, OSError, ValueError) as exc:
        print(f"acceptance suite stopped: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
