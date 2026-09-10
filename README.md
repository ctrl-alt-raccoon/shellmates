<p align="center">
  <img src="docs/artwork/shellmates.png" width="380" alt="Shellmates: two shell-shaped terminals. Persist, connect, collaborate.">
</p>

# Shellmates

**Run your AI agents in Screen. Leave. Come back to the same session.**

Shellmates makes Claude Code and Codex easier to use in GNU Screen, locally or
over SSH. It handles starting, finding, reconnecting to and stopping your agent
sessions, so you don't have to remember Screen commands.

- Give sessions useful names, such as "API work" or "Fix the tests".
- Keep several agents running and switch between their sessions.
- Detach or disconnect from SSH, then return to the same running agent later.

Your agent keeps its own settings, permissions and conversations. Shellmates
manages the terminal session around it.

[Get started](#build-from-source) · [Add the project harness](#5-add-the-project-harness-optional) ·
[Detach and reconnect](#detach-and-reconnect)

## Choose your agent

| Command | Runs |
|---|---|
| `scodex` | Native Codex CLI |
| `sclaude` | Claude Code |
| `sclaudex` | Claude Code with OpenAI models through the optional CLIProxyAPI route |

**Use `scodex` for Codex.** It does not need Claude or a proxy.
`sclaudex` is a different route, not the Codex CLI; see [proxy setup](docs/PROXY.md).

## Build from source

Shellmates runs on macOS and Linux, on amd64 or arm64. Install it on the machine
where your agents will run—on the server if you will connect over SSH.
There are no published release binaries yet, so build from source for now.

### 1. Install prerequisites

You need Go and Git to build, and GNU Screen to run sessions. Python 3.9+ is
needed only for the optional project harness. These commands include all of them,
plus `curl` for downloading installers.

**macOS**, with [Homebrew](https://docs.brew.sh/Installation) installed:

```sh
brew install go git screen python curl
```

**Linux — [Ubuntu 24.04+](https://packages.ubuntu.com/noble/golang-go) or
[Debian 13+](https://packages.debian.org/trixie/golang-go):**

```sh
sudo apt-get update && sudo apt-get install -y golang-go git screen python3 curl ca-certificates
```

The build below uses Go 1.26.8, matching CI. Go 1.21+ can
[download that toolchain automatically](https://go.dev/doc/toolchain), so allow
internet access for the first build. On older Linux releases, use the
[official Go installer](https://go.dev/doc/install) if the packaged Go is too old.

### 2. Install and sign in to your coding agent

Install [Codex CLI](https://learn.chatgpt.com/docs/codex/cli),
[Claude Code](https://code.claude.com/docs/en/setup), or both. Run `codex` or
`claude` directly and finish signing in before continuing. These agents are
separate installs, not included in the package commands above.

### 3. Build and install

```sh
git clone https://github.com/ctrl-alt-raccoon/shellmates.git
cd shellmates
build_version="v0.0.0-local.g$(git rev-parse --short=12 HEAD)"
mkdir -p bin
GOTOOLCHAIN=go1.26.8 go build -trimpath \
  -ldflags "-X main.version=$build_version" -o bin/sclaude ./cmd/sclaude
./bin/sclaude _install-release --source "$PWD/bin/sclaude" --version "$build_version"
export PATH="$HOME/.local/bin:$PATH"
```

This uses the existing installer to put **all three commands in `~/.local/bin`**
for your user—no `sudo`. The installed binary is copied out of the checkout;
you can run the commands from **any working folder**. `bin/` is only build output.
The last line enables the commands in this terminal; setup below handles future
terminals. [Installation, updates and removal](docs/INSTALLATION.md#build-from-source)

### 4. Set up Shellmates

Choose **one** setup command:

**Codex only:**

```sh
scodex setup --bin-dir "$HOME/.local/bin"
```

**Claude only:**

```sh
sclaude setup --backends claude --bin-dir "$HOME/.local/bin"
```

**Both native agents:**

```sh
scodex setup --backends claude,codex --bin-dir "$HOME/.local/bin"
```

Setup checks for Screen and the selected agent executables, then saves their paths
and your selection in Shellmates' own per-user configuration on this machine.
If Screen is missing, setup can install it through Homebrew on macOS; Linux
package installation requires explicit consent.

`--bin-dir` also adds that command directory to supported shell startup files.
Check `command -v scodex` (or `sclaude`) in a new terminal or SSH login.
Add `--no-modify-path` if you prefer to manage `PATH` yourself. These native setups
do **not** sign you in, start an agent, configure a proxy or install the project
harness. Run setup once per machine/user; a new selection replaces the previous
one. [Setup details and preview mode](docs/INSTALLATION.md#what-setup-checks)

### 5. Add the project harness (optional)

This gives Claude and Codex shared project instructions plus `handover` and
`cross-review` skills. Skip it if you only want persistent Screen sessions.

Go to your project's Git repository, then preview, install and check:

```sh
cd "/path/to/your/project"
sclaude harness install --project . --dry-run
sclaude harness install --project .
sclaude harness check --project .
```

The first command previews; the second installs; the third checks the result.
Harness management uses `sclaude harness` **for both agents**; it does not launch Claude.
It adds project-local instructions and skills, not global agent configuration.

Fill in `project.md` with your project's commands and rules, then regenerate the
Claude/Codex instructions:

```sh
sclaude harness update --project .
```

Review the generated files before committing them. Start a **new** agent session
in that project to pick them up. See the [harness guide](docs/HARNESS.md) for
existing-instruction handling, private installs, updates and removal.

### 6. Start working

From your project folder, choose your configured agent:

```sh
cd "/path/to/your/project"
```

**Codex:**

```sh
scodex new --topic "My project"
```

**Claude:**

```sh
sclaude new --topic "My project"
```

No `--backend codex` is needed: `scodex` already selects Codex.

## Detach and reconnect

Press **Ctrl-a, then d** to detach. Your agent stays running. You can close your
terminal or disconnect from SSH.

When you return, connect to the **same machine** and run:

```sh
scodex list
scodex attach SESSION_ID

# When you want to end the session:
scodex stop SESSION_ID
```

Use `sclaude` for the same commands with Claude. Screen keeps the process
alive while the host is running; it does not move sessions between machines or
keep a process alive through a reboot.

See [the usage guide](docs/USAGE.md) for more session commands and
[passing native agent arguments](docs/INSTALLATION.md#native-codex-arguments-and-command-names).

## More information

[Installation and troubleshooting](docs/INSTALLATION.md) ·
[Architecture](docs/ARCHITECTURE.md) · [Verification status and known limits](STATUS.md)

[Contributing](CONTRIBUTING.md) · [Security](SECURITY.md) · [MIT license](LICENSE)

Independent community software, not affiliated with Anthropic, OpenAI or GNU.
