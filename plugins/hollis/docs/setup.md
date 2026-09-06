# Guided setup

Install the package in your host using the [README](../README.md). The package
has no automatic install hook: ask its setup skill to begin. Claude names it
`/hollis:hollis-setup`, and the main skill `/hollis:hollis`. Codex exposes
`hollis:hollis-setup` and `hollis:hollis`; invoke them with
`$hollis:hollis-setup` and `$hollis:hollis`, or select them in the skill picker.
These names were checked in the installed hosts; see [validation](validation.md).

The agent handles these steps:

1. Check local hardware, macOS and access to Shortcuts. If necessary, guide you
   to **System Settings → Apple Intelligence & Siri** to enable Apple Intelligence
   and wait for Apple's required downloads. A restricted check means unknown
   access, not incompatible hardware.
2. Verify and install the runtime under
   `~/Library/Application Support/hollis/plugin/versions/0.3.0/`.
   A newer existing Hollis is preserved and used. The plugin's bridge kit remains
   available. No PATH modification is needed.
3. Offer all five bridges and import selected ones individually. Approve
   **Add Shortcut** in Shortcuts. Existing discovered bridges and custom choices
   are preserved; skipping one does not disable another.
4. Verify discovery and return to your task. The first requested inference can
   require Apple's **Allow** prompt or acceptance of provider terms. You make
   those decisions. A successful response establishes that route's readiness.

If setup is interrupted, ask to continue. Completed runtime/bridge steps are
recorded locally; pending imports are checked again before opening anything.
Setup does not pretend to enable Apple Intelligence or approve policies itself.
Readiness checks are read-only and can run concurrently. A host sandbox can
still block Shortcuts discovery or runtime writes: permit the relevant local
operation using that host's controls. This is separate from Apple's approvals.

Before installation, `setup.sh status <route>` reports `setup_required` and does
not create runtime state. After a managed installation or check, read the rollback receipt
alongside the main status:

| Rollback status | Meaning |
|---|---|
| `available` | `version` identifies a prior runtime verified at this check; rollback verifies it again before switching |
| `unavailable` | `version` identifies a known candidate when possible and `message` explains why its bytes cannot be selected |
| `none` | `version` is null and `message` says no rollback candidate is recorded |

An upgrade may repair a previously corrupt older runtime instead of blocking a
verified new install. `setup.sh rollback` still checks the old bytes and leaves
the working runtime selected when the candidate is unavailable. Configuration,
conversations and bridges are preserved in either case.

For troubleshooting, resolve `KIT` to the installed `plugins/hollis` directory:

```sh
bash "$KIT/scripts/setup.sh" check
bash "$KIT/scripts/setup.sh" install
bash "$KIT/scripts/setup.sh" status cloud
bash "$KIT/scripts/setup.sh" import cloud
bash "$KIT/scripts/run.sh" agent-context
```

The helpers use only macOS utilities. A downloaded archive contains the runtime;
source installations fetch its exact locked assets over HTTPS. Each managed
binary is hash-checked again before execution. `setup.sh path` returns its
absolute path. A pre-existing newer executable on PATH remains part of your
existing installation's trust boundary; it is not falsely described as our pin.

Updates keep the previous managed version. `setup.sh rollback` deliberately
switches back to it without replacing configuration, conversations or bridges.
Removing a host plugin also leaves those files and generated images intact.
No automatic downgrade, quarantine removal or privileged installation occurs.
