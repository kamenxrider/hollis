# What leaves your machine

Hollis reads only what you supply with an argument, `--prompt-file`, `--file`,
`--image`, `--system-file`, `--messages`, stdin, the API or your agent. It does not read your workspace on its
own. Where the request goes depends on the route you select.

| Route or feature | Where your prompt and attachments go |
| --- | --- |
| `local` | Stays on your Mac. Apple’s on-device model runs through the native SDK. |
| `on-device` | Stays on your Mac. Shortcuts runs Apple’s on-device model selection. |
| `cloud`, `cloud-pro` | Apple’s Private Cloud Compute, through Shortcuts. The rendered prompt, attached document text, chat history being replayed and images are sent. |
| `chatgpt` | Apple’s ChatGPT extension sends the request to ChatGPT under that extension’s terms. |
| `auto` | Cloud first; at most one fallback to Shortcuts On-Device. Never native local. |
| Image generation | Apple’s Image Playground, through your image Shortcut, with the prompt and any reference image. Apple decides where Image Playground processes the request; Hollis has not measured it. |
| PDF input | The helper extracts text on your Mac. Extracted text, not PDF bytes, travels with the prompt to whichever route answers, including Cloud, Cloud Pro and ChatGPT; `auto` follows its routing policy. Extraction alone does not make the request local. |
| MCP tools | Explicit `--mcp`/`--mcp-server` calls send approved arguments to the selected local program or HTTP server. Framed results go only to the local model and, for chat, its stored transcript. Servers are never attached implicitly or by `serve`/`batch`. See [MCP servers](mcp.md). |
| `hollis serve` | Listens on `127.0.0.1` by default. Requests go to the route they name, as above. |
| Agent plugin | Your agent host sees the context it sends and Apple’s answer. A cloud-hosted agent’s provider receives that too, under its own privacy policy. |

Configuration, chats and run history stay in a local state directory
([your data](#your-data)). Prompts use private temporary files,
removed after requests.

## Your data

Configuration (`config.json`), conversations and run history (`hollis.db`) live in
`~/Library/Application Support/hollis`, unless `HOLLIS_STATE_DIR` overrides it.
The plugin keeps its managed runtime under that directory’s `plugin` folder.
Prompts use private temporary files, removed after requests; abrupt termination can
leave files behind. [Transport and cleanup](shortcuts.md#how-it-works).

See [updating and uninstalling](getting-started.md#updating-and-uninstalling) for removal steps. Your chats and settings remain until you delete the state folder. Generated images and batch results stay at the paths you chose.
