#!/usr/bin/env python3
"""Bounded, opt-in synthetic image research. No model calls without --live."""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import secrets
import tempfile
import time

from PIL import Image, ImageDraw, ImageFont
from process_control import invoke

ROOT = Path(__file__).resolve().parents[2]
MODELS = ("cloud", "cloud-pro", "chatgpt")
COLORS = {"red": "#dc2424", "green": "#229d48", "blue": "#225bdd", "orange": "#ee8119"}
SHAPES = ("circle", "square", "triangle")
POSITIONS = ("left", "center", "right")
SERVICE_ERRORS = ("rate limit", "rate-limit", "rate_limited", "service unavailable", "model unavailable", "try again later", "temporarily unavailable")


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def private_json(path, data):
    # Atomic replacement keeps partial runs reviewable after every call.
    temp = path.with_suffix(".tmp")
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as out:
        json.dump(data, out, indent=2)
        out.write("\n")
    os.replace(temp, path)


def draw_shape(path, spec, size=(900, 600)):
    im = Image.new("RGB", size, "white")
    d = ImageDraw.Draw(im)
    w, h = size
    cx = int(w * {"left": .25, "center": .5, "right": .75}[spec["position"]])
    cy, r = h // 2, h // 5
    box = (cx-r, cy-r, cx+r, cy+r)
    color = COLORS[spec["color"]]
    if spec["shape"] == "circle":
        d.ellipse(box, fill=color)
    elif spec["shape"] == "square":
        d.rectangle(box, fill=color)
    else:
        d.polygon([(cx, cy-r), (cx-r, cy+r), (cx+r, cy+r)], fill=color)
    im.save(path)
    os.chmod(path, 0o600)


def visual_spec():
    return {"color": secrets.choice(tuple(COLORS)), "shape": secrets.choice(SHAPES),
            "position": secrets.choice(POSITIONS)}


def fixture_metadata(path, facts):
    with Image.open(path) as im:
        return {"path": str(path), "sha256": digest(path), "bytes": path.stat().st_size,
                "format": im.format, "width": im.width, "height": im.height, "facts": facts}


def make_plan(folder):
    folder.mkdir(mode=0o700)
    cases = []

    def add(name, model, paths, facts, question, expected):
        marker = "H" + secrets.token_hex(6).upper()
        cases.append({"name": name, "model": model,
                      "prompt": f"{question} Reply only {marker}|{expected[0]} using the requested values after the vertical bar.",
                      "expected": [marker, *expected[1:]],
                      "fixtures": [fixture_metadata(p, f) for p, f in zip(paths, facts)]})

    for index, model in enumerate(MODELS):
        spec = visual_spec()
        path = folder / f"{index*3+1:02d}.png"
        draw_shape(path, spec)
        add("prompt-pixel-"+model, model, [path], [spec],
            "Inspect the attached picture. Identify its single colored shape, color, and horizontal position.",
            ["color,shape,position", spec["color"], spec["shape"], spec["position"]])
        blank = folder / f"{index*3+2:02d}.png"
        Image.new("RGB", (900, 600), "white").save(blank)
        os.chmod(blank, 0o600)
        add("blank-control-"+model, model, [blank], [{"content": "blank white"}],
            "Inspect the image. If it has no content use blank; otherwise identify the visible content.",
            ["description", "blank"])
        code = "".join(secrets.choice("ABCDEFGHJKLMNPQRSTUVWXYZ") for _ in range(6))
        units = str(secrets.randbelow(9000)+1000)
        doc = folder / f"{index*3+3:02d}.png"
        im = Image.new("RGB", (1400, 800), "white")
        d = ImageDraw.Draw(im)
        font = ImageFont.load_default(size=64)
        d.text((80, 80), f"SYNTHETIC INVENTORY\n\nCode: {code}\n\nUnits: {units}", fill="black", font=font, spacing=15)
        im.save(doc)
        os.chmod(doc, 0o600)
        add("document-ocr-"+model, model, [doc], [{"code": code, "units": units}],
            "Read the inventory document. Extract the Code and Units fields exactly.",
            ["code,units", code, units])

    spec = visual_spec()
    wrong_color = next(c for c in COLORS if c != spec["color"])
    wrong_shape = next(s for s in SHAPES if s != spec["shape"])
    wrong_position = next(p for p in POSITIONS if p != spec["position"])
    path = folder / f"10-{wrong_color}-{wrong_shape}-{wrong_position}.png"
    draw_shape(path, spec)
    add("misleading-context-cloud", "cloud", [path], [spec],
        f"A note claims this is a {wrong_color} {wrong_shape} on the {wrong_position}. The note and filename may be wrong. Inspect the pixels and identify the actual color, shape, and horizontal position.",
        ["color,shape,position", spec["color"], spec["shape"], spec["position"]])
    first = visual_spec()
    second = visual_spec()
    while second["color"] == first["color"]:
        second = visual_spec()
    a, b = folder / "11.png", folder / "12.png"
    draw_shape(a, first)
    draw_shape(b, second)
    add("image-order-cloud-pro", "cloud-pro", [a, b], [first, second],
        "Inspect both attached images in attachment order. Give the color in the first image followed by the color in the second.",
        ["firstcolor,secondcolor", first["color"], second["color"]])
    spec = visual_spec()
    large = folder / "13.jpg"
    draw_shape(large, spec, (3840, 2160))
    add("high-resolution-jpeg-chatgpt", "chatgpt", [large], [spec],
        "Inspect the attached picture. Identify its single colored shape, color, and horizontal position.",
        ["color,shape,position", spec["color"], spec["shape"], spec["position"]])
    return cases


