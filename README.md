# sclaude

`sclaude` is a lightweight GNU Screen manager for Claude Code sessions.

- `sclaude` runs the normal Claude Code backend in a topic-named Screen session.
- `sclaudex` runs **Claude Code as the harness** and configures CLIProxyAPI's Anthropic-compatible localhost endpoint for Codex models, subject to Claude Code's authentication precedence described below.
- `sclaude sessions` shows what each managed session is about and whether it is Attached, Detached, Stopped, or Failed.

The vendor commands `claude`, `claudex`, and `codex` are never replaced or shadowed.

> This is an independent community project. It is not affiliated with Anthropic, OpenAI, GNU, or CLIProxyAPI. Routing subscription traffic through an unofficial client may be subject to provider terms; the account owner accepts that risk.

## Quick install

The public installer becomes available after the first GitHub release. Download a pinned installer and checksum manifest instead of piping network content directly to a shell:

```sh
version=v0.1.0
base="https://github.com/ctrl-alt-raccoon/sclaude/releases/download/$version"
curl --proto '=https' --proto-redir '=https' -fLO "$base/install.sh"
curl --proto '=https' --proto-redir '=https' -fLO "$base/SHA256SUMS"
grep '  install.sh$' SHA256SUMS | shasum -a 256 -c -
less install.sh
sh install.sh --version "$version"
```

On Linux, replace `shasum -a 256 -c -` with `sha256sum -c -` if needed. Comparing `install.sh` with the co-hosted manifest detects corruption or inconsistent release assets, but does not independently authenticate either file. Before execution, establish provenance separately—for example, inspect the pinned installer and release, verify the tag/commit through a trusted path, and verify the published GitHub attestation for the binary when available. `latest` is convenient but mutable and therefore less reproducible than a pinned tag.

To install the commands elsewhere, use the installer option or environment variable. Put setup options after `--`:

```sh
sh install.sh --version "$version" --bin-dir "$HOME/bin" -- --headless

SCLAUDE_BIN_DIR="$HOME/bin" \
  sh install.sh --version "$version" -- --skip-codex
```

After you choose to trust and run the bootstrap, it downloads the complete native binary and the release's SHA-256 manifest, checks that the bytes agree, installs a versioned copy, creates only `sclaude` and `sclaudex`, and then runs setup. Because the binary and manifest come from the same release channel, that check is an integrity/consistency check rather than independent publisher authentication. `SCLAUDE_BIN_DIR` defaults to `$HOME/.local/bin`; `--bin-dir` overrides it. The same directory is passed to setup so its shell `PATH` configuration matches the installed command location.

Supported automatic setup:

- macOS on Apple Silicon or Intel
- Debian/Ubuntu Linux on arm64 or amd64, including headless servers

Other Linux distributions receive dependency guidance but no guessed package-manager commands.

## What setup checks

`sclaude setup` verifies dependencies and can use trusted package-manager paths where available:

| Dependency | Role and setup behavior |
|---|---|
| Claude Code | Required runtime harness; setup prints the upstream manual installation command when it is missing |
| GNU Screen | Required session runtime; existing macOS Screen, `brew install screen`, or an explicitly approved `apt-get install screen` |
| CLIProxyAPI | Required only for managed `sclaudex`; Homebrew can install it on macOS, while Linux setup prints the upstream manual installation command |
| Codex CLI | Optional setup tooling; setup prints optional manual guidance when it is missing |

Setup never downloads and executes mutable Claude Code, Codex CLI, or CLIProxyAPI installer scripts. HTTPS authenticates the transport endpoint but does not authenticate mutable script content. Printed pipe-to-shell commands download and immediately execute remote content; they cannot be inspected between those operations. Independently authenticate the source or download a fixed copy, inspect it, and invoke it separately before rerunning setup. Codex CLI is optional and is never used as the `sclaudex` harness or treated as a runtime-health prerequisite; use `--skip-codex` to suppress its optional guidance.

Useful setup modes:

```sh
sclaude setup --dry-run           # report actions; write nothing
sclaude setup --yes
sclaude setup --headless          # use CLIProxyAPI device-code login
sclaude setup --non-interactive   # configure; report the remaining auth command
sclaude setup --skip-codex
sclaude setup --skip-smoke        # skip the authenticated model/readiness check
sclaude doctor
sclaude verify                    # authenticated proxy check: models and Claude aliases
```

