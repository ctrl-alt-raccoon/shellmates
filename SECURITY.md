# Security

## Reporting

Please report vulnerabilities privately through GitHub Security Advisories for this repository. Do not open a public issue for credential exposure, command injection, path traversal, installer integrity, or privilege-boundary flaws.

## Trust boundaries

- `sclaude` and `sclaudex` never replace or shadow `claude`, `claudex`, or `codex`.
- Managed `sclaudex` configures Claude Code to use CLIProxyAPI as the Anthropic-compatible transport. It does not use Codex CLI as its harness. A saved Claude apps gateway session is an exception: Claude Code gives that session higher precedence and ignores the managed proxy credentials.
- The project never reads, modifies, migrates, or deletes `~/.codex/`.
- An existing external `claudex` is treated as an opaque executable. Setup records and validates its stable absolute path and executable identity without reading its contents or inspecting its environment. Because the external command remains independently mutable, its own routing, credential, and update behavior remain outside sclaude's trust boundary.
- Managed CLIProxyAPI credentials are stored in mode-`0600` credential and Claude settings-overlay files and are never written to session metadata, command arguments, logs, or doctor output. The overlay is passed by path with `--settings`; the key itself is not placed in argv.
- Atomic replacement may retain private `.sclaude-*` artifacts in project-owned directories, including unpublished or displaced contents. The project does not truncate or delete an ambiguous inode through a mutable pathname: Darwin and Linux provide no portable unlink operation conditioned on an exact inode, and retained-descriptor truncation could corrupt a concurrently created hard link. Remove retained artifacts only after separately verifying that the containing namespace is quiescent and trusted.
- The managed proxy YAML and sclaude-created backup are also tightened to mode `0600`, because the YAML contains the same generated API key.
- Managed `sclaudex` replaces inherited Anthropic credentials and neutralizes Bedrock, Vertex, and Foundry routing selectors in both the process environment and a managed env-only settings overlay before setting the localhost proxy. It also clears inherited `CLAUDE_CODE_SIMPLE` so environment state cannot bypass the managed `--bare` prohibition. It preserves normal user, project, and local settings sources, including permission and security controls, while rejecting caller overrides of `--settings` and `--setting-sources`. The protected single-token `--disallowedTools=Skill(claude-api)` option precedes caller arguments, so value-taking options, variadic options, positional prompts, and subcommands cannot consume or precede it. These measures do not override a saved Claude apps gateway session; Claude Code uses the gateway token instead. Sign out with `claude auth logout` or `/logout` before relying on the managed localhost route.
- `sclaudex` does not read Claude Code credential storage or log users out. Although `claude auth status --json` is non-mutating, its output schema and a gateway-specific discriminator are not documented, so `sclaudex` cannot reliably preflight this condition and makes no absolute routing guarantee. Confirm the active provider with `/status` when needed.
- CLIProxyAPI is configured for `127.0.0.1:8317` only. Docker examples publish only on loopback; the installer does not enable remote management or a public listener.
- OAuth credential JSON and API keys are host secrets. Prefer a separate device-code login per server; never commit them or copy placeholder/example values into production.
- Session topics and working-directory paths are persistent metadata. Topics are also partially visible in `screen -ls`; do not put secrets in either field.

## Installer integrity

Release installation uses exact GitHub release assets and verifies the selected binary against the co-hosted `SHA256SUMS` manifest before execution. That proves the downloaded bytes agree with the manifest but does not independently authenticate either asset or the GitHub release channel. Prefer a pinned tag over mutable `latest`, inspect the downloaded installer before execution, establish tag/commit trust separately, and verify GitHub build provenance when available. Release workflows also publish an SBOM and binary attestations, but `install.sh` does not automatically verify those separate artifacts. Never pipe this repository's mutable `main` branch into a shell, and publish fixes under a new tag rather than replacing existing release bytes.

## Dependency bootstrap

Setup never downloads and executes mutable third-party installer scripts for Claude Code, Codex CLI, or CLIProxyAPI. HTTPS authenticates transport to a host, not the integrity or immutability of the script returned by that host. When one of these dependencies is missing, setup may print a copyable upstream pipe-to-shell command, but that command downloads and immediately executes remote content and therefore cannot be reviewed between those operations. Independently authenticate the source or download a fixed copy, inspect it, and invoke it separately; Codex CLI remains optional. Trusted package-manager paths may still be automated, including Homebrew packages and separately authorized distribution-package operations, with executable revalidation before use.

## Side effects

System package installation, Homebrew operations, service changes, OAuth, update, rollback, and uninstall are explicit setup/management actions. `--yes` covers ordinary prompts only: setup requires separate interactive consent before any `sudo apt-get` command, and noninteractive setup prints administrator instructions instead. System-level systemd changes are instruction-only and are never executed automatically with `sudo`. The normal `sclaude` and `sclaudex` launch paths do not install software or change services.
