# Shortcuts Cloud Gateway — Session Recap & Stack Decision

> **Date:** 2026-09-01
> **Status:** Design + validation complete. Implementation not started.
> **Name:** the binary is **`hollis`** (decided 2026-09-01 — clean across Homebrew-core, crates.io, PyPI, npm, and RubyGems). The folder was renamed from the `shortcuts-pcc-tool` placeholder.
> **Canonical plan:** `APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md` (in this folder)

---

## 1. What This Tool Is

A local, CLI-first gateway that exposes Apple Intelligence **Cloud** and **Cloud Pro** through macOS Shortcuts, with persistent local chat and an optional OpenAI-compatible HTTP endpoint.

- Gives you Apple Intelligence Cloud and Cloud Pro from the terminal: prompt in, plain text out, ~1s.
- Reads prompts from args or stdin, so shell pipelines and agents can drive it.
- Keeps persistent chats in local SQLite — because Apple's runs are stateless, it replays the stored transcript each turn to create continuity (proven to work).
- Optionally serves a local OpenAI-compatible `/v1/chat/completions` endpoint so existing local apps can point at Apple Intelligence.
- Treats Shortcuts purely as transport — one bridge shortcut invoked via `/usr/bin/shortcuts` — so the backend is swappable if Apple ships a real API.
- Ships a first-class `doctor`, honest errors, and never fabricates token counts, streaming, or model IDs.
- Local-only by default; nothing leaves the machine except the model call Apple already makes.

**Origin.** On macOS 27 beta 7 (`26A5421a`), Apple removed PCC from the public `/usr/bin/fm` CLI/server surface, but Private Cloud Compute remains reachable through the Foundation Models framework and — critically — through the Shortcuts **Use Model** action. The gateway wraps that working path.

---

## 2. What We Did

### 2.1 Plan review and doc verification

- Reviewed `APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md` (37 sections: architecture, bridge design, SQLite schema, persistence experiments, HTTP surface, phases).
- Verified the plan's §4 claims against the live Apple doc (`apd455c82f02`, "Run shortcuts from the command line"): `shortcuts run/list/view/sign`, exit 0/1, piped text semantics, `-i`, `-o`, `--output-type`, and Apple's warning that a shortcut asking for input pauses the CLI.
- Flagged gaps: `--output-type` missing from the plan; "Cloud"/"Cloud Pro" absent from public docs (beta-only labels); local doc copy did not contain the CLI page content; stale workspace path.

### 2.2 Diagnosing "works in the app, silent in the terminal"

The user's first shortcuts (`PCC Test`, `PCC Test Pro`) ran in the app but printed nothing in the terminal. We decoded the actual shortcut definitions from `~/Library/Shortcuts/Shortcuts.sqlite` (`ZSHORTCUTACTIONS.ZDATA`, binary plist, read from a `/tmp` copy) and found them **already correct** (`askllm` → `output`, Response wired). My initial "add Stop and Output" hypothesis was wrong.

The real cause, established by measurement on `PCC Test Pro`:

| Form | Result |
| --- | --- |
| `-o <file>` | full output, exit 0 |
| `-o -` redirected | full output, exit 0 |
| bare run, redirected | full output, exit 0 |
| **any form, real TTY** (`script(1)`) | **0 bytes, exit 0** |
| `-o /dev/stdout` | **SIGABRT, exit 134** |

- stdout is **silently suppressed on a TTY**; exit 0 + empty is the normal signature of success.
- **Default output is RTF**, not text; `--output-type public.plain-text` is required for clean UTF-8.
- Output has **no trailing newline**.

None of this is documented by Apple.

### 2.3 Bridge engineering

`scripts/make-bridge.py` generates real bridge shortcuts — the decisive change is binding `WFLLMPrompt` to `ExtensionInput` (Shortcut Input) via a `WFTextTokenString` with a `\uFFFC` attachment, instead of a hardcoded literal. Verified findings:

- **A hand-built plist is accepted by `shortcuts sign`** (exit 0; 1.2K → 27K). Import needs one GUI confirmation per shortcut.
- Installed bridges:
  - `AFM Bridge - Cloud.signed` — `BD8CDC56-7CB8-418D-9B02-9D33AB911BF0` — `WFLLMModel "Apple Intelligence"`
  - `AFM Bridge - Cloud Pro.signed` — `DBB6E472-CBC6-4421-8D32-9D4543D5CDE6` — `WFLLMModel "Apple Intelligence Pro"`
- Verified Shortcuts-layer mapping: UI **Cloud** = `"Apple Intelligence"`, UI **Cloud Pro** = `"Apple Intelligence Pro"`, action = `is.workflow.actions.askllm`, output = `is.workflow.actions.output`. These are Shortcuts-layer identifiers, not backend model IDs.
- The original `PCC Test` / `PCC Test Pro` shortcuts could not serve as bridges: `ZHASSHORTCUTINPUTVARIABLES = 0`, empty input classes, literal prompt.

