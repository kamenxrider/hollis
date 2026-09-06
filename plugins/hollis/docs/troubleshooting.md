# Troubleshooting

| Outcome | Next action |
|---|---|
| Setup required | Run the setup skill; install the runtime and selected bridges |
| Unsupported system | Use a local eligible Apple-silicon Mac on the supported macOS route |
| Hardware/helper access unknown | Retry the diagnostic from the local host with permitted access; do not conclude the bridge is missing |
| Runtime filesystem access required | Permit the local operation in the host sandbox; this is not a stale lock and does not call for reinstalling bridges |
| Add Shortcut pending | Complete the visible import, then rerun that route's import check. If you closed it and want another attempt, explicitly use `setup.sh import <route> --reopen` |
| Bridge discovered | Make the requested model call; discovery alone does not establish inference |
| Custom bridge unverified | Preserve it; inspect its exact name/UUID and Shortcuts availability before changing configuration |
| Integrity failure | Stop; reacquire the pinned package from its trusted distribution and investigate the changed asset |
| macOS blocks the executable | Follow macOS's supported first-open process; do not automatically remove quarantine or bypass Gatekeeper |
| Apple Intelligence not ready | Check System Settings and Apple's downloads, account/region availability and provider first-use policy |
| Permission rejected | Explain the requested action and relevant macOS settings; do not keep reopening or clicking approvals |
| Locked session | Unlock the Mac before image generation; no visible editor automation fallback |
| Timeout/cancellation | The result may be uncertain; inspect the output and running operation before considering a retry |
| Rate/daily limit | Stop the affected sequence. Preserve results and resume only after investigating the limit |
| Installation/inference lock | Inspect the lock's PID and confirm the operation ended before removing only that stale lock |
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
