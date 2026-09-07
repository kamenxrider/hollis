# Troubleshooting

`setup.sh status` reports `runtime_path` (the selected executable) and
`config_path` (the configuration path reported by that executable), separately
from the plugin's `runtime_home`. Check these after a host restart. A null
config path is unverified, not a guessed default. Persist `HOLLIS_PLUGIN_HOME`,
`HOLLIS_STATE_DIR` and any intentional `HOLLIS_CALLER_PATH` in the test launcher
or scoped host settings when using an isolated test installation.

`discovery.status: completed` means Shortcuts returned a listing;
`listed_shortcuts: 0` means that listing was empty. A `failed` listing keeps
readiness `unknown` and records its exit code. It does not prove missing
bridges or identify sandbox, helper or GUI-session failure without additional
evidence. `not_attempted` means configuration resolution failed first.

With runtime 0.3.2, `request_declined` means Apple asked for a different
description without specifying the cause. `shortcut_failed` means the Shortcut
execution failed for an unrecognized reason. Both retain CLI exit 5. Neither
is a reason to reinstall a discovered bridge or silently retry another model.
Preserved 0.3.1-backed archives still use the older error codes; updating
documentation alone does not upgrade their bundled binary.

| Outcome | Next action |
|---|---|
| Marketplace cannot find Hollis | Point marketplace add at the repository or extracted archive root containing both the marketplace entry and `plugins/hollis`; the inner plugin folder is not the marketplace root |
| Skills absent after installation | In Claude follow the install summary's reload instruction or start a new session; in Codex start a new conversation and check the skills picker. Verify the host installed the intended package |
| Setup required | `status` before installation returns `setup_required` without creating state; run the setup skill to install the runtime and selected bridges |
| Unsupported system | Use a local eligible Apple-silicon Mac on the supported macOS route |
| Hardware/helper access unknown | Retry the diagnostic from the local host with permitted access; do not conclude the bridge is missing |
| Runtime filesystem access required | Permit the local operation in the host sandbox; this is not a stale lock and does not call for reinstalling bridges |
| Add Shortcut pending | Complete the visible import, then rerun that route's import check. If you closed it and want another attempt, explicitly use `setup.sh import <route> --reopen` |
| Bridge discovered and configured | Make the requested model call; discovery alone does not establish inference |
| Image discovered, `configured:false` | Ask setup to connect the existing image bridge with `setup.sh import image`; it need not reinstall that Shortcut |
| Custom bridge unverified | Preserve it; inspect its exact name/UUID and Shortcuts availability before changing configuration |
| Integrity failure | Stop; reacquire the pinned package from its trusted distribution and investigate the changed asset |
| macOS blocks the executable | Follow macOS's supported first-open process; do not automatically remove quarantine or bypass Gatekeeper |
| Apple Intelligence not ready | Check System Settings and Apple's downloads, account/region availability and provider first-use policy |
| Permission rejected | Explain the requested action and relevant macOS settings; do not keep reopening or clicking approvals |
| Locked session | Unlock the Mac before image generation; no visible editor automation fallback |
| Timeout/cancellation | The result may be uncertain; inspect the output and running operation before considering a retry |
| Rate/daily limit | Stop the affected sequence. Preserve results and resume only after investigating the limit |
| Installation/inference lock | Inspect the lock's PID and confirm the operation ended before removing only that stale lock |
| Rollback `available` | The receipt names a verified prior `version` and includes a human-readable `message`; rollback checks it again before switching |
| Rollback `unavailable` | The receipt names a candidate `version` when known and explains the integrity or availability problem; keep the current runtime and investigate |
| Rollback `none` | The receipt has `version: null` and explains that no candidate is recorded |
| Repair upgrade | A corrupt older runtime may be retained but not selected; the verified new runtime can still be installed, while rollback stays fail-closed |
| Oversized input | Narrow the selected context or attachments; never silently truncate |
| Wrong/unavailable style | Use one of the five tested styles; do not route ChatGPT generation to another model silently |

Ask the agent for the relevant receipt or `doctor --json` output when needed.
The main answer should remain readable. Keep diagnostics private unless you
choose to share them; even prompts in otherwise harmless test logs can contain
personal or project information.

`HOLLIS_PLUGIN_HOME` can relocate the private runtime state to an absolute,
user-owned directory without symlink components. `HOLLIS_STATE_DIR` belongs to
the existing runtime and relocates its configuration/conversations. Neither is
needed for normal installation. Do not delete either directory to fix a problem.

With runtime 0.3.1, invalid image-reference diagnostics distinguish `file does
not exist`, `permission denied` and other `file access failed` errors without
printing the path. Check the attachment locally and select an accessible regular
PNG or JPEG; invalid references do not dispatch an Apple request. The preserved
0.3.0 archive lacks this runtime fix; the current package pins verified
0.3.3 assets and includes it.