### 2.4 Test suite — the architectural gate **passed**

All results executed directly; full transcripts in `results/transport-and-persistence-2026-09-01.md`.

| Test | Result | Evidence |
| --- | --- | --- |
| stdin → Use Model | PASS | `SHORTCUT_IO_OK`, exact |
| Multi-line stdin | PASS | 17-line transcript intact |
| Native cross-run memory | **NONE** | second run replied `NONE` — runs are stateless |
| Transcript replay (§15 Test B) | PASS | codeword `VANTA-ORBIT-7319` recovered |
| 10-turn replay + correction | PASS | early/middle/late facts; correction won |
| Replay on Cloud (non-Pro) | PASS | not Pro-only |
| Unicode + JSON round-trip | PASS | byte-exact (emoji, CJK, escaped quotes) |
| 4 parallel invocations | PASS | no interference, no GUI prompts, 1.92s wall |
| 1124-word output | PASS | no truncation |
| Empty input | **HANG** | never returns; caller must prevent |

The decisive pair: separate runs returned `NONE` (no native session) while replay returned the codeword. That combination is exactly what the plan's architecture depends on — **persistence must be application-owned via replay, and it works on both models.** Test C recovered early, middle, and late facts, and correctly let a later correction override an earlier one.

### 2.5 Measured operational facts

- **Latency:** Cloud ~0.9s, Cloud Pro ~1.15s (three samples each). The plan's guessed 120s was ~100× the observed p50. Revised: 30s default, 120s ceiling.
- **Concurrency:** 4 parallel invocations clean — distinct answers, no cross-talk, no GUI prompts, genuinely parallel (1.92s vs ~4.6s serial-equivalent). Keep default 1, make it configurable; 4 is proven.
- **Failure surface:** exit 1 = missing/renamed shortcut (with useful stderr), exit 64 = usage error, exit 134 = the `/dev/stdout` SIGABRT, **exit 0 + empty stdout = ambiguous** (also what success looks like on a TTY — must be treated as failure), **no exit = empty-input hang**.
- **Hand-built plists are accepted by `shortcuts sign`** — bridge generation is scriptable; import needs one GUI confirmation each.

### 2.6 Plan document updates

`APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md` now records all of the above: §3.4 output contract, §3.5 shortcut internals, §3.6 verified end-to-end, §3.7 remaining unknowns, §17 transport test (two halves), §23 error mapping, §24 concurrency, §25 timeouts, §29 Phases 1–2 marked complete, §34 questions resolved, §36 carries eight hard constraints into Phase 3.

### 2.7 Printing Press investigation (conclusion: conform, don't generate)

- `cli-printing-press` is installed (`~/go/bin`). Its engine is API → client CLI: OpenAPI parsing, HTTP clients, auth, rate limiting, MCP, skills.
- `generate --plan <markdown>` exists and parses our plan document — but parsed it to `Commands: 1` and a scaffold command literally named `0` (scraped from the §23 exit-code list), with name `apple-shortcuts-cloud-gateway-project-outline` scraped from the H1.
- The plan-mode scaffold (`plan_*.go.tmpl`) is ~100 lines of bare Cobra: **no `--agent`, no `--select`, no `--json`**, `ExitCode()` hardcoded to 1, every command body `not implemented`, every file marked DO NOT EDIT.
- Verified against an existing printed CLI: the real `--agent`/`--select` implementation lives in `delpher/source/delpher-pp-cli/internal/cli/agent_context.go` plus `root.go` (~200 lines) — hand-copyable.
- PP verification legs (`shipcheck` = verify + validate-narrative + dogfood + workflow-verify + apify-audit + verify-skill + scorecard) assume printed provenance artifacts (`source spec`, `research.json`, `.printing-press.json` — present in `delpher/source/delpher-pp-cli/`). A hand-built CLI has none of those, so most legs can't run meaningfully.

### 2.8 Artifacts inventory

```text
shortcuts-playground/shortcuts-pcc-tool/
├── APPLE_SHORTCUTS_CLOUD_GATEWAY_PLAN.md          # design doc, updated with all findings
├── results/transport-and-persistence-2026-09-01.md # full test evidence
├── scripts/make-bridge.py                          # bridge generator
├── bridges/                                        # generated + signed .shortcut files
├── shortcuts-docs.md                               # Apple docs snapshot (welcome page only)
└── shotcuts-mac-apple-intelligence.md              # Apple Use Model doc snapshot

Installed in Shortcuts.app: AFM Bridge - Cloud.signed / AFM Bridge - Cloud Pro.signed
Pre-existing: PCC Test, PCC Test Pro (hardcoded prompts; not usable as bridges)
```