For nonstandard or headless deployments, pass explicit proxy paths and choose who supervises it:

```sh
# Installer-created per-user unit
sclaude setup --headless \
  --proxy-executable "$HOME/cliproxyapi/cli-proxy-api" \
  --proxy-config "$HOME/cliproxyapi/config.yaml" \
  --proxy-service systemd

# Administrator-created /etc/systemd/system/cliproxyapi.service
sclaude setup --headless \
  --proxy-executable /usr/local/bin/cli-proxy-api \
  --proxy-config /opt/cliproxyapi/config.yaml \
  --proxy-service systemd --proxy-system-service

# An already-running Docker container owns restart policy
sclaude setup --headless \
  --proxy-executable /path/to/host/cliproxyapi \
  --proxy-config /opt/cliproxyapi/config.yaml \
  --proxy-service docker
```

`--proxy-service none` also skips service commands. With `docker` or `none`, start the proxy separately before the model verification step, or use `--skip-smoke` and run `sclaude verify` once the proxy is up. Homebrew service management is limited to the Homebrew formula's config (`/opt/homebrew/etc/cliproxyapi.conf` or `/usr/local/etc/cliproxyapi.conf`); use `--proxy-service none` or another explicitly configured supervisor with a custom config path.

## `sclaudex` direction

Direction matters:

```text
sclaudex
  -> Claude Code CLI harness
  -> http://127.0.0.1:8317 (CLIProxyAPI Anthropic-compatible endpoint)
  -> ChatGPT/Codex backend and models
```

This is **not** Codex CLI running Claude models. The project never reads or modifies `~/.codex/`.

> **Authentication limitation:** an active Claude apps gateway sign-in takes precedence over `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, cloud-provider selectors, and other per-invocation credentials. In that state Claude Code ignores the localhost proxy credentials that managed `sclaudex` supplies, so the route shown above does not apply. Sign out of the Claude apps gateway with `claude auth logout` (or `/logout` inside Claude Code), then authenticate again later when you need the gateway. `sclaudex` never logs you out or changes Claude Code's saved authentication state.
>
> `claude auth status --json` is non-mutating, but Claude Code does not document its JSON schema or a gateway-specific discriminator. `sclaudex` therefore cannot reliably detect this state before launch and does not claim an absolute routing guarantee. Use `/status` inside Claude Code to confirm the active provider when routing matters.

### Existing `claudex`

If setup finds an existing executable `claudex`, it records only its absolute path and treats it as opaque. It does not read the script, copy its token, or change its proxy configuration.

### Fresh machine

When no existing `claudex` is present—or when explicit `--proxy-*` options request the managed proxy—install CLIProxyAPI first. Setup can install the Homebrew package on macOS; on Linux it prints upstream manual guidance instead of executing a mutable installer script. Once the executable is available, setup can:

1. Bind CLIProxyAPI to `127.0.0.1:8317`.
2. Generate a fresh private proxy key, remove placeholder `your-api-key-*` entries, and preserve unrelated configuration.
3. Install the required `oauth-model-alias` mappings for pinned Claude model IDs, with `fork: true` so the original `gpt-*` names remain usable.
4. Store the key in a mode-`0600` project credential file and a separate atomic mode-`0600` Claude settings overlay, and tighten the proxy YAML and backup to mode `0600` because they contain the same secret.
5. Run browser login:
   ```sh
   cliproxyapi --config /path/to/config.yaml --codex-login
   ```
6. Or on a headless server run device login:
   ```sh
   cliproxyapi --config /path/to/config.yaml --codex-device-login
   ```
7. Wait for the proxy to become ready, verify that `/v1/models` contains `gpt-5.6-sol`, `gpt-5.6-terra`, and `gpt-5.6-luna` plus the four required Claude aliases, and make an authenticated `/v1/messages` request through `claude-opus-4-8`, without printing the key. The same check is available later as `sclaude verify`.

Managed `sclaudex` launches then apply the current wrapper policy in-process and pass the private overlay with `--settings`. Claude Code continues loading normal user, project, and local settings, including permission and security controls; the overlay contains only managed `env` entries so those settings cannot reroute this backend away from the localhost proxy. No settings source is disabled.

- top-level model `gpt-5.6-sol(xhigh)`
- Opus/Sonnet/Haiku tier mappings to `gpt-5.6-sol(xhigh)`, `gpt-5.6-sol(high)`, and `gpt-5.6-luna(low)`
- `CLAUDE_CODE_AUTO_COMPACT_WINDOW=300000`
- `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=60`
- `CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000`
- `CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION=1000`
- `CLAUDE_CODE_SUBAGENT_MODEL=gpt-5.6-sol(high)`
- `--disallowedTools=Skill(claude-api)` to prevent the oversized bundled skill from exhausting this proxy backend's effective context window

Arguments passed to `sclaudex` follow the managed model default, so an explicit flag such as `--model "gpt-5.6-terra(medium)"` deliberately overrides the pinned model for that launch. The protected single-token `--disallowedTools=Skill(claude-api)` option is placed before all caller arguments, keeping it separate from value-taking and variadic caller options and before any Claude subcommand. The wrapper-supplied environment settings above cannot be overridden by caller arguments, but a saved Claude apps gateway session outranks and ignores them as described above. Provider selectors and `CLAUDE_CODE_SIMPLE` are replaced with empty values in both the process environment and overlay; Claude Code treats empty provider-selection variables as unset, and clearing simple mode prevents inherited environment state from bypassing the managed `--bare` prohibition. Managed launches reject `--settings`, `--setting-sources`, and `--disallowedTools`/`--disallowed-tools` overrides so the private overlay and blocked skill remain authoritative, and reject `--bare` (including `--bare=...` forms) and the `--` argument delimiter because they are incompatible with managed launch behavior.

The generated key is never stored in session metadata, prompts, command arguments, logs, or doctor output. It appears only in private configuration files and the child process environment.

### Linux system service and Docker

When separately authenticated and run manually, the upstream Linux installer normally creates a per-user systemd unit. On a server that instead uses a system service, create a host-specific unit such as:

```ini
# /etc/systemd/system/cliproxyapi.service
[Unit]
Description=CLIProxyAPI
After=network-online.target

