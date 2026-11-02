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
   `~/Library/Application Support/hollis/plugin/versions/0.4.0/`.
   A newer existing Hollis is preserved and used. The plugin's bridge kit remains
   available. No PATH modification is needed.
3. Native `local` requires the matching helper and no bridge; run `setup.sh status local`. Offer all five bridges and import selected ones individually. Approve
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

## Route-scoped status and diagnostics

`setup.sh status <route>` is scoped to the route you name. A current runtime
returns a v1 plan with `schema_version`, `route`, `status` (`ready`,
`import_required`, `approval_required`, `model_not_ready` or `unknown`), a stable
`reason_code`, explicit `actions` and `inference_tested:false`; an older runtime
falls back to the legacy view marked `"source":"legacy"`. Plain
`setup.sh status` summarizes every route. The matching CLI diagnostic is
`doctor --model <route> --json`, which returns the same plan plus the observed
facts for that route; plain `doctor --json` keeps the full bridge table and the
`local` row. Read `status` and `reason_code` together: discovery or installation
is not inference proof, and no status claims a successful model call.

The native helper is matched per component. The receipt pins its protocol and the
plugin compares it with the helper's own `--protocol-version`; the runtime record
reports `helper_protocol_mismatch` or `helper_version_mismatch`, and a
selected-route plan reports `helper_mismatch`. All three call for the matching
bundle, not a reinstall of bridges. Native `local` needs that match;
selected Shortcut routes remain usable with a helper mismatch. Guided output
(`--schema`) and the measured `context_size` diagnostic additionally require
helper 0.5.0/protocol 2: `models --model local --json` reports
`helper_protocol: 2` and `capabilities.guided_output: true` when the runtime
supports them, and a protocol-1 helper is refused before inference. Never
promise those features from the plugin version alone.

## Where files live

| Location | Purpose |
| --- | --- |
| Host's installed plugin directory | Skills, scripts, manifests and bundled assets; the host may cache a copy |
| `~/Library/Application Support/hollis/plugin/` | Managed runtime versions, bridge kit, setup receipts and pacing records |
| `~/Library/Application Support/hollis/` | Hollis configuration and saved conversations by default |
| Output path selected for a generation or batch | Your resulting images/documents |

Both hosts normally use the same Hollis installation and Mac-account Shortcuts.
Installing a plugin copy does not create isolated Hollis conversation storage.
`HOLLIS_PLUGIN_HOME` overrides the managed-runtime location;
`HOLLIS_STATE_DIR` overrides runtime configuration/conversations. They are
independent, and ordinary installation needs neither.

For an isolated test, persist both exact paths in the test launcher so they
survive a host restart. `HOLLIS_CALLER_PATH` optionally controls discovery of an
external executable; it is not a state-directory setting. With current source,
`setup.sh status` reports the selected `runtime_path` and the executable's resolved
`config_path`. Verify those paths before configuring a test installation.
Discovered Shortcuts are shared at Mac-account level and do not prove which
Hollis state directory is in use. [More diagnostic detail](troubleshooting.md).

## Manual diagnosis

The setup skill normally handles commands. For manual diagnosis, set `KIT` to
the **actual installed plugin root**, not its `skills/` directory or an unrelated
source copy. These checks do not request Apple inference:

```sh
KIT="/absolute/path/to/installed/plugins/hollis"
bash "$KIT/scripts/setup.sh" check
bash "$KIT/scripts/setup.sh" status cloud
```

Once a runtime is installed, inspect its identity, resolved state and commands:

```sh
bash "$KIT/scripts/setup.sh" path
bash "$KIT/scripts/run.sh" --version
bash "$KIT/scripts/run.sh" config show --json
bash "$KIT/scripts/run.sh" agent-context
```

Installation and imports are separate actions. `setup.sh install` installs the
locked runtime and bridge kit; `setup.sh import cloud` connects/imports that route.
Use `cloud-pro`, `on-device`, `chatgpt` or `image` for another selected route.
An import may open Shortcuts for **Add Shortcut**. Rerun the same import after
approval to verify it; do not repeatedly reopen a pending prompt. A discovered
image bridge with `configured:false` still needs `setup.sh import image` to
connect it to Hollis before use.

The helpers use only macOS utilities. A downloaded archive contains the runtime;
source installations fetch its exact locked assets over HTTPS. Each managed
binary is hash-checked again before execution. `setup.sh path` returns its
absolute path. A pre-existing newer executable on PATH remains part of your
existing installation's trust boundary; it is not falsely described as our pin.

## Updates and removal

Install the new plugin package through the host, load its updated skills, then
ask the setup skill to check/update Hollis. A new skill file alone does not
replace the runtime. Local marketplace copies may need refresh/reinstallation;
check the installed package path and version rather than assuming the host is
reading the checkout you edited.

Runtime updates keep the previous managed version. `setup.sh rollback` deliberately
switches back to it without replacing configuration, conversations or bridges.
This is executable rollback, not data rollback: it does not revert the database
schema or restore an old backup. An older executable may refuse a database
created by a newer runtime. Do not silently restore a pre-migration backup,
because that can discard newer conversations.
Removing a host plugin also leaves those files and generated images intact.
No automatic downgrade, quarantine removal or privileged installation occurs.
