# Hollis plugin quick start

Use Hollis inside Claude Code or Codex on an Apple-silicon Mac running macOS
27 or later. Enable Apple Intelligence in System Settings and finish its
downloads first. Install and sign in to your chosen agent host. It must run
commands on this Mac; a remote Linux agent cannot use these models.

Plugin 0.4.0 uses runtime 0.7.1, its matched native helper and nine bridges.
You do not need a separate Hollis CLI installation, server or model API key.
Setup uses macOS utilities; it needs no Homebrew, `gh`, Go, Python, Node,
shell startup changes or administrator installation.

## Claude Code

1. In a Claude Code conversation, enter these commands one at a time:

   ```text
   /plugin marketplace add kamenxrider/hollis
   /plugin install hollis@hollis-plugins
   ```

   Choose the installation scope Claude asks for. If its install summary asks
   for `/reload-plugins`, run it. Otherwise start a new session if the skills
   do not appear.
2. In that conversation, run setup:

   ```text
   /hollis:hollis-setup
   ```
3. After setup, make a first request with the on-device Local model:

   ```text
   /hollis:hollis Use native Local to explain what a closure is in programming.
   ```

Then continue in the same conversation: "Ask it for a short example."

## Codex

1. In Terminal on this Mac, run:

   ```sh
   codex plugin marketplace add kamenxrider/hollis
   codex plugin add hollis@hollis-plugins
   ```

   If `codex plugin` is unavailable, update to a plugin-capable Codex version.
2. Start a new Codex conversation. Type this there, not in Terminal:

   ```text
   $hollis:hollis-setup
   ```

   You can also choose `hollis:hollis-setup` in the skills picker.
3. After setup, ask:

   ```text
   $hollis:hollis Use native Local to explain what a closure is in programming.
   ```

Continue in the same conversation: "Ask it for a short example."

## What setup asks you to click

Choose the capabilities you want: Cloud, Cloud Pro, On-Device, native Local,
ChatGPT or images. Local needs no Shortcut. You can skip capabilities and
add them later by running setup again.

For Shortcut routes, setup explains the permissions before opening windows.
Choose **Add Shortcut** to add each bridge to your Shortcuts library.
The first request may ask you to **Allow** the Shortcut to use Apple's model
or pass supplied content. Read the request before approving. Keep your Mac
unlocked for image generation. ChatGPT also needs its extension enabled in
System Settings > Apple Intelligence & Siri.

<!-- media: plugin-setup.gif — Capability selection and Add Shortcut during plugin setup -->

Setup preserves existing bridges, custom settings and chats. If interrupted,
ask it to continue. It checks installation and bridge discovery without asking
a model. Your first successful answer or image confirms that capability.
First-use permissions on a fresh macOS account have not been tested.

Setup checks the pinned runtime contents. The release's checksums and GitHub
build provenance cover its origin; the binaries are not Developer ID signed or
notarized. [Privacy and integrity](privacy.md) · [Setup and recovery](setup.md).

## Choose a model and continue

Name the model in the request, such as "Use Cloud Pro" or "Use On-Device."
Without a choice, the plugin uses your existing concrete preference, then
Cloud. It does not switch models after a failure.

Apple controls cloud rate limits. Pause after a limit; the remaining allowance
and recovery time are unknown. Your agent host receives the context it sends
and Apple's answer. A cloud-hosted agent provider receives that too, under its
own privacy policy. Hollis adds no inference charge; the host has its own costs.

Follow-ups send relevant context back to the selected route. The answer appears
inline with its model label; the plugin does not automatically save another
copy of the whole host conversation. Generated images include an absolute
Open image link and a preview where the host supports it.
[Everyday requests](usage.md) · [Problems and fixes](troubleshooting.md).

<details>
<summary>Install from a local source checkout</summary>

Give the host the repository root, which contains the marketplace entries and
`plugins/hollis/`. Do not give marketplace installation the inner plugin folder.
Keep the source checkout available for later local marketplace updates.
Replace the illustrative path below with your checkout.

In Claude Code:

```text
/plugin marketplace add /absolute/path/to/hollis
/plugin install hollis@hollis-plugins
```

In a Terminal for Codex:

```sh
codex plugin marketplace add "/absolute/path/to/hollis"
codex plugin add hollis@hollis-plugins
```

Source setup downloads the exact checksum-pinned runtime assets; it does not
need the plugin release archive. A built release archive includes those assets.
Only select a branch or tag with Codex's `--ref` when that ref exists.

</details>
