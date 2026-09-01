# hollis CLI — Independent Test Report (Run 2)

- **Date:** 2026-09-01
- **Tester:** Independent agent run (fresh session, no prior context)
- **CLI version:** 0.1.0
- **Method:** All commands executed live on this machine. Model responses are verbatim terminal output. No prior test reports or reports were consulted.

---

## 1. Baseline Health

```bash
$ hollis version
hollis 0.1.0

$ hollis doctor
hollis doctor (version 0.1.0)
  transport: ok
  timeout default: 30s (ceiling 120s)
  bridges (referenced by UUID):
    [OK] cloud      BD8CDC56-7CB8-418D-9B02-9D33AB911BF0 (AFM Bridge - Cloud.signed)
    [OK] cloud-pro  DBB6E472-CBC6-4421-8D32-9D4543D5CDE6 (AFM Bridge - Cloud Pro.signed)
    [OK] on-device  E530AE25-3C3C-4B11-88AF-A66F74039F88 (AFM Bridge - On-Device.signed)
    [OK] chatgpt    24B4B536-571B-49D9-9519-B644281C8B08 (AFM Bridge - ChatGPT.signed)
```

**Result:** ✅ Transport stable, all 4 bridges resolvable and healthy.

---

## 2. Cross-Model Comparison (Fresh Prompt)

Prompt: `"What is the difference between TCP and UDP? Answer in one sentence."`

| Tier | Response (verbatim) |
|------|---------------------|
| **on-device** | `TCP ensures reliable, ordered delivery of data through connections, while UDP sends data quickly without guaranteeing delivery or order.` |
| **cloud** | `TCP provides reliable, connection-oriented communication with error checking and ordering, while UDP offers faster, connectionless transmission without guarantees of delivery or sequence.` |
| **cloud-pro** | `TCP is a connection-oriented protocol that guarantees reliable, ordered data delivery, whereas UDP is a connectionless protocol that prioritizes speed by sending data without ensuring it arrives successfully.` |
| **chatgpt** | `TCP is a connection-oriented protocol that ensures reliable, ordered data delivery, while UDP is connectionless and faster but does not guarantee reliability or order.` |

**Result:** ✅ 4/4 correct. Distinct phrasing confirms separate model backends per tier.

---

## 3. Output Mode Validation

### `--agent` (structured JSON)

```bash
$ hollis respond --agent --model cloud-pro "What is the capital of Japan? Answer in one word."
{"meta":{"source":"apple-intelligence"},"results":{"model":"cloud-pro","response":"Tokyo"}}
```

### `--json` (compact)

```bash
$ hollis respond --json --model chatgpt "Translate 'good morning' to Spanish."
{"model":"chatgpt","response":"\"Good morning\" in Spanish is **buenos días**."}
```

### `--select` (field projection)

```bash
$ hollis respond --agent --model on-device --select response "How many days are in a leap year?"
{"meta":{"source":"apple-intelligence"},"results":{"response":"A leap year has 366 days."}}
```

**Result:** ✅ All three modes emit valid, machine-parseable JSON. `--select` correctly strips unwanted fields (note `model` key absent from `results`).

---

## 4. Persistent Chat & Continuity

### Turn 1: Plant context

```bash
$ hollis chat --no-input --agent --model cloud "My favorite color is purple. Please acknowledge this."
{"meta":{"source":"apple-intelligence"},"results":{"conversation_id":"676cada2-c9e6-4efb-b86c-33ca45ca4da6","model":"cloud","response":"Got it — your favorite color is purple."}}
```

### Turn 2: Cross-process recall (new shell invocation)

```bash
$ hollis chat --continue 676cada2-c9e6-4efb-b86c-33ca45ca4da6 --no-input --agent "What is my favorite color?"
{"meta":{"source":"apple-intelligence"},"results":{"conversation_id":"676cada2-c9e6-4efb-b86c-33ca45ca4da6","model":"cloud","response":"Your favorite color is purple."}}
```

**Result:** ✅ Continuity verified. The model correctly recalled "purple" from a separate process invocation, proving the transcript replay mechanism works.

### Transcript inspection

