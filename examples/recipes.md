# Hollis job recipes

`hollis examples` prints this guide; it does not run commands, read config or
write files. Copy a recipe only when you want to run it. Each recipe makes one
model request. Cloud recipes send their text through the configured Cloud
Shortcut; local uses the matched native helper on Apple Silicon/macOS 27.
Check the route with `hollis doctor --model cloud` or `--model local` first.
Stop on failure or throttling; these recipes do not retry or change models.

Use a scratch directory: the file recipes create or replace `notes.md`,
`reverse.go` and `person.json`. The chat recipe writes your local Hollis chat
history. Bash or zsh can run the blocks. Replace the synthetic input with your
own text deliberately. The recipes save generated code for review and never
execute it.

## Summarize meeting notes

```sh
printf '%s\n' 'Release notes: tests pass. Documentation needs a final check.' > notes.md &&
printf '%s\n' 'Summarize these notes in two bullets.' | hollis respond --model cloud --file notes.md
```

## Draft a reply

```sh
hollis --model cloud 'Draft a polite reply: I can review the document on Thursday, not Wednesday.'
```

## Explain an error

```sh
printf '%s\n' 'Explain this Go error and suggest one fix:' 'cannot use "3" as int value in assignment' | hollis respond --model cloud
```

## Suggest a commit message

```sh
printf '%s\n' 'Suggest a concise commit message for this synthetic change:' 'Added a test for empty input and return a clear error instead of panicking.' | hollis respond --model cloud
```

## Review a small diff

```sh
printf '%s\n' 'Review this synthetic diff for correctness:' '--- before.go' '+++ after.go' '@@ -1 +1 @@' '-return items[0]' '+if len(items) == 0 { return "" }; return items[0]' | hollis respond --model cloud
```

## Save code for review

```sh
hollis respond --model cloud --code 'Write a Go function Reverse that reverses a slice of strings in place. Put the function in a fenced Go code block.' > reverse.go
```

## Request a structured local answer

```sh
printf '%s\n' '{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string"}},"required":["name"]}' > person.json &&
hollis respond --model local --schema person.json --agent --select response 'Invent one fictional person.'
```

## Save and find a conversation

```sh
hollis chat --model cloud --no-input 'Suggest two release checklist items.' &&
hollis chats list &&
hollis chats search 'release checklist'
```

The offline test suite executes every shell block above verbatim against
synthetic model runners in isolated directories. That checks CLI syntax,
pipes, files, output modes and storage, not model quality, permissions or live
route availability.