---

## 3. Stack Recommendation

### 3.1 Language: Go — with high confidence

- **Single native binary**, trivially installed under `<slug>/bin/`, no runtime deps — matches every other tool in the tree.
- `exec.Command` with pipes is exactly the transport we proved works (`shortcuts run` suppresses output on a TTY but a subprocess pipe is the *working* path — production gets this for free).
- `context.Context` gives us deadline + `Kill` semantics, which is **not optional**: empty input hangs `shortcuts run` forever and macOS has no `timeout(1)`. Go's process-group kill via `os/exec` is exactly the right primitive.
- First-class `net/http` for the OpenAI-compatible server; `net/http/httptest`-style tests without frameworks.
- SQLite via **`modernc.org/sqlite`** (pure Go, no cgo) — same choice the Press ecosystem uses; keeps the binary self-contained and cross-compilable.
- The whole team workflow (AGENTS.md conventions, `--agent --select` pattern, README template) is already Go-shaped.

**Alternatives considered and rejected:**

- **Swift.** Tempting — Foundation Models is right there, and `PrivateCloudComputeLanguageModel()` type-checks. But we measured it failing at the authorization boundary (`LanguageModelError -1` / `ModelManagerError 1046`) from an ad-hoc, unentitled binary. That wall is *why* this project exists. A Swift tool would need proper signing + entitlements, a rebuild-per-seed risk profile, and heavier distribution. Wrong trade for a thin orchestrator.
- **Python / Node:** fine for prototyping, wrong for the artifact — the goal is one durable binary an agent can call for years, installed in `bin/`, with no venv/uv/interpreter drift.
- **Rust:** overkill for a subprocess orchestrator; slower iteration for zero runtime benefit here.

**The genuinely novel engineering is all Go-friendly:** deadline-kill runner, RTF→plain-text handling, SQLite store, transcript renderer, OpenAI-shaped server.

### 3.2 Build mode: hand-built CLI conforming to Printing Press conventions — do NOT use the generator

**Why not `generate --spec`:** we have no API. PP's engine is spec → HTTP client (auth schemes, rate limiting, pagination, endpoint mirrors). None of it applies. The non-HTTP alternative, device specs, is BLE/GATT-specific — not our shape.

**Why not `generate --plan`:** verified by dry-run. It parses only `` - `name` - description `` list items, so our plan yields **one** pseudo-command named `0` (scraped from the §23 exit-code list). Even with a restructured plan, the plan scaffold emits (verified against the actual templates):

- bare Cobra root — **no `--agent`, `--select`, or `--json` flags**,
- `ExitCode()` hardcoded to `1` (violates our stable-error-code requirement),
- every command body `return fmt.Errorf("not implemented")`,
- every file stamped `DO NOT EDIT`.

~100 lines of boilerplate we'd immediately need to fight, plus a `DO NOT EDIT` contract that fights back. The parts of PP that create real value — spec parsing, HTTP client generation, auth, rate limiting, pagination — all target problems we don't have.

**What we do instead:**

1. **Hand-build a normal Go CLI** that lives in the Printing Press tree and obeys every convention: `<slug>/bin/<slug>-pp-cli`, `source/` (rebuildable Go), `skill/` agent skill, README per `CLI_DOC_TEMPLATE.md`.
2. **Copy the agent contract from `delpher`**: `internal/cli/agent_context.go` (~200 lines) implements `--agent` / `--select`; root.go shows flag wiring. Copying a proven ~200-line pattern beats adopting a generator we then have to fight, and the result is indistinguishable to callers.
3. **Verification: `go test` + our own scripts**, not `shipcheck`. Verified: PP's verification legs (`dogfood`, `validate-narrative`, `scorecard --live-check`) assume printed artifacts — `delpher/source/delpher-pp-cli/.printing-press.json` exists and legs consume spec/research artifacts a hand-built CLI won't have. Faking a manifest to run partial legs is possible but fragile; revisit only if we publish.

This is a conscious deviation from the playground's "built with Printing Press" rule — worth a one-line note in the eventual README ("conforms to house conventions; not machine-printed because the surface is a local subprocess, not an HTTP API"). The alternative — printing a scaffold and immediately hand-editing DO-NOT-EDIT files — buys nothing and costs regen pain.

### 3.3 Library choices

