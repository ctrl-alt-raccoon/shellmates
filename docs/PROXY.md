# Optional Claude/CLIProxyAPI route

[Overview](../README.md) · [Native setup](INSTALLATION.md) · [Usage](USAGE.md)

This is optional. Native Claude and native Codex do not need CLIProxyAPI.

## `sclaudex` direction

Direction matters:

```text
sclaudex
  -> Claude Code CLI harness
  -> http://127.0.0.1:8317 (CLIProxyAPI Anthropic-compatible endpoint)
  -> ChatGPT/Codex backend and models
```

This is **not** Codex CLI running Claude models. The wrapper does not read or modify `~/.codex/`; native `scodex` launches deliberately let Codex manage its own files.

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

This repository also declares the official OpenAI Codex Claude Code plugin as optional project tooling. `.claude/settings.json` pins marketplace `openai/codex-plugin-cc` to tag `v1.0.6` and enables `codex@openai-codex`; Claude Code asks each collaborator to trust the project before obtaining project-declared plugin code. Plugins execute with that user's privileges, so review the pinned source and trust prompt before accepting. This declaration is separate from both managed `sclaudex` and native `scodex`: sclaude does not install, configure, authenticate, invoke, or depend on the plugin. It does not automatically run plugin setup/transfer, Codex login, review gates, app-server brokers, or transcript inspection. An explicit command such as `scodex login` or `scodex review` runs the corresponding native command. Plugin-owned behavior and explicit transcript transfer remain outside sclaude's trust boundary.

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
scp /path/to/CLIProxyAPI/RECREATE.md <host>:
```
