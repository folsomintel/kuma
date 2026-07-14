# Kuma

Run coding agents from anywhere over a secure tunnel.

## Install

```bash
brew install folsomintel/kuma/kuma
```

Or grab a binary from [Releases](https://github.com/folsomintel/kuma/releases).

## Local Quick start

To run Kuma, you need to host the relay server, and install our CLI. You will need both [Go](https://github.com/golang/go) and [just](https://github.com/casey/just) installed.

**1. Setting up the relay server**
```bash
git clone https://github.com/folsomintel/kuma.git
cd kuma
just relay
# Or: KUMA_RELAY_AUTH_SECRET=dev-relay-secret go run ./cmd/kuma-relay
```

**2. Using the CLI**

```bash
export KUMA_RELAY_AUTH_SECRET=dev-relay-secret
export KUMA_RELAY_URL=ws://127.0.0.1:8080

kuma up      # Starts the daemon (kumad); creates config + remote "local"
kuma status  # Shows the status of the daemon
kuma run     # Pick an agent and start a session
```

`kuma up` writes config under your user config dir (e.g. `~/.config/kuma/` or `~/Library/Application Support/kuma/`) and registers a `local` remote when the auth secret is set.

Show credentials anytime:

```bash
kuma keys -S "$KUMA_RELAY_AUTH_SECRET"
```

## Commands

| Command                       | What it does                                                 |
| ----------------------------- | ------------------------------------------------------------ |
| `kuma up` / `down` / `status` | Start, stop, or inspect the local daemon                     |
| `kuma keys`                   | Print machine id + E2E key (use `-S` to sync remote `local`) |
| `kuma remote`                 | Manage remotes (interactive, or `add` / `list` / `remove`)   |
| `kuma agent list [remote]`    | List agents on a remote                                      |
| `kuma session …`              | List, resume, or remove sessions                             |
| `kuma run [remote] [agent]`   | Start a session (`-W` for working directory)                 |

Bare parent commands (`kuma remote`, `kuma run`, …) open interactive prompts. Pass args/flags to skip them.

Useful flags: `-C` config, `-R` relay URL, `-S` auth secret, `-M` machine id, `-K` key, `-T` join token, `-W` cwd, `-V` version.

## Connect to another machine

On the **remote** machine: run a relay it can reach (or point at a shared relay), then `kuma up` with the same `KUMA_RELAY_AUTH_SECRET` / join token setup.

On your **laptop**:

```bash
kuma keys -S "$KUMA_RELAY_AUTH_SECRET"   # On the remote, copy values
kuma remote add mybox -M … -K … -R wss://relay.example -T …
kuma run mybox
```

Or use `kuma remote` interactively.

## License

Apache-2.0 — see [LICENSE](LICENSE).
