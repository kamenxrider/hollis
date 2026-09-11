# CLI usage and local data

Start with the [verified install and first request](../README.md#install-verified). This guide keeps the detailed command and storage contracts together.

## Everyday use

```bash
hollis respond "Summarize this repo in one sentence"
printf 'Explain closures in Go' | hollis respond
hollis respond --model cloud-pro "Analyze this bug"
hollis respond model cloud-pro "same thing, model before the prompt"
hollis respond --timeout 90s "A question worth waiting for"
```

The prompt comes from the argument, `--prompt-file`, or stdin, so pipelines work. Each `respond` call is stateless. Default timeout is 30 seconds, ceiling 120. Hollis rejects a rendered prompt over 128 KiB before invoking Apple. For Shortcuts routes, all rendered text reaches Shortcuts through a private temporary `.txt` file; see [transport and cleanup details](shortcuts.md#how-it-works).

## Native local streaming

`hollis respond --model local` and `hollis chat --model local` use Apple’s native
on-device SDK and stream human output when stdout is a terminal. Use `--stream`
to stream explicitly to a pipe, or `--stream=false` for a complete answer. JSON
and agent output never stream, even in a terminal; `--stream=true` with either
is rejected. Terminal control characters are rendered visibly. No conversation
is silently shortened to fit native context capacity. See [native local](native-local.md).

## Text documents

```bash
hollis respond --prompt-file instructions.txt
hollis respond "Summarize the differences" --file first.md --file second.txt
```

`--prompt-file` reads the instruction exactly as UTF-8 text. Repeat `--file` to append local `.txt` and `.md` documents in order, with their basenames and explicit boundaries. The instruction, boundaries and document contents together must fit the 128 KiB prompt limit; Hollis rejects oversized input without truncation. Empty, invalid UTF-8 and nonregular files are rejected. PDF is not supported.

Choose one instruction source. File requests require positional text or `--prompt-file` and reject nonempty piped stdin. Documents cannot be mixed with `--image` in one request. Documents are prompt content; their boundaries do not isolate untrusted instructions.

## Images

`respond` accepts PNG and JPEG files through the existing bridges:

```bash
hollis respond --image photo.jpg "What is this?"
hollis respond --model cloud-pro --image a.png --image b.png "Compare them"
```

An image request with no selected or configured model defaults directly to Cloud. Cloud and Cloud Pro accept repeated `--image`; ChatGPT accepts one image. `local`, `auto` and On-Device are rejected for images because the tested On-Device Shortcut ignored the pixels, making automatic fallback unsafe.

Images must be direct regular PNG/JPEG files, at most 64 MiB and 64 million pixels each. Hollis rejects symlinks in the file or its parent directories, apart from macOS's standard root-owned `/var`, `/tmp` and `/etc` aliases. Use the real path for an image reached through a custom directory link. The runner validates a private byte snapshot and passes only that snapshot to Shortcuts; staged images are removed after success, failure or cancellation.

When images are present, give the prompt as an argument or with `--prompt-file`. Hollis passes the private prompt file first, followed by the images as repeated Shortcuts inputs, and cleans up staging after the run subject to the limits described above. Do not pipe a second prompt through stdin with `--image`. Image chat history remains unsupported. The same model tiers accept inline images through the API, described in the [API guide](api.md#inline-image-input).

## Persistent chats

Shortcuts model calls are stateless. Hollis stores conversations locally and replays the transcript each turn:

When human chat output is attached to a terminal, Hollis renders terminal
control characters visibly so a model response or stored message cannot alter
terminal state. JSON output and redirected human output preserve the original
content for deterministic scripts.

```bash
hollis chat "Remember the codeword VANTA-ORBIT-7319"
hollis chat --continue <id> "What was the codeword?"
```

Run `hollis chat` with no argument in a terminal for an interactive session — blank lines are skipped, Ctrl-D ends it, and `--continue <id>` resumes an existing conversation:

```
$ hollis chat
New chat · auto · 6f2c…
> What is a closure?
< A closure is a function that captures variables from its surrounding scope.
> Give me one in Go.
< func counter() func() int { i := 0; return func() int { i++; return i } }
```

Managing them:

```bash
hollis chats list
hollis chats show <id>            # metadata plus every message
hollis chats search VANTA-ORBIT   # full-text (FTS5)
hollis chats rename <id> "New title"
hollis chats delete <id> --yes
```

The whole search query is treated as one phrase, so hyphenated tokens like `VANTA-ORBIT` match verbatim. Archived conversations are skipped.

A continuation always uses the model stored with that conversation. Combining `--continue` with a positional model or explicit `--model` is rejected. Hollis stores complete turns atomically and preserves complete history; when a chat reaches 256 stored messages or its rendered prompt exceeds 128 KiB, it fails clearly instead of trimming or summarizing it.

## Scripts and agents

```bash
hollis respond --agent "Name three Go testing tips"   # JSON + non-interactive
hollis models --json
hollis doctor --json --select bridges
hollis agent-context                                  # machine-readable CLI description
```

JSON output carries both `model_requested` (what you asked for) and `model_used` (what
answered). They differ when `auto` falls back from Cloud to On-Device — the two
are not interchangeable, so the substitution is reported rather than hidden. In
human mode that fallback prints one line to stderr, leaving stdout clean for
pipelines. The local API reports the serving tier in its `model` field, as
OpenAI does.

`--agent` is shorthand for `--json --no-input` on data commands. In `--no-input` mode hollis never waits on a terminal, and destructive commands require explicit confirmation flags (`hollis chats delete <id> --yes`). Long-running `serve` and shell `completion` support human output only and reject JSON/agent mode clearly.

Exit codes are stable and parseable:

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Unexpected internal error |
| 2 | Usage error — bad flag, unknown model, empty prompt, no matching command |
| 3 | Missing resource — unknown conversation id, no search hits, bridge not installed |
| 5 | Discovery, transport, Shortcut execution, declined request, or Apple rate-limit failure |
| 7 | Timeout — the run exceeded its deadline and was killed |
| 10 | Config or database error |

Runtime 0.3.2 adds specific JSON codes `request_declined` and `shortcut_failed`,
both retaining exit 5. See [failure meanings and next actions](errors.md).

## Your data

Configuration, chat history and run diagnostics live in one directory:

```
~/Library/Application Support/hollis/
├── hollis.db       conversations, messages, and per-run diagnostics
├── hollis.db.chat.lock  cross-process chat-continuation lock
├── config.json.lock     cross-process config update lock
└── config.json          default model and any bridge overrides
```

The state directory is mode `0700`; its database, config, lock, migration backups, and temporary files are mode `0600`. Set `HOLLIS_STATE_DIR` to an **absolute** directory for an isolated test or alternate state location. Existing Hollis-owned modes are tightened without changing broader parent directories.

Generated images and batch results live at the output paths you choose, outside this state directory. They are not deleted when you delete a chat.

Run diagnostics contain only request ID, requested/used tier, timing, exit code, error class, fallback, and byte counts — never prompts, replies, or raw Apple stderr. Conversation deletion removes its messages, run records, and search entries in one transaction. To delete everything Hollis knows, remove that directory. To delete one conversation, `hollis chats delete <id>`.

## Quick reference

```bash
hollis respond "prompt"                    # one-shot response
hollis respond --model cloud-pro "prompt"  # explicit Cloud Pro
hollis respond --image photo.jpg "describe" # image input; defaults to Cloud
hollis respond --file notes.md "summarize"   # attach a text document
hollis image generate "A lighthouse" --style sketch --output lighthouse.png
hollis batch resume --job ./job.json --max-calls 12
hollis chat "remember this"                # start a persistent chat
hollis chat --continue <id> "follow up"     # continue a chat
hollis chats list                           # list stored chats
hollis chats show <id>                      # show one chat
hollis chats search "query"                 # search stored chats
hollis serve --token-file hollis.token      # authenticated local API
hollis doctor                               # check transport and bridges
hollis models                               # show available tiers
hollis config set model cloud-pro           # save a default tier
```

[Back to Hollis](../README.md)