```bash
$ hollis chats show 676cada2-c9e6-4efb-b86c-33ca45ca4da6 --no-input
id: 676cada2-c9e6-4efb-b86c-33ca45ca4da6
model: cloud
title: My favorite color is purple. Please acknowledge this.
created: 2026-09-01T23:23:53.933431Z  updated: 2026-09-01T23:23:59.783627Z

USER:
My favorite color is purple. Please acknowledge this.

ASSISTANT:
Got it — your favorite color is purple.

USER:
What is my favorite color?

ASSISTANT:
Your favorite color is purple.
```

### Deletion verification

```bash
$ hollis chats delete 676cada2-c9e6-4efb-b86c-33ca45ca4da6 --yes --no-input
$ hollis chats list --no-input --json | grep -c 676cada2
0
```

**Result:** ✅ Full CRUD lifecycle verified. Conversation removed from store.

---

## 5. Input Handling & Error Cases

### Stdin pipe

```bash
$ printf "Name a planet in our solar system. Just the name." | hollis respond --no-input --model on-device
Mars
```

**Result:** ✅ Piped stdin accepted as prompt.

### Invalid model name

```bash
$ hollis respond --model invalid-tier --no-input "test"; echo "EXIT: $?"
unknown model "invalid-tier": choose auto (default), cloud, cloud-pro, on-device, or chatgpt
EXIT: 2
```

**Result:** ✅ Clean error, exit code 2, no model call made.

### Config inspection

```bash
$ hollis config show --no-input
config file: /Users/zandbak/Library/Application Support/hollis/config.json
default model: auto
```

**Result:** ✅ Config readable at expected path.

### Agent context schema

```bash
$ hollis agent-context | python3 -c "import sys,json; d=json.load(sys.stdin); print('Schema OK:', d.get('schema_version'), '| CLI:', d['cli']['name'], d['cli']['version'], '| Commands:', len(d['commands']))"
Schema OK: 1 | CLI: hollis 0.1.0 | Commands: 9
```

**Result:** ✅ Valid JSON schema, 9 commands exposed for agent discovery.

---

## 6. Summary

| Test | Result | Notes |
|------|--------|-------|
| Transport / Bridges | ✅ | All 4 UUIDs healthy |
| Cross-model respond | ✅ | 4 distinct correct answers |
| `--agent` JSON | ✅ | Stable meta/results schema |
| `--json` | ✅ | Compact, valid |
| `--select` | ✅ | Field projection works |
| Chat continuity | ✅ | Cross-process recall confirmed |
| Chat CRUD | ✅ | Create, show, delete verified |
| Stdin pipe | ✅ | Works |
| Error handling (bad model) | ✅ | Exit 2, clean message |
| Config | ✅ | Path correct, value readable |
| Agent context | ✅ | Valid schema v1 |

**Overall:** ✅ **11/11 PASS** — No defects found in this independent run.

---

## Appendix: Commands Executed

1. `hollis version`
2. `hollis doctor`
3. `hollis respond --model on-device --no-input "What is the difference between TCP and UDP? Answer in one sentence."`
4. `hollis respond --model cloud --no-input "What is the difference between TCP and UDP? Answer in one sentence."`
5. `hollis respond --model cloud-pro --no-input "What is the difference between TCP and UDP? Answer in one sentence."`
6. `hollis respond --model chatgpt --no-input "What is the difference between TCP and UDP? Answer in one sentence."`
7. `hollis respond --agent --model cloud-pro "What is the capital of Japan? Answer in one word."`
8. `hollis respond --json --model chatgpt "Translate 'good morning' to Spanish."`
9. `hollis respond --agent --model on-device --select response "How many days are in a leap year?"`
10. `hollis chat --no-input --agent --model cloud "My favorite color is purple. Please acknowledge this."`
11. `hollis chat --continue 676cada2-c9e6-4efb-b86c-33ca45ca4da6 --no-input --agent "What is my favorite color?"`
12. `hollis chats show 676cada2-c9e6-4efb-b86c-33ca45ca4da6 --no-input`
13. `hollis chats delete 676cada2-c9e6-4efb-b86c-33ca45ca4da6 --yes --no-input`
14. `hollis chats list --no-input --json | grep -c 676cada2`
15. `printf "Name a planet in our solar system. Just the name." | hollis respond --no-input --model on-device`
16. `hollis respond --model invalid-tier --no-input "test"`
17. `hollis config show --no-input`
18. `hollis agent-context | python3 -c "..."`
