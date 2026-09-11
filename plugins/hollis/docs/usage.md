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
| ChatGPT text | Use Hollis's ChatGPT route to draft an invitation from these eight details without inventing an address or URL. |
| Image generation | Use Hollis Illustration to create a tiny greenhouse on a brass turtle. Save it as a new image and show it here. |
| Folder processing | Plan summaries for the .md files in this folder using Cloud. Show the file count first; run at most three calls and keep the job so we can resume. |

Hollis does not perform web search. For research, supply sources or ask the host
to gather relevant material before sending selected context to Apple. Text-file
input accepts UTF-8 `.txt` and `.md`; PDF and Office files are not directly
supported. Instructions and documents must fit the runtime's 128 KiB rendered
prompt limit. Oversized requests need a smaller agreed scope, not silent truncation.

## Choose the route

| Route | Text and text documents | Image understanding |
| --- | --- | --- |
| Cloud | Yes | PNG/JPEG |
| Cloud Pro | Yes | PNG/JPEG |
| On-Device | Yes | Unavailable |
| ChatGPT | Yes | One PNG/JPEG per request |

These are Hollis's **Shortcuts routes**. Cloud and Cloud Pro use PCC; they are
separate from Apple's native Foundation Models developer API. On-Device selects
local model execution, but your surrounding agent may still be a cloud service.
See [privacy](privacy.md).

Your explicit selection wins. Otherwise Hollis's plugin uses an existing concrete
model preference, then Cloud. A saved `auto` preference is not a concrete model;
the plugin chooses Cloud in that case. Failure does not authorize another tier.

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
