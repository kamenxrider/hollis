# Ask Apple, then continue

After [installation and setup](../README.md#install), use `/hollis:hollis` in
Claude Code or `$hollis:hollis` in a Codex conversation. The examples below are
the request to put after that prefix. Select actual files in your host or give
their accessible local paths; example filenames are not attachments by themselves.

## Everyday requests

| Task | Example request |
| --- | --- |
| Writing | Ask Apple Cloud to turn the notes above into a friendly invitation. Keep the date, cost and location unchanged. |
| Planning | Ask Apple Cloud Pro to compare these three venues against the stated budget and accessibility requirements. Flag missing information. |
| Research synthesis | Ask Apple Cloud to compare the two supplied source extracts. Separate agreement, disagreement and unanswered questions. |
| Documents | Use Cloud to compare brief.md with notes.txt. List decisions that appear in only one document. |
| Image understanding | Ask Apple Cloud Pro to describe the attached diagram and explain which labels are unclear. |
| Local text | Use On-Device to shorten this paragraph to 60 words while keeping its meaning. |
| Structured local data | Use native Local with a schema to return one person as an object with `name`, `age` and `city`. |
| ChatGPT text | Use Hollis's ChatGPT route to draft an invitation from these eight details without inventing an address or URL. |
| Image generation | Use Hollis Illustration to create a tiny greenhouse on a brass turtle. Save it as a new image and show it here. |
| Code to a file | Use Cloud to write a shell function that renames the PNG files in a folder by date, and save only the code to rename.sh so I can review it. |
| Folder processing | Plan summaries for the .md files in this folder using Cloud. Show the file count first; run at most three calls and keep the job so we can resume. |

Hollis does not perform web search. For research, supply sources or ask the host
to gather relevant material before sending selected context to Apple. Text-file
input accepts regular UTF-8 documents of any extension. When `agent-context`
describes PDF support on `--file`, PDFs are extracted on the Mac; `--pages`
selects pages from one PDF. Office files need conversion first. Instructions and documents must fit the runtime's 128 KiB rendered
prompt limit. Oversized requests need a smaller agreed scope, not silent truncation.

## Context and Local controls

Use the actual runtime contract before offering newer flags. When `agent-context` lists `--messages` on `respond`,
`respond --messages <private-file> --agent --model cloud`
passes selected host context as a one-shot messages array; it does not save a
second transcript. Listed `--system` (`-s`) and `--system-file` supply instructions.
Before large Local inputs, listed `--count-tokens` checks fit without generating.
Context shortening needs your explicit agreement to omit older context:
`--context-strategy` supports request-only drop-oldest, sliding-window and
Local-only summarize (one extra Local call), without editing stored history.
Listed `--retry` is opt-in for Local busy failures only, never Cloud or ChatGPT;
`--permissive` is only for Local transformations of supplied text and can still
refuse. `benchmark` makes serial Local model calls only when requested;
`update` makes an explicit GitHub check, never an automatic upgrade.

## Choose the route

| Route | Text and text documents | Image understanding |
| --- | --- | --- |
| Cloud | Yes | PNG/JPEG |
| Cloud Pro | Yes | PNG/JPEG |
| On-Device | Yes | Unavailable |
| ChatGPT | Yes | One PNG/JPEG per request |
| Native Local | Yes, when reported available by `models --model local --json` | One PNG/JPEG when its image capability is reported |

Cloud, Cloud Pro, On-Device and ChatGPT are Hollis's **Shortcuts routes**. Cloud and Cloud Pro use PCC; they are
separate from Apple's native Foundation Models developer API. On-Device selects
local model execution, but your surrounding agent may still be a cloud service.
See [privacy](privacy.md).

Guided output is a native `local` capability, not a Shortcut route: it returns an
object that follows a supported JSON Schema subset, only when the installed
runtime reports the local model `available: true` with
`capabilities.guided_output: true`.
Guided output never replaces a requested Cloud or Cloud Pro answer. Limits,
rejections and diagnostic fields are in the
[skill workflows](../skills/hollis/references/workflows.md#guided-output-native-local-only).

Your explicit selection wins. Otherwise Hollis's plugin uses an existing concrete
model preference, then Cloud. A saved `auto` preference is not a concrete model;
the plugin chooses Cloud in that case. Failure does not authorize another tier.

## Hollis as the model behind another tool

Ask the agent to set Hollis up as the model for a script or another
OpenAI-compatible tool on this Mac: for example, “Show me how to call Hollis
Cloud from my shell script and read only the answer,” or “Help me point my
editor's OpenAI-compatible setting at Hollis.” Scripts call `hollis respond`
with JSON output and read structured errors. Tools connect to `hollis serve`,
a local endpoint at `http://127.0.0.1:1978/v1` protected by a token. You start
the server yourself because it keeps running. On runtime 0.7.0, Native Local accepts sampling fields and caller-executed
function tools when its capabilities report support; Shortcuts routes refuse
`temperature`, `max_tokens` and `tools`, so configure those clients to omit them; listing the models does not prove calls will work. The
[skill workflows](../skills/hollis/references/workflows.md#hollis-as-the-model-behind-a-script-or-tool)
list exactly what a client must send.

## Follow up in the same conversation

After the venue comparison, say: “Ask Apple to reconsider with a EUR 350 budget,
keeping the accessibility requirement.” The host sends the relevant earlier
details and your change. You receive the next answer in the same conversation.

If you want a Hollis conversation saved across host sessions, explicitly ask:
“Save this as a persistent Hollis conversation and keep its ID.” Ordinary
follow-ups do not automatically create that separate saved history.

## Images and revisions

Choose **Animation, Illustration, Sketch, Genmoji or Any Style**. Animation is
the default when no style is requested. Any Style does not guarantee photography;
ChatGPT image generation is unavailable through the tested Shortcut route.

Ask for a shape explicitly: “Make a 16:9 crop of the result,” or “Pad it to
1200 by 800 without cutting off the subject.” Crop, pad and resize happen locally
after generation; they are not native sampling settings. No seed control is exposed.

For a revision: “Attach the previous image and request a blue background. Keep
the brass turtle and greenhouse described in the prompt; save a new file.” The
plugin explicitly submits the previous file. **Reliable use of its pixels remains
unproven**, including subject preservation. Judge the returned image; receipt
of an attachment is not proof of an edit. See [reference evidence](compatibility.md#image-reference-evidence).

The response should include a model/style label, a preview where supported and
an absolute **Open image** file link. If only the link displays, open the file
through that link. Agent inspection of a file does not prove your host displayed it.

## Folder jobs and recovery

Folder planning makes no Apple calls. Execution uses an explicit call budget
and a saved job record. Ask to resume that job later; verified successful items
are skipped. The plugin paces requests and does not create a background watcher.

On a rate limit, it stops the affected sequence. A timeout may leave the result
uncertain, so investigate before requesting another generation. If the host or
Apple needs a permission, the agent explains the specific action. Existing
authorization still covers ordinary follow-ups within the agreed task.

[Return to the plugin guide](../README.md) · [workflow example](../examples/demo.md) ·
[Troubleshooting](troubleshooting.md)
