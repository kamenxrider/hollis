# Getting started with Hollis

You need an Apple-silicon Mac with macOS 27 or later. In System Settings,
open Apple Intelligence & Siri, enable Apple Intelligence and wait for its
model downloads to finish. Intel Macs cannot run these models. macOS 26 is
untested and is not supported by this bundle.

Using Claude Code or Codex? Follow the [plugin quick start](../plugins/hollis/docs/quickstart.md)
instead. The agent must run on this Mac.

## Install

Open Terminal, which is in Applications > Utilities. Paste commands there,
one block at a time.

Choose Homebrew if you already use it. It manages upgrades and removal:

```bash
brew install kamenxrider/tap/hollis
```

Otherwise, use the install script. Download it, read it in Terminal, then run it:

```bash
curl -fsSLo install-hollis.sh https://raw.githubusercontent.com/kamenxrider/hollis/main/scripts/install.sh
less install-hollis.sh
```

Press `q` to leave the viewer. Run the script only if you trust what it does:

```bash
bash install-hollis.sh
export PATH="$HOME/.local/bin:$PATH"
```

The script needs no administrator password or `sudo`. It installs the complete
bundle in `~/.local/share/hollis/0.7.0/` and puts links to both executables in
`~/.local/bin`. It also opens nine Shortcut import windows. You may close
those windows if you only want Local; you can add the bridges later.
Homebrew does not open imports during installation.

