#!/usr/bin/env python3
"""Explicit, serial Apple integration test. Not run by CI; no automatic retries."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import struct
import subprocess
import time
import zlib


def fixture_png(path):
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    rows = b"".join(b"\x00" + (b"\xff\x00\x00" * 32 + b"\x00\x00\xff" * 32) for _ in range(32))
    path.write_bytes(b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 64, 32, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b""))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--live", action="store_true", required=True, help="Explicitly authorize Apple model calls")
    p.add_argument("--kit", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--phase", choices=["text", "vision", "generation", "reference", "batch"], required=True)
    args = p.parse_args()
    os.umask(0o077)
    out = args.output.resolve(); out.mkdir(parents=True, exist_ok=True)
    runner = args.kit.resolve() / "scripts/run.sh"
    active = out / ".live-lock"
    try:
        active.mkdir()
    except FileExistsError:
        raise SystemExit("Another suite may be active; inspect before removing its lock.")
    (active / "pid").write_text(str(os.getpid()))

    def call(name, argv, live=True):
        receipt = out / f"{name}.json"
        if receipt.exists():
            old = json.loads(receipt.read_text())
            if old.get("exit_code") == 0:
                return old
            raise RuntimeError(f"{name} already has a failed/uncertain receipt; investigate, do not retry automatically")
        start = datetime.now(timezone.utc).isoformat()
        # Journal before dispatch so a killed harness cannot silently repeat inference.
        record = {"name": name, "started_at": start, "arguments": argv, "exit_code": None, "live": live}
        receipt.write_text(json.dumps(record, indent=2) + "\n")
        before = time.monotonic()
        result = subprocess.run(["/bin/bash", str(runner), *argv], text=True, capture_output=True)
        record.update(ended_at=datetime.now(timezone.utc).isoformat(), elapsed_seconds=round(time.monotonic()-before, 3), exit_code=result.returncode, stdout=result.stdout, stderr=result.stderr)
        receipt.write_text(json.dumps(record, indent=2) + "\n")
        print(json.dumps({k: record[k] for k in ["name", "exit_code", "elapsed_seconds"]}), flush=True)
        if result.returncode:
            print((result.stderr or result.stdout)[-1200:], flush=True)
            raise RuntimeError(f"Stopped at {name}; investigate its receipt before any retry")
        return record

    try:
        if args.phase == "text":
            for model in ["cloud", "cloud-pro", "on-device", "chatgpt"]:
                call(f"text-{model}", ["respond", "--model", model, "--agent", "--timeout", "120s", "A repair cafe has 12 appointments of 20 minutes and two repairers working for two hours each. Calculate total available repairer-minutes and appointment-minutes. Answer in two short sentences."])
        elif args.phase == "vision":
            image = out / "red-blue.png"; fixture_png(image)
            for model in ["cloud", "cloud-pro", "chatgpt"]:
                call(f"vision-{model}", ["respond", "--model", model, "--image", str(image), "--agent", "--timeout", "120s", "Name the solid color on the left and on the right. Answer in one short sentence."])
        elif args.phase == "generation":
            prompts = [
                "A welcoming neighbourhood repair cafe, a small red toolbox beside a blue bicycle and a potted fern, warm morning light, clear simple composition, no lettering.",
                "A tiny lighthouse on a mossy island, calm turquoise water, two white clouds and a yellow sailboat, clear simple composition, no lettering.",
            ]
            for style in ["animation", "illustration", "sketch", "genmoji", "any"]:
                for i, prompt in enumerate(prompts, 1):
                    call(f"image-{style}-{i}", ["image", "generate", prompt, "--style", style, "--output", str(out / f"image-{style}-{i}.png"), "--timeout", "120s", "--agent"])
        elif args.phase == "reference":
            reference = out / "image-illustration-1.png"
            if not reference.is_file():
                raise RuntimeError("Run generation first; reference output is missing")
            call("image-reference-followup", ["image", "generate", "Use this repair cafe illustration as a reference. Keep the red toolbox and blue bicycle, add a small yellow watering can beside the fern. No lettering.", "--style", "illustration", "--reference-image", str(reference), "--fit", "pad", "--size", "1200x800", "--output", str(out / "image-reference-followup.png"), "--timeout", "120s", "--agent"])
        else:
            inputs = out / "batch-input"; inputs.mkdir(exist_ok=True)
            (inputs / "one.md").write_text("Repair cafe: bring one broken small household item on Saturday at 10am.\n")
            (inputs / "two.txt").write_text("Volunteers: arrive at 9:30am, label your tools, and stay until noon.\n")
            prompt = out / "batch-instruction.txt"; prompt.write_text("Summarize this note in one sentence, keeping its time.\n")
            job = out / "batch-job.json"
            call("batch-plan", ["batch", "plan", "--model", "cloud", "--input-dir", str(inputs), "--output-dir", str(out / "batch-results"), "--prompt-file", str(prompt), "--job", str(job), "--agent"], live=False)
            call("batch-first-budget", ["batch", "run", "--job", str(job), "--max-calls", "1", "--agent"])
            call("batch-resume", ["batch", "resume", "--job", str(job), "--max-calls", "1", "--agent"])
    finally:
        (active / "pid").unlink()
        active.rmdir()


if __name__ == "__main__":
    main()
