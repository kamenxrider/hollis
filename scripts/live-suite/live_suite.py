#!/usr/bin/env python3
"""Black-box suite for the installed hollis binary.

See scripts/live-suite/README.md for the method. Stdlib only.

    python3 scripts/live-suite/live_suite.py              # offline contract
    python3 scripts/live-suite/live_suite.py --live       # + Apple Intelligence
    python3 scripts/live-suite/live_suite.py --live --quality
"""

from __future__ import annotations

import argparse
import json
import os
import shlex
import shutil
import subprocess
import time
import uuid
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import asdict, dataclass, field
from typing import Any


PASS = "PASS"
FAIL = "FAIL"
OBSERVED = "OBSERVED"  # model behavior, not a CLI defect


@dataclass
class Invocation:
    cmd: str
    exit: int | None
    seconds: float
    stdout: str
    stderr: str
    timed_out: bool = False


@dataclass
class Case:
    name: str
    layer: str  # offline | live | quality
    status: str
    detail: str
    invocation: Invocation | None = None
    extra: dict[str, Any] = field(default_factory=dict)


def bin_path() -> str:
    override = os.environ.get("HOLLIS_BIN")
    if override:
        return override
    found = shutil.which("hollis")
    if not found:
        raise SystemExit("hollis not on PATH; set HOLLIS_BIN or rebuild into GOPATH/bin")
    return found


def run(
    args: list[str],
    stdin: str | None = None,
    timeout: float = 90,
) -> Invocation:
    t0 = time.perf_counter()
    try:
        p = subprocess.run(
            args,
            input=stdin,
            text=True,
            capture_output=True,
            timeout=timeout,
        )
        return Invocation(
            cmd=" ".join(shlex.quote(a) for a in args),
            exit=p.returncode,
            seconds=round(time.perf_counter() - t0, 3),
            stdout=p.stdout,
            stderr=p.stderr,
        )
    except subprocess.TimeoutExpired as e:
        stdout = e.stdout.decode() if isinstance(e.stdout, (bytes, bytearray)) else (e.stdout or "")
        stderr = e.stderr.decode() if isinstance(e.stderr, (bytes, bytearray)) else (e.stderr or "")
        return Invocation(
            cmd=" ".join(shlex.quote(a) for a in args),
            exit=None,
            seconds=round(time.perf_counter() - t0, 3),
            stdout=stdout,
            stderr=stderr,
            timed_out=True,
        )


def loads(inv: Invocation) -> Any:
    return json.loads(inv.stdout)


def token() -> str:
    # Short, unique, no spaces — easy to exact-match in a model reply.
    return "H" + uuid.uuid4().hex[:10].upper()


def looks_like_refusal(text: str) -> bool:
    t = text.lower()
    needles = (
        "i cannot",
        "i can't",
        "cannot respond",
        "cannot repeat",
        "please clarify",
        "please provide a new request",
    )
    return any(n in t for n in needles)