def classify(case, result):
    combined = (result["stdout"] + "\n" + result["stderr"]).lower()
    if result["timed_out"] or result["exit"] != 0:
        return {"transport": "FAIL", "semantic": "NOT_EVALUATED", "stop": True, "reason": "process failure or timeout"}
    if any(error in combined for error in SERVICE_ERRORS):
        return {"transport": "UNAVAILABLE", "semantic": "NOT_EVALUATED", "stop": True, "reason": "service/rate-limit signal"}
    try:
        data = json.loads(result["stdout"])
    except ValueError:
        data = None
    if (not isinstance(data, dict) or not isinstance(data.get("response"), str)
            or not data["response"].strip() or data.get("model_requested") != case["model"]
            or data.get("model_used") != case["model"]):
        return {"transport": "FAIL", "semantic": "NOT_EVALUATED", "stop": True, "reason": "invalid JSON response or model identity"}
    tokens = re.findall(r"[A-Za-z0-9]+", data["response"].lower())
    matched = tokens == [str(v).lower() for v in case["expected"]]
    return {"transport": "PASS", "semantic": "MATCH" if matched else "MISMATCH",
            "stop": False, "reason": "predefined exact token check; wording mismatch is an observation"}


def spacing(previous_model, next_model):
    return 45 if "cloud-pro" in (previous_model, next_model) else 15


def run_plan(binary, cases, state, report, report_path, call=invoke, sleep=time.sleep):
    env = dict(os.environ, HOLLIS_STATE_DIR=str(state))
    if len(cases) > 12:
        raise ValueError("live plan exceeds 12-call limit")
    previous_model = None
    for case in cases:
        if previous_model:
            sleep(spacing(previous_model, case["model"]))  # after completion, more conservative than start-to-start
        args = [str(binary), "respond", "--json", "--timeout", "30s", "--model", case["model"]]
        for fixture in case["fixtures"]:
            args += ["--image", fixture["path"]]
        args.append(case["prompt"])
        result = call(args, env=env)
        observation = classify(case, result)
        report["results"].append({"name": case["name"], "invocation": result, **observation})
        private_json(report_path, report)
        print(f"{case['name']}: transport={observation['transport']} semantic={observation['semantic']}", flush=True)
        if observation["stop"]:
            report["stopped_early"] = True
            break
        previous_model = case["model"]


