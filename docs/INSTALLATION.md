# Installing Shellmates

[Overview](../README.md) · [Project harness](HARNESS.md) · [Usage](USAGE.md)

## Quick install

No release has been published yet; use [Build from source](#build-from-source) for now. Once the first release is published, replace the example version below with a published tag and download its pinned installer and checksum manifest instead of piping network content directly to a shell:

```sh
version=v0.1.0
base="https://github.com/ctrl-alt-raccoon/shellmates/releases/download/$version"
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

After you choose to trust and run the bootstrap, it downloads the complete native binary and the release's SHA-256 manifest, checks that the bytes agree, installs a versioned copy, creates only `sclaude`, `sclaudex`, and `scodex`, and then runs setup. Because the binary and manifest come from the same release channel, that check is an integrity/consistency check rather than independent publisher authentication. `SCLAUDE_BIN_DIR` defaults to `$HOME/.local/bin`; `--bin-dir` overrides it. The same directory is passed to setup so its shell `PATH` configuration matches the installed command location.

Supported automatic setup:

- macOS on Apple Silicon or Intel
- Debian/Ubuntu Linux on arm64 or amd64, including headless servers

Other Linux distributions receive dependency guidance but no guessed package-manager commands.

## What setup checks

`sclaude setup` verifies dependencies and can use trusted package-manager paths where available:

| Dependency | Role and setup behavior |
|---|---|
| Claude Code | Required for the selected `claude` or managed `claudex` backend, not for Codex-only or opaque external `claudex` setups |
| GNU Screen | Required session runtime; existing macOS Screen, `brew install screen`, or an explicitly approved `apt-get install screen` |
| CLIProxyAPI | Required only for managed `sclaudex`; Homebrew can install it on macOS, while Linux setup prints the upstream manual installation command |
| Codex CLI | Required when `codex` is selected; otherwise optional in legacy/default setup |

Setup never downloads and executes mutable Claude Code, Codex CLI, or CLIProxyAPI installer scripts. HTTPS authenticates the transport endpoint but does not authenticate mutable script content. Printed pipe-to-shell commands download and immediately execute remote content; they cannot be inspected between those operations. Independently authenticate the source or download a fixed copy, inspect it, and invoke it separately before rerunning setup. Codex CLI is never used as the `sclaudex` harness. It is a runtime-health prerequisite only when the native `codex` backend is enabled; `--skip-codex` suppresses optional guidance and discovery in default setup.

Select the backends you actually want:

```sh
scodex setup --no-modify-path                 # Codex + Screen only
sclaude setup --backends codex --no-modify-path
sclaude setup --backends claude               # Claude + Screen only
sclaude setup --backends claude,codex         # both native harnesses, no proxy
sclaude setup --backends claude,claudex,codex  # all three routes
```

`--backends` is the complete enabled selection, not an additive toggle. Without it, `sclaude setup` retains the original Claude/claudex selection and also enables an already-installed Codex unless `--skip-codex` is set. `--skip-proxy` without an external claudex permits Claude-only setup. Explicitly selecting claudex while skipping its proxy requires an external claudex. `scodex setup` defaults to Codex only. Missing explicitly selected dependencies are errors, not silently optional. `--codex-executable /absolute/path/to/codex` supports nonstandard installations and rejects the project's own launchers.

New setup writes configuration schema 2 with `enabled_backends` and, when selected, `real_codex`. Existing schema-1 configuration still loads as Claude + claudex without modification. Disabling a backend does not delete its previous files or stop its externally managed services. Doctor checks only enabled backends; native authentication is left to each vendor CLI, and doctor does not perform native inference.

### Native Codex arguments and command names

```sh
scodex -p work                         # profile, not Claude print mode
scodex resume --last                   # native interactive resume
scodex fork --last                     # native interactive fork
scodex exec --json "Summarize changes"  # direct; no Screen or chooser
scodex review --uncommitted            # direct
scodex new --topic "API work" --detach -- -p work -c 'model_reasoning_effort="high"'
sclaude new --backend codex --topic "API work" -- -p work
```

Codex receives the original backend argv and environment without injected Claude flags, proxy credentials, model defaults, or permission changes. In particular, Codex `-p` selects a profile and `-c` is a TOML configuration override; Claude `-p` is print mode. Codex's existing status line remains its own. [Official Codex command reference](https://learn.chatgpt.com/docs/developer-commands?surface=cli)

Automatic dispatch keeps interactive launches, resume, and fork eligible for Screen. Exec/review/authentication/utility commands, help/version, non-TTY use, and nested/bypass launches run directly. Explicit Codex utility commands remain direct even with `SCLAUDE_FORCE=1`. Unknown options are delegated directly so Codex can handle them; an explicit `new --backend codex -- ...` requests managed execution when needed. Option values are not mistaken for commands: a profile named `exec` remains a profile.

`scodex` reserves `sessions`, `list`, `new`, `attach`, `stop`, `prune`, and `setup` for the manager. Its `doctor`, `update`, `help`, `--help`, and `--version` belong to Codex. Use `sclaude doctor/update/help/version` for the manager, or a leading `scodex -- ...` to bypass its reserved names. Arguments after `new`'s `--` belong entirely to the backend; use a second `--` there when Codex itself needs a delimiter.

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

## Build from source

Install the prerequisites first: the README has
[one-line macOS and Ubuntu/Debian package commands](../README.md#1-install-prerequisites)
and [native agent setup links](../README.md#2-install-and-sign-in-to-your-coding-agent).
Go is a build-time dependency, not a requirement for running a prebuilt Shellmates
binary. Python 3.9+ is needed only for the optional harness commands.

The minimum language version remains Go 1.23; build and verify releases with the patched Go 1.26.8 toolchain pinned in CI. Keeping the language directive separate prevents the release workflow from selecting an obsolete compiler. [Go release history](https://go.dev/doc/devel/release)

```sh
git clone https://github.com/ctrl-alt-raccoon/shellmates.git
cd shellmates
GOTOOLCHAIN=go1.26.8 go build -o sclaude ./cmd/sclaude
```

With GNU Screen and your chosen vendor CLI installed, configure and launch the local build. For native Codex only:

```sh
./sclaude setup --backends codex --no-modify-path
./sclaude new --backend codex --topic "First session"
```

Use `--backends claude,codex` to enable both native backends, or `--backends claude` for Claude only. Source builds do not install the three launcher links; the release installer creates them. Use `./sclaude` for the local build and select the backend explicitly with `new --backend`.

For development, run tests in disposable HOME/XDG/cache roots as described in [CONTRIBUTING.md](../CONTRIBUTING.md). Release and opt-in regression commands, within that isolated verification environment, include:

```sh
./scripts/build-release.sh snapshot
SCLAUDE_SCREEN_INTEGRATION=1 go test -count=1 -run '^(TestRealScreenLifecycle|TestNativeCodexScreenLifecycle)$' ./internal/screen ./internal/app
SCLAUDE_RELEASE_INTEGRATION=1 go test -count=1 -run '^TestCandidateOwnsInstallerMigration$' ./internal/setup
```

The release build produces `CGO_ENABLED=0` binaries for macOS/Linux on amd64/arm64, a copy of `install.sh`, and `SHA256SUMS` covering every binary and the installer. All three launchers use the same binary; no extra platform assets are necessary. Linux builds avoid cgo-linked C libraries; macOS Mach-O binaries still use operating-system libraries/frameworks and are not claimed to be fully static. CI verifies formatting, modules, tests, race tests, vet, shell syntax, vulnerabilities, real Screen lifecycle, the four platform builds, and generated checksums before release publication. The real Screen test uses a disposable socket namespace, never the user's existing sessions.