| Concern | Pick | Rationale |
| --- | --- | --- |
| CLI framework | **cobra** | House standard in every printed CLI; matches `--agent`-style persistent-flag patterns; PP tooling (verify-skill, mcp surfaces) assumes Cobra-tree shapes. |
| SQLite driver | **`modernc.org/sqlite`** | Pure Go, no cgo, no cross-compile pain, no library plist entanglement. Adequate for single-writer local chat. |
| HTTP server | **stdlib `net/http`** | Three endpoints; nothing to buy. Go 1.22+ ServeMux suffices. |
| Config | **stdlib + `BurntSushi/toml`** (or plain flags first) | Keep §27's config minimal; avoid viper's weight. |
| Structured output | `encoding/json` | Only for `--json` / `--agent` responses. |
| Testing | stdlib `testing` + `testscript`-style golden tests | No heavyweight framework; the transport is already scriptable. |

Deliberately **not** starting with: MCP server (PP would generate one from an HTTP spec later if wanted), Bubbletea/lipgloss TUI for `chat` (a line-reader REPL is enough for v1), viper, GORM, or an ORM.

### 3.4 Architecture (unchanged from the plan, now evidence-backed)

```text
CLI (cobra) ── application core ──┬── Chat Store (SQLite)
                                  └── Runner interface
                                          └── ShortcutRunner
                                                └── /usr/bin/shortcuts run <bridge-UUID>
                                                  --output-type public.plain-text
                                                  (pipe stdin/stdout, context deadline)
```

Non-negotiable runner rules, each earned by a measured failure:

1. Always `--output-type public.plain-text` (default is RTF).
2. Capture stdout via a pipe (never a TTY).
3. **Always** impose a context deadline and kill the child — empty input hangs forever; macOS has no `timeout(1)`.
4. Reject empty prompts before spawning.
5. Treat exit 0 + empty stdout as `shortcut_no_output`, never as an empty response.
6. Don't expect a trailing newline; don't store one that wasn't there.
7. Reference bridges by UUID, not name (names collide/rename; imports got `.signed` suffixes).
8. Default concurrency 1, configurable — 4 proven clean.

Error mapping (measured): exit 1 = missing shortcut, 64 = usage, 134 = SIGABRT (`-o /dev/stdout`), 0+empty = ambiguous → error, no exit = hang.

### 3.5 Verification story

- **Transport tests:** `scripts/test-shortcut-io.sh` + `test-chat-persistence.sh` (already specified in plan §16; results file exists).
- **Unit:** renderer, compaction, store migrations, error mapping — plain `go test`.
- **PP tooling:** `verify-skill` is plausible once we ship a `SKILL.md` (it diffs SKILL.md against CLI flags/commands); `shipcheck`/`scorecard`/`dogfood` assume printed provenance and should be treated as aspirational, not gating, unless we hand-author a minimal `.printing-press.json` that satisfies them. Empirically unresolved — first task when Phase 8 arrives.
- **Post-v1 option:** once `/v1/chat/completions` exists, Printing Press can legitimately print a *client* CLI against it from an OpenAPI spec. Circular for v1, real later.

### 3.6 Open decisions (not blocking Phase 3)

- **Binary name — resolved: `hollis`.** Chosen 2026-09-01 after a registry sweep (Homebrew-core, crates.io, PyPI, npm, RubyGems): every other folksy candidate checked (`hank`, `gus`, `walt`, `otis`, `wilbur`, `huck`, `bosco`, and most of the §32 ideas) is already an existing CLI somewhere; `hollis` is clean in all five registries and absent from our own domain (no other AI/terminal agent shares it).
- Whether to hand-write the PP-style README now or after v1 commands exist.

### 3.7 Immediate next step

**Phase 3 — minimal Go runner**, in the placeholder folder, by hand, conforming to conventions:

```
hollis/
├── go.mod
├── cmd/hollis/main.go
├── internal/cli/        # root + respond + doctor (agent contract copied from delpher)
├── internal/runner/     # Runner interface + ShortcutRunner (deadline, kill, RTF→text)
├── scripts/             # test-shortcut-io.sh, make-bridge.py (make-bridge.py already here)
└── results/
```

Everything else (chat store, serve, compaction) waits until `respond` is real.

---

## Appendix — the 7-bullet product definition (agreed alignment)

- Gives you Apple Intelligence **Cloud** and **Cloud Pro** from the terminal: prompt in, plain text out, ~1s.
- Reads prompts from args or stdin, so shell pipelines and agents can drive it.
- Keeps **persistent chats in local SQLite** — and because Apple's runs are stateless, it replays the stored transcript each turn to create continuity (now proven to work).
- Optionally serves a local **OpenAI-compatible** `/v1/chat/completions` endpoint so existing local apps can point at Apple Intelligence.
- Treats Shortcuts purely as **transport** — one bridge shortcut invoked via `/usr/bin/shortcuts` — so the backend is swappable if Apple ships a real API.
- Ships a first-class `doctor`, honest errors, and never fabricates token counts, streaming, or model IDs.
- Local-only by default; nothing leaves the machine except the model call Apple already makes.
