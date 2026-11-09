# Run the local API in the background

`hollis serve` runs in the foreground until it receives Ctrl-C, SIGTERM or SIGHUP. To
keep the API available after login, run it as a per-user LaunchAgent. Hollis
ships a template at
[`packaging/launchd/io.github.kamenxrider.hollis.serve.plist`](../packaging/launchd/io.github.kamenxrider.hollis.serve.plist);
nothing installs or loads it for you.

The template uses these default settings:

- It binds to loopback, `127.0.0.1:1978`, and does not pass `--allow-remote`
  or `--no-auth`.
- It reads the bearer token from a private file you create. The plist holds
  only the file's path, never a token value.
- It runs as your user in your login session, which is where Shortcuts and the
  on-device model are available.

These steps change only files in your home directory and your own launchd
session. They make no model calls.

## 1. Create a private token file

```sh
mkdir -p ~/Library/Application\ Support/hollis
( umask 077; openssl rand -base64 48 > ~/Library/Application\ Support/hollis/serve.token )
```

The token must be at least 32 bytes. `hollis serve` refuses a token file that is
a symlink or that group or others can read or write, so keep it at mode `600`.
Clients send it as `Authorization: Bearer <token>`; see the
[API guide](api.md) for request examples.

`hollis serve` reads the token only from `--token-file` or `HOLLIS_API_TOKEN`;
`HOLLIS_STATE_DIR` does not change where it looks, and a LaunchAgent does not
inherit your shell's variables. If you keep the token file somewhere else, edit
the `--token-file` path in the plist to match.

## 2. Install the LaunchAgent

The template uses `/Users/you` as a placeholder home directory and expects the
default installer location, `~/.local/bin/hollis`. Fill in your home directory:

```sh
mkdir -p ~/Library/LaunchAgents
sed "s|/Users/you|$HOME|g" packaging/launchd/io.github.kamenxrider.hollis.serve.plist \
  > ~/Library/LaunchAgents/io.github.kamenxrider.hollis.serve.plist
plutil -lint ~/Library/LaunchAgents/io.github.kamenxrider.hollis.serve.plist
```

If `command -v hollis` prints another path, for example after
`install.sh --bin-dir` or a Homebrew install, put that absolute path in the
first `ProgramArguments` entry. launchd does not search your shell `PATH`.

To use another port, change the `--addr` value. Keep a loopback address
(`127.0.0.1` or `::1`). Binding elsewhere needs `--allow-remote` and an
encrypted trusted path; see
[authentication and remote access](api.md#authentication-and-remote-access)
before changing it.

## 3. Load, check and unload

Load it now and at every login:

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/io.github.kamenxrider.hollis.serve.plist
curl -s localhost:1978/health
```

Inspect its state, last exit status and process ID:

```sh
launchctl print gui/$(id -u)/io.github.kamenxrider.hollis.serve
```

Restart it after you change configuration, replace the token, set up images or
upgrade Hollis:

```sh
launchctl kickstart -k gui/$(id -u)/io.github.kamenxrider.hollis.serve
```

Stop it and keep it from starting at the next login:

```sh
launchctl bootout gui/$(id -u)/io.github.kamenxrider.hollis.serve
rm ~/Library/LaunchAgents/io.github.kamenxrider.hollis.serve.plist
```

`bootout` stops the service but does not disable it: while the plist stays in
`~/Library/LaunchAgents`, launchd loads it again at the next login. Removing the
plist file makes the stop permanent. The token file is separate; delete it yourself if you no
longer need it.

## Restart behavior

`RunAtLoad` starts the server when the agent loads. `KeepAlive` with
`SuccessfulExit` set to false restarts it only after it exits with an error.
A clean SIGTERM shutdown, which is what `bootout` sends, exits 0 and is not
restarted. `ThrottleInterval` waits at least 30 seconds between starts, so a
configuration error, such as a missing token file or a port already in use,
does not restart in a tight loop.

## Logs

Standard output and standard error both go to
`~/Library/Logs/hollis-serve.log`. Console.app also shows that folder under
**Log Reports**. Each start writes one line, for example
`hollis serve listening on http://127.0.0.1:1978 (GET /health)`, and a failed
start writes its error. Hollis does not log requests, prompts or responses
there, and Hollis never prints the token.

launchd appends to the file and does not rotate it. Delete or truncate it when
you like; it is recreated at the next start.

## Why not an environment variable?

`hollis serve` also accepts the token through `HOLLIS_API_TOKEN`, which suits a
foreground run from a shell where you supply the value. For a LaunchAgent, the
value would have to be written into the plist's `EnvironmentVariables` or set
with `launchctl setenv`, which exposes it to every process in your session. The
template uses a token file to keep the secret out of both.

[Back to the API guide](api.md) · [Back to Hollis](../README.md)

With `--no-auth`, only Host `localhost`, `127.0.0.1` or `[::1]` (with an optional port) is accepted, and any Origin must also be loopback. Foreign values get 403 to resist DNS rebinding. This does not authenticate local processes; prefer the default token mode.