class Suite:
    def __init__(self, hollis: str) -> None:
        self.hollis = hollis
        self.cases: list[Case] = []
        self.created_chats: list[str] = []

    def record(self, case: Case) -> Case:
        self.cases.append(case)
        mark = {"PASS": ".", "FAIL": "F", "OBSERVED": "O"}[case.status]
        print(f"  [{mark}] {case.name}: {case.detail}", flush=True)
        return case

    def h(self, *args: str) -> list[str]:
        return [self.hollis, *args]

    # --- assertions ---------------------------------------------------------

    def expect_exit(self, name: str, layer: str, inv: Invocation, code: int, note: str = "") -> Case:
        if inv.timed_out:
            return self.record(Case(name, layer, FAIL, f"timed out after {inv.seconds}s", inv))
        if inv.exit != code:
            return self.record(
                Case(
                    name,
                    layer,
                    FAIL,
                    f"exit {inv.exit}, want {code}" + (f" ({note})" if note else ""),
                    inv,
                )
            )
        detail = note or f"exit {code}"
        return self.record(Case(name, layer, PASS, detail, inv))

    def expect_json(self, name: str, layer: str, inv: Invocation) -> Any | None:
        if inv.exit != 0:
            self.record(Case(name, layer, FAIL, f"exit {inv.exit}, want 0", inv))
            return None
        try:
            return loads(inv)
        except json.JSONDecodeError as e:
            self.record(Case(name, layer, FAIL, f"stdout is not JSON: {e}", inv))
            return None

    # --- offline ------------------------------------------------------------

    def offline(self) -> None:
        print("== offline contract", flush=True)

        inv = run(self.h("version"))
        if inv.exit == 0 and inv.stdout.startswith("hollis "):
            self.record(Case("version", "offline", PASS, inv.stdout.strip(), inv))
        else:
            self.record(Case("version", "offline", FAIL, f"exit {inv.exit}: {inv.stdout!r}", inv))

        inv = run(self.h("doctor", "--json"))
        data = self.expect_json("doctor-json", "offline", inv)
        if data is not None:
            bridges = data.get("bridges")
            models = [b.get("model") for b in bridges] if isinstance(bridges, list) else []
            empty = [b for b in bridges if not b] if isinstance(bridges, list) else ["missing"]
            if empty:
                self.record(
                    Case(
                        "doctor-json-bridges",
                        "offline",
                        FAIL,
                        f"empty/missing bridge objects: {bridges!r}",
                        inv,
                    )
                )
            elif models != ["cloud", "cloud-pro", "on-device", "chatgpt"]:
                self.record(
                    Case(
                        "doctor-json-bridges",
                        "offline",
                        FAIL,
                        f"models={models}",
                        inv,
                    )
                )
            else:
                self.record(
                    Case(
                        "doctor-json-bridges",
                        "offline",
                        PASS,
                        f"four bridges, installed={ [b.get('installed') for b in bridges] }",
                        inv,
                    )
                )
            # Runtime resolution must be visible: macos, resolved_ref,
            # source, status.
            resolution_ok = (
                bool(data.get("macos"))
                and all(
                    b.get("resolved_ref") and b.get("source") and b.get("status")
                    for b in (bridges or [])
                    if isinstance(b, dict)
                )
            )
            self.record(
                Case(
                    "doctor-json-resolution",
                    "offline",
                    PASS if resolution_ok else FAIL,
                    f"macos={data.get('macos')!r} resolved={[b.get('resolved_ref') for b in (bridges or [])]}",
                    inv,
                )
            )

        inv = run(self.h("models", "--json"))
        rows = self.expect_json("models-json", "offline", inv)
        if rows is not None:
            got = [r.get("model") for r in rows] if isinstance(rows, list) else None
            if got != ["auto", "cloud", "cloud-pro", "on-device", "chatgpt"]:
                self.record(Case("models-json-tiers", "offline", FAIL, f"got {got}", inv))
            else:
                self.record(Case("models-json-tiers", "offline", PASS, "auto + four tiers", inv))

        inv = run(self.h("agent-context"))
        ctx = self.expect_json("agent-context", "offline", inv)
        if ctx is not None:
            ok = ctx.get("schema_version") == "1" and ctx.get("cli", {}).get("name") == "hollis"
            self.record(
                Case(
                    "agent-context-shape",
                    "offline",
                    PASS if ok else FAIL,
                    f"schema_version={ctx.get('schema_version')}",
                    inv,
                )
            )

        self.expect_exit(
            "empty-prompt-arg",
            "offline",
            run(self.h("respond", "   ")),
            2,
            "empty arg rejected before spawn",
        )
        self.expect_exit(
            "empty-prompt-stdin",
            "offline",
            run(self.h("respond"), stdin=""),
            2,
            "empty stdin rejected before spawn",
        )
        self.expect_exit(
            "empty-chat-arg",
            "offline",
            run(self.h("chat", "")),
            2,
            "empty chat rejected",
        )
        before = self._chat_count()
        # Re-run empty chat and prove it did not insert a row.
        run(self.h("chat", ""))
        after = self._chat_count()
        if before is not None and after is not None:
            self.record(
                Case(
                    "empty-chat-no-orphan",
                    "offline",
                    PASS if after == before else FAIL,
                    f"chats before={before} after={after}",
                    None,
                )
            )

        self.expect_exit(
            "unknown-model",
            "offline",
            run(self.h("respond", "--model", "nope", "hello")),
            2,
        )
        self.expect_exit(
            "missing-chat",
            "offline",
            run(self.h("chats", "show", "does-not-exist")),
            3,
        )
        self.expect_exit(
            "delete-without-yes-agent",
            "offline",
            run(self.h("chats", "delete", "x", "--agent")),
            2,
        )

        inv = run(self.h("respond", "--model", "cloud", "--timeout", "1ms", "Reply with PONG"))
        if inv.exit != 7:
            self.record(Case("timeout-1ms", "offline", FAIL, f"exit {inv.exit}, want 7", inv))
        elif "exceeded 30s" in inv.stderr:
            self.record(
                Case(
                    "timeout-1ms",
                    "offline",
                    FAIL,
                    "message still cites the 30s default",
                    inv,
                )
            )
        elif "exceeded" not in inv.stderr:
            self.record(Case("timeout-1ms", "offline", FAIL, f"stderr={inv.stderr!r}", inv))
        else:
            self.record(Case("timeout-1ms", "offline", PASS, inv.stderr.split("\n", 1)[0], inv))

        inv = run(self.h("respond", "--help"))
        help_txt = inv.stdout + inv.stderr
        if "later phase" in help_txt:
            self.record(Case("respond-help", "offline", FAIL, "stale 'later phase' text still present", inv))
        elif "hollis chat" not in help_txt:
            self.record(Case("respond-help", "offline", FAIL, "does not point at hollis chat", inv))
        else:
            self.record(Case("respond-help", "offline", PASS, "points at hollis chat", inv))

        # chats search exit contract: 2 empty query, 3 no matches.
        self.expect_exit(
            "chats-search-empty",
            "offline",
            run(self.h("chats", "search", "   ")),
            2,
        )
        self.expect_exit(
            "chats-search-no-hits",
            "offline",
            run(self.h("chats", "search", "zzz-no-such-phrase-in-any-chat")),
            3,
        )

        # Bridge override roundtrip: a
        # fake config name must drop cloud-pro from the catalog, then the
        # override is cleared again. No Apple quota involved.
        before_cfg = run(self.h("config", "show", "--json"))
        before_bridges = (self.expect_json("config-show-before-bridges", "offline", before_cfg) or {}).get("bridges") or {}
        set_inv = run(self.h("config", "set", "bridge", "cloud-pro", "Fake Pro Bridge"))
        if set_inv.exit != 0:
            self.record(Case("config-set-bridge", "offline", FAIL, set_inv.stderr, set_inv))
        else:
            inv = run(self.h("models", "--json"))
            rows = self.expect_json("models-fake-pro", "offline", inv)
            if rows is not None:
                got = [r.get("model") for r in rows if isinstance(r, dict)]
                self.record(
                    Case(
                        "config-set-bridge-drops-pro",
                        "offline",
                        FAIL if "cloud-pro" in got else PASS,
                        f"models={got}",
                        inv,
                    )
                )
        clear = run(self.h("config", "set", "bridge", "cloud-pro", ""))
        if clear.exit != 0:
            self.record(Case("config-bridge-restored", "offline", FAIL, clear.stderr, clear))
        else:
            after_cfg = run(self.h("config", "show", "--json"))
            after_bridges = (self.expect_json("config-show-after-bridges", "offline", after_cfg) or {}).get("bridges") or {}
            self.record(
                Case(
                    "config-bridge-restored",
                    "offline",
                    PASS if after_bridges == before_bridges else FAIL,
                    f"bridges after={after_bridges!r} (before={before_bridges!r})",
                    after_cfg,
                )
            )

        inv = run(self.h("chats", "list"))
        header = inv.stdout.splitlines()[0] if inv.stdout else ""
        if inv.exit != 0:
            self.record(Case("chats-list-header", "offline", FAIL, f"exit {inv.exit}", inv))
        elif "MODEL" not in header:
            self.record(Case("chats-list-header", "offline", FAIL, f"header={header!r}", inv))
        else:
            rows = inv.stdout.splitlines()[1:]
            # If there is at least one chat, MODEL must appear as a cell, not
            # just the header (the original defect printed TITLE in that slot).
            if rows:
                known = ("cloud-pro", "on-device", "chatgpt", "cloud", "auto")
                hit = any(any(k in line for k in known) for line in rows)
                self.record(
                    Case(
                        "chats-list-model-column",
                        "offline",
                        PASS if hit else FAIL,
                        "MODEL cell present in rows" if hit else f"rows missing a model cell: {rows[0]!r}",
                        inv,
                    )
                )
            else:
                self.record(Case("chats-list-header", "offline", PASS, "MODEL in header (no rows)", inv))

    def _chat_count(self) -> int | None:
        inv = run(self.h("chats", "list", "--json"))
        if inv.exit != 0:
            return None
        try:
            rows = json.loads(inv.stdout)
        except json.JSONDecodeError:
            return None
        return len(rows) if isinstance(rows, list) else None

    # --- live ---------------------------------------------------------------

    def live(self, mutate_config: bool) -> None:
        print("== live transport", flush=True)
        tok = {m: token() for m in ("cloud", "cloud-pro", "chatgpt", "auto", "on-device")}

        for model in ("cloud", "cloud-pro", "chatgpt"):
            prompt = f"Reply with exactly these characters and nothing else: {tok[model]}"
            inv = run(self.h("respond", "--json", "--model", model, prompt))
            data = self.expect_json(f"exact-token-{model}", "live", inv)
            if data is None:
                continue
            got = data.get("response", "")
            if got == tok[model]:
                self.record(
                    Case(
                        f"exact-token-{model}-body",
                        "live",
                        PASS,
                        got,
                        inv,
                        extra={"token": tok[model]},
                    )
                )
            else:
                self.record(
                    Case(
                        f"exact-token-{model}-body",
                        "live",
                        FAIL,
                        f"want {tok[model]!r}, got {got!r}",
                        inv,
                    )
                )

        prompt = f"Reply with exactly these characters and nothing else: {tok['auto']}"
        inv = run(self.h("respond", "--json", prompt))
        data = self.expect_json("exact-token-auto", "live", inv)
        if data is not None:
            got = data.get("response", "")
            ok = got == tok["auto"] and data.get("model") == "auto"
            self.record(
                Case(
                    "exact-token-auto-body",
                    "live",
                    PASS if ok else FAIL,
                    f"model={data.get('model')} response={got!r}",
                    inv,
                )
            )

        prompt = f"Reply with exactly these characters and nothing else: {tok['on-device']}"
        inv = run(self.h("respond", "--json", "--model", "on-device", prompt))
        data = self.expect_json("exact-token-on-device", "live", inv)
        if data is not None:
            got = str(data.get("response", ""))
            if got == tok["on-device"]:
                self.record(Case("exact-token-on-device-body", "live", PASS, "echoed token", inv))
            elif looks_like_refusal(got):
                self.record(
                    Case(
                        "exact-token-on-device-body",
                        "live",
                        OBSERVED,
                        f"on-device refused token (model behavior): {got}",
                        inv,
                    )
                )
            else:
                self.record(
                    Case(
                        "exact-token-on-device-body",
                        "live",
                        FAIL,
                        f"neither echo nor known refusal: {got!r}",
                        inv,
                    )
                )

        uni = "café — naïve — 日本語 — Ω≈ç√ — 🎉"
        inv = run(
            self.h(
                "respond",
                "--json",
                "--model",
                "cloud",
                f"Reply with exactly this line and nothing else: {uni}",
            )
        )
        data = self.expect_json("unicode-cloud", "live", inv)
        if data is not None:
            got = data.get("response", "")
            self.record(
                Case(
                    "unicode-cloud-body",
                    "live",
                    PASS if got == uni else FAIL,
                    got if got == uni else f"got {got!r}",
                    inv,
                )
            )

        stdin_tok = token()
        inv = run(
            self.h("respond", "--model", "cloud", "--json"),
            stdin=f"Line1: ignore.\nLine2: Reply with exactly these characters and nothing else: {stdin_tok}\n",
        )
        data = self.expect_json("stdin-multiline", "live", inv)
        if data is not None:
            got = data.get("response", "")
            self.record(
                Case(
                    "stdin-multiline-body",
                    "live",
                    PASS if got == stdin_tok else FAIL,
                    got if got == stdin_tok else f"got {got!r}",
                    inv,
                )
            )

        pos_tok = token()
        inv = run(
            self.h(
                "respond",
                "--json",
                "model",
                "cloud-pro",
                f"Reply with exactly these characters and nothing else: {pos_tok}",
            )
        )
        data = self.expect_json("positional-cloud-pro", "live", inv)
        if data is not None:
            ok = data.get("model") == "cloud-pro" and data.get("response") == pos_tok
            self.record(
                Case(
                    "positional-cloud-pro-body",
                    "live",
                    PASS if ok else FAIL,
                    f"model={data.get('model')} response={data.get('response')!r}",
                    inv,
                )
            )

        agent_tok = token()
        inv = run(
            self.h(
                "respond",
                "--agent",
                "--model",
                "cloud",
                "--select",
                "response",
                f"Reply with exactly these characters and nothing else: {agent_tok}",
            )
        )
        data = self.expect_json("agent-select", "live", inv)
        if data is not None:
            got = (data.get("results") or {}).get("response") if isinstance(data, dict) else None
            ok = data.get("meta", {}).get("source") == "apple-intelligence" and got == agent_tok
            self.record(
                Case(
                    "agent-select-body",
                    "live",
                    PASS if ok else FAIL,
                    f"got {data!r}",
                    inv,
                )
            )

        # Stateless: two respond calls must not share memory.
        codeword = "Z" + uuid.uuid4().hex[:8].upper()
        run(
            self.h(
                "respond",
                "--json",
                "--model",
                "cloud",
                f"Remember this exact codeword: {codeword}. Reply only: ACK",
            )
        )
        inv = run(
            self.h(
                "respond",
                "--json",
                "--model",
                "cloud",
                "What exact codeword did I give you earlier? If you do not know, reply with exactly NONE",
            )
        )
        data = self.expect_json("stateless-respond", "live", inv)
        if data is not None:
            got = data.get("response", "")
            self.record(
                Case(
                    "stateless-respond-body",
                    "live",
                    PASS if got == "NONE" else FAIL,
                    got,
                    inv,
                )
            )

        self._live_chat("cloud", "KESTREL")
        self._live_chat("chatgpt", "HELIOS")
        self._live_ondevice_color()
        self._live_parallel()
        if mutate_config:
            self._live_config_roundtrip()

    def _live_chat(self, model: str, tag: str) -> None:
        codeword = f"{tag}-{uuid.uuid4().hex[:6].upper()}"
        inv = run(
            self.h(
                "chat",
                "--json",
                "--model",
                model,
                f"Remember this exact codeword for our conversation: {codeword}. Reply only: ACK",
            )
        )
        data = self.expect_json(f"chat-{model}-turn1", "live", inv)
        if data is None:
            return
        cid = data.get("conversation_id")
        if not cid:
            self.record(Case(f"chat-{model}-turn1-id", "live", FAIL, "no conversation_id", inv))
            return
        self.created_chats.append(cid)
        if data.get("response") != "ACK":
            self.record(
                Case(
                    f"chat-{model}-turn1-body",
                    "live",
                    FAIL,
                    f"want ACK, got {data.get('response')!r}",
                    inv,
                )
            )
            return
        self.record(Case(f"chat-{model}-turn1-body", "live", PASS, f"ACK id={cid}", inv))

        inv2 = run(
            self.h(
                "chat",
                "--json",
                "--continue",
                cid,
                "What exact codeword did I give you earlier? Reply with just the codeword.",
            )
        )
        data2 = self.expect_json(f"chat-{model}-turn2", "live", inv2)
        if data2 is None:
            return
        got = data2.get("response", "")
        if got == codeword:
            self.record(Case(f"chat-{model}-turn2-body", "live", PASS, got, inv2))
        elif model == "on-device" and looks_like_refusal(str(got)):
            self.record(
                Case(
                    f"chat-{model}-turn2-body",
                    "live",
                    OBSERVED,
                    f"on-device refused codeword echo: {got}",
                    inv2,
                )
            )
        else:
            self.record(
                Case(
                    f"chat-{model}-turn2-body",
                    "live",
                    FAIL,
                    f"want {codeword!r}, got {got!r}",
                    inv2,
                )
            )

    def _live_ondevice_color(self) -> None:
        inv = run(
            self.h(
                "chat",
                "--json",
                "--model",
                "on-device",
                "Remember my favorite color is teal. Reply only: ACK",
            )
        )
        data = self.expect_json("chat-on-device-color-t1", "live", inv)
        if data is None:
            return
        cid = data.get("conversation_id")
        if cid:
            self.created_chats.append(cid)
        if data.get("response") != "ACK":
            self.record(
                Case(
                    "chat-on-device-color-t1-body",
                    "live",
                    FAIL if not looks_like_refusal(str(data.get("response"))) else OBSERVED,
                    str(data.get("response")),
                    inv,
                )
            )
            return
        inv2 = run(
            self.h(
                "chat",
                "--json",
                "--continue",
                cid,
                "What is my favorite color? Reply with just the color word.",
            )
        )
        data2 = self.expect_json("chat-on-device-color-t2", "live", inv2)
        if data2 is None:
            return
        got = str(data2.get("response", "")).strip().lower()
        if got == "teal":
            self.record(Case("chat-on-device-color-t2-body", "live", PASS, "teal", inv2))
        elif looks_like_refusal(got):
            self.record(
                Case(
                    "chat-on-device-color-t2-body",
                    "live",
                    OBSERVED,
                    f"on-device refused color recall: {data2.get('response')}",
                    inv2,
                )
            )
        else:
            self.record(
                Case(
                    "chat-on-device-color-t2-body",
                    "live",
                    FAIL,
                    f"got {data2.get('response')!r}",
                    inv2,
                )
            )

    def _live_parallel(self) -> None:
        jobs = [
            ("p1", "cloud", token()),
            ("p2", "cloud", token()),
            ("p3", "cloud-pro", token()),
        ]
        t0 = time.perf_counter()
        futs = {}
        with ThreadPoolExecutor(max_workers=3) as ex:
            for name, model, tok in jobs:
                args = self.h(
                    "respond",
                    "--model",
                    model,
                    f"Reply with exactly these characters and nothing else: {tok}",
                )
                futs[ex.submit(run, args)] = (name, model, tok)
            results = [(futs[f], f.result()) for f in as_completed(futs)]
        wall = round(time.perf_counter() - t0, 3)
        ok = True
        details = []
        for (name, model, tok), inv in results:
            body = inv.stdout.strip()
            hit = body == tok
            details.append(f"{name}/{model}={body!r}")
            if inv.exit != 0 or not hit:
                ok = False
                self.record(
                    Case(
                        f"parallel-{name}",
                        "live",
                        FAIL,
                        f"exit {inv.exit} body={body!r} want={tok}",
                        inv,
                    )
                )
            else:
                self.record(Case(f"parallel-{name}", "live", PASS, tok, inv))
        self.record(
            Case(
                "parallel-wall",
                "live",
                PASS if ok else FAIL,
                f"wall={wall}s " + "; ".join(details),
                None,
            )
        )

    def _live_config_roundtrip(self) -> None:
        show = run(self.h("config", "show", "--json"))
        data = self.expect_json("config-show-before", "live", show)
        if data is None:
            return
        previous = data.get("default_model") or "auto"
        try:
            set_inv = run(self.h("config", "set", "model", "on-device"))
            if set_inv.exit != 0:
                self.record(Case("config-set", "live", FAIL, set_inv.stderr, set_inv))
                return
            inv = run(self.h("respond", "--json", "Reply with exactly the word PING"))
            body = self.expect_json("config-default-on-device", "live", inv)
            if body is not None:
                if body.get("model") != "on-device":
                    self.record(
                        Case(
                            "config-default-on-device-model",
                            "live",
                            FAIL,
                            f"model={body.get('model')}",
                            inv,
                        )
                    )
                else:
                    got = str(body.get("response", ""))
                    status = PASS if got == "PING" else OBSERVED if looks_like_refusal(got) else FAIL
                    self.record(
                        Case(
                            "config-default-on-device-body",
                            "live",
                            status,
                            got,
                            inv,
                        )
                    )
            flag = run(
                self.h(
                    "respond",
                    "--json",
                    "--model",
                    "cloud",
                    f"Reply with exactly these characters and nothing else: {token()}",
                )
            )
            fdata = self.expect_json("config-flag-overrides", "live", flag)
            if fdata is not None:
                self.record(
                    Case(
                        "config-flag-overrides-model",
                        "live",
                        PASS if fdata.get("model") == "cloud" else FAIL,
                        f"model={fdata.get('model')}",
                        flag,
                    )
                )
        finally:
            rest = run(self.h("config", "set", "model", str(previous)))
            self.record(
                Case(
                    "config-restored",
                    "live",
                    PASS if rest.exit == 0 else FAIL,
                    f"restored {previous}",
                    rest,
                )
            )

    def quality(self) -> None:
        print("== quality (observational)", flush=True)
        prompt = (
            "In 3-5 sentences, explain why Go's defer runs LIFO, then give one "
            "concrete pitfall with a one-line code sketch. Start your reply with "
            "the exact word DEFER."
        )
        for model in ("cloud", "cloud-pro", "on-device", "chatgpt"):
            inv = run(self.h("respond", "--json", "--model", model, prompt), timeout=120)
            data = self.expect_json(f"quality-{model}", "quality", inv)
            if data is None:
                continue
            text = str(data.get("response", ""))
            starts = text.startswith("DEFER")
            self.record(
                Case(
                    f"quality-{model}-body",
                    "quality",
                    OBSERVED,
                    text if len(text) < 800 else text[:800] + "…",
                    inv,
                    extra={"starts_with_DEFER": starts, "chars": len(text)},
                )
            )

    def cleanup(self) -> None:
        for cid in self.created_chats:
            inv = run(self.h("chats", "delete", cid, "--yes"))
            status = PASS if inv.exit == 0 else FAIL
            self.record(Case(f"cleanup-delete-{cid[:8]}", "live", status, f"exit {inv.exit}", inv))


