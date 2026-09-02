# macOS 26 compatibility — actionable fix list

Hollis was developed on **macOS 27.0** (build `26A5421a`). No macOS 26 (Tahoe) machine was available, and this document is written under that constraint: 26 support is defensive, shipped from a 27 machine, marked **untested on 26**, and contains no invented 26-only measurements. Every claim below is labelled with how it was arrived at.

Hollis reaches Apple Intelligence through Shortcuts (`/usr/bin/shortcuts run`), not through `fm` / Foundation Models — on 26 as on 27. `fm --model pcc` is not the path for 26 users.

## Status of claims

| Claim | Status |
| --- | --- |
| This Mac: 4 bridges, UUID invoke, TTY/empty-hang/RTF rules | **Measured** on 27 |
| 26 Use Model has 3 locations: On-Device, one PCC cloud, ChatGPT | **Documented** (Apple Help, MacStories WWDC 2025) |
| 27 adds Cloud Pro (`WFLLMModel "Apple Intelligence Pro"`) | **Measured** on 27 |
| 26 PCC string is `WFLLMModel "Apple Intelligence"` | **Inferred** (public plists); not decoded from a 26 `Shortcuts.sqlite` |
| 26 on-device / ChatGPT strings equal 27's | **Unknown** |
| `shortcuts run <UUID>` on 26 | **Unknown** (27-only measurement) |
| Import of `WFWorkflowClientVersion 3100.0.2.3` on 26 | **Unknown** (min version is 900, so maybe; Pro enum is the real risk) |
| `PrivateCloudComputeLanguageModel` Swift API | **27+** — irrelevant to hollis |

## What 26 vs 27 means for hollis

```text
26 Tahoe     on-device | cloud (PCC, one model) | chatgpt
27 (yours)   on-device | cloud (AFM 3) | cloud-pro (AFM 3 Pro) | chatgpt
```

Same CLI flags (`cloud`, `on-device`, `chatgpt`). **Different underlying models** behind `cloud`. Do not print “AFM 3 …” for 26.

`auto` stays cloud → on-device on both. Never fall back to ChatGPT. Never fall back to Pro.

## What is already broken on any other Mac (including 27)

`internal/runner/shortcut.go` `New()` hardcodes **this Mac’s** imported UUIDs:

```text
cloud      BD8CDC56-7CB8-418D-9B02-9D33AB911BF0
cloud-pro  DBB6E472-CBC6-4421-8D32-9D4543D5CDE6
on-device  E530AE25-3C3C-4B11-88AF-A66F74039F88
chatgpt    24B4B536-571B-49D9-9519-B644281C8B08
```

A 26 user who imports `bridges/*.signed.shortcut` gets **new** UUIDs. `doctor` only checks names; `Run` still invokes the baked UUIDs → exit 3 / missing shortcut.

**Fix this first.** It is required for 26 *and* for a second 27 Mac.

---

## Work you can do entirely on this 27 Mac

Do these in order. Each step is testable here. 26 remains untested until a volunteer runs the script at the bottom.

### 1. Resolve bridges at runtime (required)

**Files:** `internal/runner/shortcut.go`, `internal/cli/config.go`, `internal/cli/doctor.go`

Resolution order for each tier:

1. `config.json` override (`bridges.cloud`, etc. — name or UUID)
2. `shortcuts list` match on stable names:
   - `AFM Bridge - Cloud.signed` / `AFM Bridge - Cloud`
   - `AFM Bridge - Cloud Pro.signed` / `AFM Bridge - Cloud Pro`
   - `AFM Bridge - On-Device.signed` / `AFM Bridge - On-Device`
   - `AFM Bridge - ChatGPT.signed` / `AFM Bridge - ChatGPT`
3. Compile-time UUID **only as last resort** (keeps *this* machine working without config)

Invoke with whatever `shortcuts list` / config gave you. Prefer **name** when present so 26 does not depend on UUID-run (unmeasured there). Keep UUID as the 27 fast path when config/list yields a UUID.

`hollis config set bridge cloud <name-or-uuid>` (or a nested JSON object written by doctor). Keep the file tiny; no viper.

**27 test:** rename a bridge in Shortcuts.app → `hollis doctor` still OK → `hollis respond --model cloud` still works via name. Restore the name.

### 2. Catalog = installed + OS, not “always four”

**Files:** `internal/cli/models.go`, `internal/server/server.go` `handleModels`, `internal/runner/runner.go` `Valid()` if needed

- `hollis models` / `GET /v1/models` list `auto` plus tiers whose bridges resolve.
- `cloud-pro` missing → omit from lists.
- Explicit `hollis respond --model cloud-pro` when unresolved → exit **2** with  
  `cloud-pro is not available (bridge not installed; Cloud Pro requires macOS 27)`  
  not a generic missing-shortcut.
- `config set model cloud-pro` on a machine without that bridge → same exit 2.
- `chatgpt` `owned_by` stays `OpenAI` (already fixed in serve).

Do **not** hide Pro on 27 just because Darwin parsing is wrong. Gate Pro on **bridge presence**, not only on `sw_vers`. A 27 Mac with no Pro shortcut should behave like 26.

Optional extra: `sw_vers -productVersion` major `< 27` → doctor line  
`cloud-pro: unsupported on macOS 26 (untested; Use Model had no Cloud Pro)`.  
Use **ProductVersion**, not `uname` (this 27 box’s build is `26A5421a`).

**27 test:** temporarily point Pro at a fake name in config → models omit it, respond cloud-pro exits 2, cloud still works.

### 3. `doctor` tells the truth

Print:

- `macos: 27.0` (from `sw_vers`)
- each tier: `OK` (resolved + listed) / `MISSING` / `UNSUPPORTED`
- the **resolved ref** (name or UUID), not always the baked UUID
- `support: macOS 27 measured; macOS 26 untested`

JSON: add `macos`, `resolved_ref`, `status`.

**27 test:** `doctor --json` has four OK with real objects (already true); after step 1 the `uuid`/`name` fields match what `Run` uses.

### 4. Split bridge generation (26-safe vs 27)

**File:** `scripts/make-bridge.py`

Today it always emits four plists, `WFWorkflowClientVersion 3100.0.2.3`, Pro included.

Add a profile flag, default 27:

```bash
python3 scripts/make-bridge.py bridges/              # 27: four bridges (current)
python3 scripts/make-bridge.py --os 26 bridges/26/   # 26: three bridges, no Pro
```

| Profile | Tiers | `WFLLMModel` | ClientVersion |
| --- | --- | --- | --- |
| 27 (measured) | 4 | Cloud / Pro / on Device / ChatGPT as now | keep 3100 |
| 26 (best guess) | 3, no Pro | Cloud: `"Apple Intelligence"` (inferred). On-device: `"Apple Intelligence on Device"` (27 string, **guess**). ChatGPT: `"ChatGPT"` (guess) | lower to something pre-27, e.g. `2700.0.4` found in older shortcut dumps; keep `MinimumClientVersion` 900 |

Keep Follow Up off, `WFGenerativeResultType` Text, prompt = Shortcut Input. That already avoids the 26 GUI hang (Follow Up / Ask Each Time).

Do **not** ship the 27 signed Pro shortcut as a 26 install step.

README install: 27 uses `bridges/*.signed.shortcut`. 26 uses `bridges/26/` and is **untested**.

**27 test:** `--os 26` writes exactly three files and none contain `Apple Intelligence Pro`. 27 profile unchanged; re-import not required on this Mac.

### 5. Errors and help copy

- `respond --help` / `models` Long: Pro is “macOS 27+; ignored/unavailable if the bridge is missing”.
- Never mention `fm` as a hollis backend.
- Timeout / empty-prompt / no-output rules stay; they are 27-measured and likely 26 but unlabeled as such.

### 6. README — honest compatibility (paste-ready)

Add a short section. Do not claim 26 works.

```markdown
## Compatibility

Measured on macOS 27 (Apple Intelligence Use Model: on-device, Cloud,
Cloud Pro, ChatGPT).

macOS 26 (Tahoe) is **untested**. Shortcuts there documented three Use
Model locations (on-device, one Private Cloud Compute cloud, ChatGPT) —
no Cloud Pro. `hollis` will refuse `cloud-pro` if that bridge is absent.
The `cloud` tier on 26 is last year’s PCC model, not AFM 3.

`fm` is not used. On macOS 26 the Foundation Models API had no PCC
backend; Shortcuts is the cloud path.

If you are on 26: generate `python3 scripts/make-bridge.py --os 26`,
sign, import, run `hollis doctor`, then `hollis respond --model cloud
"Reply with OK"`. Please file what doctor printed and whether UUID or
name worked. Until then, treat 26 as experimental.
```

Also in Requirements: “macOS 27 measured; macOS 26 experimental / untested”.

### 7. Tests you can run here (no 26)

Unit (fakes):

- Bridge resolution: config name > list name > compiled UUID
- Missing Pro → `Valid`/respond usage 2, models JSON has no `cloud-pro`
- `make-bridge.py --os 26` file set
- doctor JSON includes macos + status

Live (this Mac, 27):

- After resolution change: `python3 scripts/live-suite/live_suite.py` then `--live`
- `hollis respond --model cloud-pro` still works **with** the Pro bridge installed
- Fake-missing Pro via config does not break cloud

Do not add a live 26 job to CI.

---

## What you cannot finish without a 26 Mac

Leave these as README “untested”; do not guess a green check.

1. Decode 26 `Shortcuts.sqlite` ZDATA for on-device and ChatGPT `WFLLMModel` strings.
2. Confirm `shortcuts run <UUID>` vs name.
3. Confirm 3100 vs 2700 plist import.
4. TTY-empty-stdout and empty-input hang (assume same, don’t claim).
5. ChatGPT extension login (“logout to make it work” is 27-only).
6. On-device replay / 4K context overflow.
7. PCC daily quota stderr shape.

If a 26 user shows up, they run:

```bash
sw_vers
hollis doctor --json
hollis models --json
hollis respond --model cloud "Reply with exactly: PONG"
hollis respond --model on-device "Reply with exactly: PONG"
hollis respond --model chatgpt "Reply with exactly: PONG"
hollis respond --model cloud-pro "Reply with exactly: PONG"   # expect exit 2
```

Capture exit codes and stdout. That is enough to lock the 26 strings and drop “untested”.

---

## Out of scope (do not do for 26)

- Calling `fm` or Foundation Models from hollis
- Embeddings / Engram
- Changing the replay transcript format without 26 evidence
- Advertising AFM 3 / Core Advanced on 26
- Faking Cloud Pro as an alias of `cloud` on 26 (that lies)

## Suggested implementation order

1. Runtime bridge resolution + config override (unblocks every other Mac)
2. Models/serve/doctor list what is installed; Pro missing → exit 2
3. `make-bridge.py --os 26`
4. README compatibility paragraph
5. Unit tests for 1–3
6. Re-run live suite on this 27 Mac so Pro still works

That is the whole 26 project without a 26 Mac: **portable bridges, honest catalog, untested label.**