Without `gh`, the installer checks file fingerprints (SHA-256) only. Only `gh`
verifies the source commit, using GitHub build provenance. Install the
[GitHub CLI](https://cli.github.com/) before running the script if you want
that extra check. The binaries are not Developer ID signed or notarized.
See [verification and trust](verification.md) before changing macOS security settings.

<details>
<summary>Keep the installer on your PATH in new Terminal windows</summary>

The `export` command above affects only the current window. For the default
macOS zsh shell, add this line to `~/.zshrc` in a text editor:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Open a new Terminal window afterwards. For another shell, add the equivalent
setting to its startup file. A custom installer destination is available through
`bash install-hollis.sh --bin-dir /absolute/directory`; the complete bundle
still lives under `~/.local/share/hollis/`.

</details>

## First check

```bash
hollis doctor --model local
hollis respond --model local "Reply with OK"
```

The first command checks prerequisites without asking a model or using
Shortcuts. The second asks the free native Local model on your Mac.
A successful answer confirms that inference works. If the check fails,
finish Apple's downloads in System Settings and follow the command's next step.

<!-- media: local-first-request.gif — Terminal check and first Local request -->

## Add the Apple Shortcuts bridges

Skip this section if you only want Local. Cloud, Cloud Pro, On-Device,
ChatGPT and image generation use small Apple Shortcuts called bridges.
Cloud, Cloud Pro and ChatGPT need an internet connection. For ChatGPT,
enable its extension in System Settings > Apple Intelligence & Siri.

```bash
hollis setup --import
```

Confirm the list in Terminal. In each Apple window, choose **Add Shortcut**.
This puts a bridge in your Shortcuts library; it does not ask a model.
There are nine bridges: a text and a JSON bridge for each of the four text
routes, plus **Hollis Image - Reference Input v2** for images. The command
skips bridges it can already find and preserves custom image-bridge settings.
If the installer already opened the windows, finish adding those first.

<!-- media: shortcuts-add-shortcut.gif — Add Shortcut window during setup -->

On the first request through a bridge, macOS may show **Allow** windows.
They ask permission for that Shortcut to use Apple's model or pass the
supplied content. Read the request and approve it only if you want to proceed.
Keep your Mac unlocked for image generation. First-use approvals on a fresh
macOS account have not been tested.

Check what Hollis can find:

```bash
hollis doctor
```

This is an overview, not a model test. Missing optional bridges do not stop
Local from working. To check one route with a failing exit status when it is
not ready, use `hollis doctor --model cloud`.

<details>
<summary>Add only selected routes, or replace an older bridge</summary>

```bash
hollis setup --import cloud chatgpt
```

Each text route includes its JSON bridge. Rerun setup to add other routes later.
For replacement imports, see [import details](cli.md#explicit-shortcut-imports).
Keep only one copy of each bridge name in Shortcuts. Replace an older image
bridge with **Hollis Image - Reference Input v2**.

</details>

## Everyday use

The filenames below are examples. Replace them with your own files. Each
request sends the supplied content to the selected model.

### Ask a question

```bash
hollis respond --model local "What is a closure in programming?"
```

`respond` does not remember earlier requests. [More input options](cli.md#everyday-use).

### Chat

```bash
hollis chat --model local
```

Type a question and press Return. Ask a follow-up in the same session.
Type `help` for commands, or `quit` to leave. Chats are saved on your Mac.
To resume one, list chats and replace `CHAT_ID` with an ID from that list:

```bash
hollis chats list
hollis chat --continue CHAT_ID
```

Continued chats keep their original model. [Saved chats](cli.md#persistent-chats).

### Choose a model

```bash
hollis respond --model cloud-pro "Compare two ways to organize a small project."
```

Use `local`, `cloud`, `cloud-pro`, `on-device` or `chatgpt`. Cloud and Cloud
Pro are distinct Private Cloud Compute selections. Apple does not publish
stable backend IDs for them. The default `auto` starts with Cloud, not Local.
Save a preference if you want to omit `--model` in future requests:

```bash
hollis config set model local
```

Explicit models never silently switch after a failure. Apple controls cloud
rate limits. Hollis cannot show the remaining allowance or predict recovery.
Pause after a limit instead of repeatedly retrying. [Models and limits](overview.md#model-details).

### Summarize a document or PDF

```bash
hollis respond --model local --file notes.txt "Summarize these notes."
hollis respond --model local --file report.pdf --pages 1,4-6 "Summarize these pages."
```

Hollis reads text and extracts PDF text on your Mac, using OCR for scanned
pages. Extraction and model answers can contain errors; check important
numbers against the original. Choosing a cloud model sends the extracted text
to that route. Large documents may not fit; select fewer pages or use smaller
files. [Document limits](cli.md#text-documents).

### Understand an image

```bash
hollis respond --model local --image photo.jpg "Describe this image."
```

Local accepts one PNG or JPEG when Apple's installed model supports vision.
Cloud and Cloud Pro accept up to three; ChatGPT accepts one. Shortcuts
On-Device does not support image input in Hollis. [Image input](cli.md#images).

### Generate an image

```bash
hollis image generate "A brass turtle carrying a tiny greenhouse" \
  --style illustration --output turtle.png
```

This uses the image bridge, not the Local text model. Keep your Mac unlocked.
Choose a new output filename in an existing folder. Styles are `animation`,
`illustration`, `sketch`, `genmoji` and `any`. Any Style does not guarantee a
photograph. [Images, sizing and references](image-generation.md).

### JSON output

For a machine-readable envelope around an answer:

```bash
hollis respond --model local --json "Name two uses for a paperclip."
```

To ask a Shortcut model for a JSON object:

```bash
hollis respond --model cloud --format json 'Return an object with key ready.'
```

`--json` wraps the answer; `--format json` requests and validates an object.
Shortcut routes do not enforce requested fields, types or values. Invalid JSON
fails explicitly, without repair or retry. For guided structure on Local,
use a schema file with [Local guided output](native-local.md#guided-output).

### Batch

Put your documents in `inbox` and write the task in `instructions.txt`.
Planning previews work without calling a model:

```bash
hollis batch plan --input-dir ./inbox --prompt-file instructions.txt \
  --model local --output-dir ./results --job ./job.json
```

Review the plan, then allow at most three requests:

```bash
hollis batch run --job ./job.json --max-calls 3
hollis batch resume --job ./job.json --max-calls 3
```

Use `resume` only for unfinished work. A failure stops the run. Each run or
resume has its own call budget. [Batch guide](batch.md).

### Local API server

Other apps can use Hollis through an OpenAI-compatible API. Create a private
token file and start the server in Terminal:

```bash
umask 077
openssl rand -base64 48 > hollis.token
hollis serve --token-file hollis.token
```

Use `http://127.0.0.1:1978/v1` as the app's base URL, the file's contents as
its API key and `local` as its model. Keep the token private. The server stays
in this Terminal; press Control-C to stop it. Shortcuts routes require complete
responses, so turn off streaming when using them. [API settings and authentication](api.md).

## Updating and uninstalling

Close running Hollis chats and servers before updating. Back up chats you
want to keep. Newer versions can upgrade the database; replacing the binary
with an older version does not undo that change.
[Database backup and recovery](cli.md#database-upgrades-and-recovery).

```bash
hollis update
```

This checks GitHub for the latest stable release and prints upgrade guidance.
It runs only when you ask. It does not install anything by itself.
For Homebrew:

```bash
brew upgrade kamenxrider/tap/hollis
```

For the script install, download, inspect and run the script again as above.
It replaces older plain-file executables or its own links. Earlier version
folders remain in `~/.local/share/hollis`; remove unused versions in Finder
only after closing processes that use them. An existing same-version folder
with different bytes is refused; move that folder aside before reinstalling.

To uninstall Homebrew's copy:

```bash
brew uninstall hollis
```

To uninstall the script's copy, remove its two links (or old plain files):

```bash
rm -i "$HOME/.local/bin/hollis" "$HOME/.local/bin/hollis-native"
```

Then in Finder, use Go > Go to Folder and enter `~/.local/share/hollis`.
Move that folder to the Trash after checking that no other Hollis installation
uses it. For a custom `--bin-dir` or an older `/usr/local/bin` installation,
remove the two executables from that directory instead.

Chats and settings remain in `~/Library/Application Support/hollis`, or your
chosen `HOLLIS_STATE_DIR`. Back up anything you want before deleting that
folder in Finder. Remove the plugin through its host's plugin manager.
Delete imported Hollis shortcuts in Shortcuts only if no remaining installation
uses them. Generated images and batch outputs are separate files.
[Your data](privacy.md#your-data) · [Plugin state and removal](../plugins/hollis/docs/setup.md).

## The top 5 problems and their fixes

1. **Terminal says "command not found".** For a script install, run
   `export PATH="$HOME/.local/bin:$PATH"` and make it permanent as described
   above. Check `hollis --version` in the new window. An older copy earlier on
   PATH may need removing.
2. **Local is unavailable.** Run `hollis doctor --model local`. Enable Apple
   Intelligence, finish downloads and check macOS and hardware requirements.
   If the helper is missing or mismatched, reinstall the matched bundle from
   the same installation source. Do not mix CLI and helper versions.
3. **A Shortcut bridge is missing or an approval window is waiting.** Run
   `hollis setup --import`, finish Add Shortcut, then run `hollis doctor`.
   Read and approve first-use Allow prompts. Remove duplicate bridge names.
   If setup cannot find the bundle manifest, reinstall the complete bundle.
4. **Cloud reports a limit or fails.** Stop and wait. Apple's allowance and
   reset time are unknown. Check your connection and ChatGPT extension
   settings when relevant. To work offline, explicitly select `--model local`
   for a new request; Hollis will not switch an explicit cloud request for you.
5. **A file is too large, JSON fails or an answer looks wrong.** Use fewer PDF
   pages or a smaller document, and check the original. Clarify the request.
   For typed JSON fields, use Local with a supported schema. No route
   guarantees factual correctness. [Errors and next actions](errors.md).

[Reference docs](overview.md#reference) · [What leaves your machine](privacy.md)