def summarize(cases: list[Case]) -> int:
    counts = {PASS: 0, FAIL: 0, OBSERVED: 0}
    for c in cases:
        counts[c.status] += 1
    print()
    print(f"{counts[PASS]} pass  {counts[FAIL]} fail  {counts[OBSERVED]} observed")
    fails = [c for c in cases if c.status == FAIL]
    if fails:
        print("failures:")
        for c in fails:
            print(f"  - {c.name}: {c.detail}")
    return 1 if fails else 0


def dump_json(path: str, cases: list[Case]) -> None:
    payload = []
    for c in cases:
        row = {
            "name": c.name,
            "layer": c.layer,
            "status": c.status,
            "detail": c.detail,
            "extra": c.extra,
        }
        if c.invocation is not None:
            row["invocation"] = asdict(c.invocation)
        payload.append(row)
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, indent=2, ensure_ascii=False)
        fh.write("\n")
    print(f"wrote {path}")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--live", action="store_true", help="hit Apple Intelligence (spends quota)")
    ap.add_argument("--quality", action="store_true", help="also run observational quality prompts (implies --live)")
    ap.add_argument(
        "--mutate-config",
        action="store_true",
        help="exercise config set/restore (always restores the previous default)",
    )
    ap.add_argument(
        "--json-out",
        default="",
        help="write the full case log as JSON (default: none)",
    )
    args = ap.parse_args()
    live = args.live or args.quality

    suite = Suite(bin_path())
    print(f"binary: {suite.hollis}", flush=True)
    try:
        suite.offline()
        if live:
            suite.live(mutate_config=args.mutate_config)
        if args.quality:
            suite.quality()
    finally:
        suite.cleanup()

    if args.json_out:
        dump_json(args.json_out, suite.cases)
    return summarize(suite.cases)


if __name__ == "__main__":
    raise SystemExit(main())
