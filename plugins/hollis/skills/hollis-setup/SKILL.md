---
name: hollis-setup
description: Install, check, repair or update the Hollis plugin's local Mac runtime and selected Apple Shortcuts bridges. Guides first-use Apple approvals, preserves existing Hollis data and verifies pinned assets without requiring development tools.
---

Get the user from this conversation to a useful Apple answer. Resolve `KIT` two directories above this skill's actual installed SKILL.md, and use absolute script paths. Installation is part of the experience, not a list of commands to hand off unnecessarily.

## First use

1. Run `bash "$KIT/scripts/setup.sh" check`. Explain unsupported hardware/OS or restricted discovery accurately. Before installation, bridge status returns `setup_required` without creating runtime state. The supported route requires a local Apple-silicon Mac on macOS 27, Apple Intelligence enabled, and Shortcuts for bridge routes. Native Local requires no Shortcut. Cloud/Cloud Pro/ChatGPT need network; image generation needs an unlocked session. No private settings database is used to guess whether models are ready.
2. Briefly explain: Cloud and Cloud Pro use Apple's Private Cloud Compute through **Shortcuts Use Model**. This is separate from the native Foundation Models developer API. This agent receives the submitted context and result. Hollis testing observed no separate Apple model charge, with rate limits; host-agent charges/policies remain separate. ChatGPT is a distinct extension route.
3. Run `bash "$KIT/scripts/setup.sh" install` within the user's installation request. The helper verifies pinned assets, installs under user-owned storage and preserves existing state. An `existing_newer` result means use the newer executable returned by `setup.sh path`; do not downgrade or modify PATH.
4. Offer Cloud, Cloud Pro, native Local, On-Device, ChatGPT and image generation together. Native `local` uses the bundled SDK helper and requires no Shortcut import; check it with `setup.sh status local`. If the user already chose capabilities, use that choice. Explain that skipping a bridge leaves that capability unavailable but does not block others. For each selected route, run `setup.sh import <cloud|cloud-pro|on-device|chatgpt|image>` separately.
5. **Before opening an import**, tell the user that Shortcuts may ask **Add Shortcut**, followed on first use by **Allow**. Honor those decisions; never click policy approvals automatically or suppress macOS protections. `permission_pending` means wait for the user, then rerun the same import command to verify discovery. Do not repeatedly open it while waiting.
6. Run `setup.sh status <route>` for the selected route. Discovery is not inference proof. On a user's authorized first task, call that route through `run.sh` and report its actual outcome. Do not spend calls probing every optional model during routine setup.

## Recovery and upgrades

- The installer is resumable: rerun `install`, then only unfinished bridge imports. It never overwrites user configuration wholesale. A configured but unverified custom bridge needs diagnosis or explicit replacement, not automatic duplicate installation.
- A pending import does not open again automatically. If the user closed it and now requests another attempt, use `setup.sh import <route> --reopen` once.
- Installer and inference locks prevent overlapping operations. If an interrupted operation leaves a lock, inspect its recorded PID and confirm no operation is running before removing only that stale lock. Never clear locks blindly.
- A runtime upgrade retains the previous version, including corrupt files. Managed install/check results include `rollback` with `status`, `version` and `message`: `available` means the candidate was verified at that check; `unavailable` explains why it cannot be selected; `none` means no candidate is recorded and `version` is null. A repair upgrade may proceed when the older runtime is corrupt. `setup.sh rollback` verifies again immediately before switching; a refused rollback leaves the current runtime selected. Configuration, conversations and bridges are preserved. A newer installed runtime is not automatically downgraded to this plugin's pin.
- Re-run `check` after a host/plugin update. Use `doctor --json` through `run.sh` for diagnosis, parsing nonzero-exit JSON too. A Shortcuts helper error is unknown access, not proof of missing bridges.
- Rate limit: stop that sequence. Timeout or cancellation: an outcome can be uncertain; investigate before any retry. Image generation must remain unattended after first-use approvals; do not automate the visible Playground app.
- Removing the plugin from a host does not delete Hollis conversations, configuration or generated files. Do not erase that data as part of troubleshooting or uninstall without explicit permission.

The 0.2.0 release archive bundles the ARM64 binary, matching native helper and five bridges. Source installs download the exact pinned release assets. Packaging verifies their provenance; installation compares bytes against the trusted plugin's lock. This does not claim Developer ID signing, notarization or independent attestation of a specific Apple inference server.