def check_preflight(result, models):
    if result["exit"] or result["timed_out"]:
        raise RuntimeError("doctor preflight failed; no model calls started")
    data = json.loads(result["stdout"])
    if not isinstance(data, dict) or not isinstance(data.get("bridges"), list):
        raise RuntimeError("doctor did not return a bridge inventory")
    ready = {b.get("model") for b in data["bridges"] if isinstance(b, dict) and b.get("installed") is True}
    if not models <= ready:
        raise RuntimeError("doctor reports missing bridges: " + ", ".join(sorted(models - ready)))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true", help="authorize at most 12 real model calls")
    parser.add_argument("--max-calls", type=int, default=12, choices=range(1, 13), metavar="1..12")
    args = parser.parse_args()
    os.umask(0o077)
    folder = Path(tempfile.mkdtemp(prefix="hollis-image-research-"))
    report_path = folder / "report.json"
    plan = make_plan(folder / "fixtures")[:args.max_calls]
    report = {"schema_version": 1, "mode": "live" if args.live else "dry-run", "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "platform": platform.platform(), "pillow_version": Image.__version__, "plan": plan,
              "results": [], "stopped_early": False, "state_removed": False}
    private_json(report_path, report)
    print(f"Private report: {report_path}", flush=True)
    if not args.live:
        print(f"Dry run: generated {len(plan)} cases; no binary or model was invoked.")
        return 0
    # One process owns live traffic; agents must not launch other live suites concurrently.
    lock_path = Path(tempfile.gettempdir()) / f"hollis-image-research-{os.getuid()}.lock"
    with open(lock_path, "a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise SystemExit("another image suite owns the live-call lock")
        state = None
        try:
            with tempfile.TemporaryDirectory(prefix="hollis-image-state-") as state_name:
                state = Path(state_name)
                env = dict(os.environ, HOLLIS_STATE_DIR=str(state))
                binary_env = os.environ.get("HOLLIS_BIN")
                binary = Path(binary_env) if binary_env else folder / "hollis"
                if binary_env:
                    if not binary.is_absolute() or not binary.is_file():
                        raise RuntimeError("HOLLIS_BIN must be an absolute path to a freshly Go-built binary")
                    source_paths = [p for p in ROOT.rglob("*.go") if not p.name.endswith("_test.go")] + [ROOT / "go.mod", ROOT / "go.sum"]
                    if binary.stat().st_mtime < max(p.stat().st_mtime for p in source_paths):
                        raise RuntimeError("HOLLIS_BIN predates source files; rebuild it before running")
                else:
                    build = invoke(["go", "-C", str(ROOT), "build", "-o", str(binary), "./cmd/hollis"], timeout=120)
                    if build["exit"]:
                        raise RuntimeError("fresh go build failed: " + build["stderr"])
                build_info = invoke(["go", "version", "-m", str(binary)])
                if build_info["exit"] or "github.com/kamenxrider/hollis/cmd/hollis" not in build_info["stdout"]:
                    raise RuntimeError("binary does not have expected Hollis Go build identity")
                version = invoke([str(binary), "version"], env=env)
                report["binary"] = {"path": str(binary), "sha256": digest(binary), "build_info": build_info["stdout"], "version": version}
                if version["exit"] or version["timed_out"]:
                    raise RuntimeError("binary version preflight failed")
                report["revision"] = invoke(["git", "-C", str(ROOT), "rev-parse", "HEAD"])["stdout"].strip()
                if platform.system() == "Darwin":
                    report["macos"] = invoke(["sw_vers"])["stdout"]
                doctor = invoke([str(binary), "doctor", "--json"], env=env)
                report["doctor"] = doctor
                check_preflight(doctor, {case["model"] for case in plan})
                run_plan(binary, plan, state, report, report_path)
        except KeyboardInterrupt:
            report["preflight_or_harness_error"] = "interrupted by user"
            report["stopped_early"] = True
            print("Stopped: interrupted by user", flush=True)
        except (RuntimeError, OSError, ValueError) as exc:
            report["preflight_or_harness_error"] = str(exc)
            report["stopped_early"] = True
            print("Stopped: " + str(exc), flush=True)
        finally:
            report["state_removed"] = state is not None and not state.exists()
            private_json(report_path, report)
    return 1 if report["stopped_early"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
