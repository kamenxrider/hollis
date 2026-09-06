#!/usr/bin/env python3
"""Run one explicitly requested host demonstration turn and retain its raw events.

Developer harness only. Run serially after other live tests. Existing host
authentication is used; the plugin itself does not depend on Python.
"""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess
import time


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--workspace", type=Path, required=True)
    p.add_argument("--kit", type=Path, required=True)
    p.add_argument("--name", required=True)
    p.add_argument("--prompt-file", type=Path, required=True)
    p.add_argument("--resume")
    p.add_argument("--host", choices=["claude", "codex"], default="claude")
    args = p.parse_args()
    if not re.fullmatch(r"[a-z0-9-]+", args.name):
        raise SystemExit("Use a simple lowercase test name")
    work = args.workspace.resolve()
    events = work / f"{args.name}.events.jsonl"
    receipt = work / f"{args.name}.receipt.json"
    if events.exists() or receipt.exists():
        raise SystemExit("This turn already has a record. Inspect it; do not repeat uncertain calls automatically.")
    os.umask(0o077)
    command = ["claude", "-p", "--model", "sonnet", "--effort", "high", "--output-format", "stream-json", "--verbose",
               "--tools", "Read,Write,Bash,Skill", "--allowedTools", "Read", "Write", "Bash", "Skill",
               "--setting-sources", "project", "--settings", str(work / "host-settings.json"),
               "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--plugin-dir", str(args.kit.resolve())]
    if args.host == "claude":
        if args.resume:
            command += ["--resume", args.resume]
        command += [args.prompt_file.read_text()]
    else:
        if args.resume:
            raise SystemExit("This Codex test uses a fresh ephemeral conversation")
        command = ["codex", "exec", "--model", "gpt-5.6-sol", "-c", 'model_reasoning_effort="low"',
                   "--approve-for-me", "--json", "--ephemeral", "--cd", str(work),
                   "--output-last-message", str(work / f"{args.name}.final.md"), args.prompt_file.read_text()]
    env = dict(os.environ, GSTACK_HOME=str(work / "gstack-state"), GSTACK_SESSION_KIND="spawned")
    record = {"host": "Claude Code" if args.host == "claude" else "Codex CLI", "model": "sonnet" if args.host == "claude" else "gpt-5.6-sol", "name": args.name, "started_at": datetime.now(timezone.utc).isoformat(), "exit_code": None,
              "disclosure": "Scripted fictional fixture. The Claude gstack turn uses spawned-mode recommendations; the Codex turn uses ordinary automatic approval review. No video recording is claimed."}
    receipt.write_text(json.dumps(record, indent=2)+"\n")
    start = time.monotonic()
    with events.open("w") as stream, (work / f"{args.name}.stderr.txt").open("w") as errors:
        result = subprocess.run(command, cwd=work, env=env, stdout=stream, stderr=errors)
    record.update(exit_code=result.returncode, elapsed_seconds=round(time.monotonic()-start, 3), ended_at=datetime.now(timezone.utc).isoformat())
    for line in events.read_text().splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get("type") == "result":
            record["session_id"] = event.get("session_id")
            record["permission_denials"] = event.get("permission_denials", [])
            record["result"] = event.get("result")
            record["is_error"] = event.get("is_error", False)
        if event.get("type") == "thread.started":
            record["session_id"] = event.get("thread_id")
        if event.get("type") == "item.completed" and event.get("item", {}).get("type") == "agent_message":
            record["result"] = event["item"].get("text")
    receipt.write_text(json.dumps(record, indent=2)+"\n")
    print(json.dumps({k: record.get(k) for k in ["name", "exit_code", "elapsed_seconds", "session_id", "is_error"]}))
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