[Service]
ExecStart=/usr/local/bin/cli-proxy-api --config /opt/cliproxyapi/config.yaml
Restart=always
User=<service-user>

[Install]
WantedBy=multi-user.target
```

Verify the binary, config path, service user, and auth-directory ownership on that host, then run setup as the service user with `--proxy-system-service`. Setup writes the selected user-owned configuration and credentials but does not elevate or change the system unit itself. It prints shell-quoted `sudo systemctl enable` and `start` or `restart` commands for an administrator to review and run. After authentication and those commands complete, run `sclaude verify`. Do not run the entire setup with `sudo`, and do not copy an API key or OAuth credential into source control.

The equivalent Docker service from the current runbook is:

```sh
docker run -d --name cliproxyapi --restart unless-stopped \
  -p 127.0.0.1:8317:8317 \
  -v /opt/cliproxyapi/config.yaml:/CLIProxyAPI/config.yaml \
  -v /opt/cliproxyapi/auth:/root/.cli-proxy-api \
  eceasy/cli-proxy-api:latest
```

Use `cliproxyapi --codex-device-login` for a separate server credential where possible. Copying `codex-*.json` works, but shares refresh/revocation state between machines. A plain copy of the source runbook is sufficient when provisioning manually:

```sh
scp /Users/me/Projects/CLIProxyAPI/RECREATE.md <host>:
```

## Usage

### Launch

```sh
sclaude
sclaude --continue
sclaudex
sclaudex --resume
```

A new interactive session requires a topic such as `OAuth callback refactor`. Topics are persistent and partially visible in `screen -ls`, so do not include secrets or private prompt content.

If related sessions already exist, the launcher shows a compact chooser:

```text
1  Running/Detached  OAuth callback refactor   ~/work/api
2  Running/Attached  Investigate flaky tests   ~/work/service
3  Stopped (exit 0)  Update docs               ~/work/docs
n  New named session
m  All sessions
q  Cancel
```

Detach from Screen with **Ctrl-a d**. The Claude process keeps running.

### Manage

```sh
sclaude sessions
sclaude list
sclaude list --json
sclaude new --backend claudex --topic "Proxy comparison" --detach -- -p "..."
sclaude attach 3fa1c290
sclaude attach "OAuth callback refactor"
sclaude attach SESSION --multi
sclaude attach SESSION --takeover
sclaude stop SESSION
sclaude prune --older-than 30d
```

An attached session is never taken over implicitly. Choose `--multi` to join it or `--takeover` to detach its other display.

### Product help, version, and pass-through

```sh
sclaude help
sclaude --help
sclaude version
sclaude --version
sclaude -- --help       # pass --help to Claude Code
sclaude -- version      # pass the word "version" to Claude Code
```

A leading product-level `--` is removed by sclaude and sends every following argument directly to the selected backend. Managed `sclaudex` still rejects backend arguments that would replace its private settings or protected routing policy.

### Noninteractive behavior

Print mode and pipelines bypass Screen and preserve stdout and exit status:

```sh
sclaude -p "Reply with exactly OK"
sclaudex -p "Answer as JSON" | jq .
printf '%s' "$PROMPT" | sclaude -p
```

Inside an unrelated existing Screen, `sclaude` runs directly in that Screen and notes that it is not tracked. Set `SCLAUDE_FORCE_NEST=1` only when deliberate Screen nesting is desired.

## State and privacy

Session metadata lives under `${XDG_STATE_HOME:-~/.local/state}/sclaude/sessions/`. It records topic, backend, the durable working-directory path, Screen status, process IDs, timestamps, and exit state. Topics are also embedded in managed Screen names, so both topics and working-directory paths should be treated as metadata rather than secret storage.

It does **not** store:

- prompts or Claude arguments
- environment variables
- CLIProxyAPI or OAuth credentials
- Claude/Codex output
- Claude transcript content

Stopped records remain until pruned. They are separate from Claude Code's own conversation/session storage.

## Update and uninstall

```sh
sclaude update
sclaude rollback
sclaude uninstall
sclaude uninstall --purge-state
```

Updates download the exact platform asset and checksum manifest, verify SHA-256, and activate a versioned release through the same journaled transaction used by installation. The current and previous verified releases are retained; older ledger-owned releases are pruned after commit, and cleanup failures are reported without undoing a successful activation. Rollback re-verifies the retained previous release before activating it.

Uninstall first proves that managed Screen sessions are inactive and that all ledger-owned launchers, releases, and managed shell blocks are unchanged. The default mode removes the installed launchers/releases and unchanged managed PATH blocks, while preserving runtime/session state. `--purge-state` additionally removes the project runtime configuration, credential, settings overlay, and known session/launch/install-state children. The private state-root directory remains as the admission-lock anchor, and unrelated children are preserved. Neither mode removes vendor CLIs, the external CLIProxyAPI YAML or its sclaude-created backup, service definitions, OAuth/auth-directory contents, or `~/.codex/`; rotate proxy keys and perform external service/config cleanup separately when required.

## Build from source

Go 1.23 or newer:

```sh
go test ./...
go vet ./...
go build ./cmd/sclaude
./scripts/build-release.sh snapshot
```

The release build produces `CGO_ENABLED=0` binaries for macOS/Linux on amd64/arm64, a copy of `install.sh`, and `SHA256SUMS` covering every binary and the installer. Linux builds avoid cgo-linked C libraries; macOS Mach-O binaries still use operating-system libraries/frameworks and are not claimed to be fully static. CI verifies formatting, modules, tests, race tests, vet, shell syntax, the four platform builds, and the generated checksums before release publication.

## Screen compatibility

The implementation supports older GNU Screen releases, including macOS Screen 4.00.03. It uses `-dmS`, `-ls`, `-r`, `-x`, `-d -r`, and `-X quit`; it does not require `screen -Q`, enable Screen logging, run `screen -wipe`, or modify `.screenrc`.

## Security

See [SECURITY.md](SECURITY.md). The recommended reproducible install pins a release tag. GitHub releases include checksums, an SBOM, and GitHub build-provenance attestations for the binaries. The installer verifies consistency with the co-hosted SHA-256 manifest; this does not independently authenticate the release channel. Provenance and the SBOM are separate release artifacts and are not verified automatically by `install.sh`. Published release assets must not be replaced in place; fixes use a new version.
