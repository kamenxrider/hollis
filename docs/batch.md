# Folder processing

```bash
hollis batch plan --input-dir ./inbox --prompt-file instructions.txt \
  --model cloud --output-dir ./results --job ./job.json
hollis batch run --job ./job.json --max-calls 12
hollis batch resume --job ./job.json --max-calls 12
```

`plan` reads a sorted, nonrecursive inventory of `.txt`, `.md`, `.png`, `.jpg` and `.jpeg` files and records skipped entries. It makes no model calls. Instructions must be outside the input inventory; job and output destinations must be outside the input folder. Symlinks, existing reserved outputs, invalid text and oversized prepared prompts are rejected. The instruction and each text document together must fit 128 KiB. Image files have a 64 MiB per-file Hollis limit; planning checks readable bytes. Execution also validates PNG/JPEG content and a 64-million-pixel limit before calling a model.

Choose a concrete model explicitly. On-Device supports text-only jobs; image jobs use Cloud, Cloud Pro or ChatGPT. Every run or resume requires a new `--max-calls` budget from 1 to 100. A budget-limited run pauses cleanly. Output distinguishes attempts in this invocation from lifetime attempts. There is one call per file, no automatic model switching, and no automatic retry.

Cloud calls wait at least 30 seconds after completion before the next call; Cloud Pro waits 45 seconds and ChatGPT 15 seconds. On-Device and Local have no pacing. Apple rate-limits cloud requests per Mac without publishing the limit; we observed a block of about 17–24 hours after roughly 115 Cloud requests in 34 minutes. Pacing reduces the risk but cannot guarantee access. The pacing checkpoint survives a restart. Cancellation, a provider failure or a persistence failure stops further calls.

Plans show the spacing and reason in human output and as `spacing_seconds` and `spacing_reason` in JSON/agent output. `minimum_pacing_seconds` is the total inter-call waiting time for the planned items, excluding model runtime, not a completion-time prediction. Run and resume print one stderr notice when they first wait.

Resume verifies completed results and checks source hashes before continuing. If an interrupted call has no verifiable saved result, its status is **uncertain**. Choose `--retry-uncertain` to risk repeating that call, or `--skip-uncertain` to leave it unresolved and process pending items. Failed items require `--retry-failed` before more work proceeds. Exactly-once provider execution across a crash cannot be guaranteed. Changed inputs require a new plan.

Job manifests contain local paths, hashes and status, but no prompt or response content. Private `.response.json` files intentionally contain the model response and recovery metadata. Existing result files are never overwritten; the job manifest is updated atomically. An existing result directory must already be private (0700 or stricter); Hollis does not change its permissions. Inspect the manifest and results before sharing them. This is a finite command, not a background folder watcher.

Run and resume keep a stable `.lock` beside the manifest. Hollis rejects a job
location when that directory or one of its resolved ancestors is owned by an
untrusted local account, or is writable by other accounts without sticky-bit
protection. Put job manifests in a private directory you own; user-owned
directories beneath a standard sticky temporary directory remain supported.
On macOS, Hollis also checks extended ACLs on the lock and its directory chain.
Allow entries that grant mutation rights are rejected; read-only and deny
entries remain supported. An unreadable or unrecognized ACL fails closed.


[Back to Hollis](../README.md)

Native `local` also supports text-only batch jobs, with no inter-call pacing.
