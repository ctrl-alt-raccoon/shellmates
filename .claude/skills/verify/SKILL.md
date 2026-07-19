---
name: verify
summary: Run sclaude's isolated one-shot pre-push verification matrix.
---

# Verify sclaude

Run this skill only after implementation is frozen. The complete matrix runs once. Do not edit source/config during it, do not start an adversarial review afterward, and do not silently rerun the full matrix after a failure.

## Safety setup

1. Use fresh temporary `HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_DATA_HOME` roots for every test that could resolve user state.
2. Use explicit `127.0.0.1` fixtures for CLIProxyAPI models/messages behavior. Never read live credentials, OAuth state, service state, proxy config, `~/.codex/`, or Claude transcripts.
3. Never change real shell profiles, install dependencies, run system-level `sudo`, log users out, or invoke plugin setup/transfer/authentication.
4. Build disposable binaries outside tracked source paths where practical and clean all managed Screen sessions started by the matrix.

## Matrix

1. Static gates:
   - Verify changed Go files are formatted and `gofmt -l .` is empty.
   - Run `go mod verify`.
   - Run `sh -n install.sh scripts/build-release.sh`.
   - Parse both workflow YAML files with the available read-only parser.
   - Parse `.claude/settings.json` and run its static project-plugin contract test.
   - Run `git diff --check`.
2. Go gates:
   - `go test ./...`
   - `go test -race ./...`
   - `go vet ./...`
3. Release gates:
   - Build Darwin/Linux for amd64/arm64 with `CGO_ENABLED=0` into a fresh verification directory.
   - Require exactly `sclaude_darwin_amd64`, `sclaude_darwin_arm64`, `sclaude_linux_amd64`, `sclaude_linux_arm64`, `install.sh`, and `SHA256SUMS` before SBOM publication.
   - Verify every SHA-256 entry.
4. Isolated install lifecycle:
   - Exercise install, update, rollback, uninstall, and purge with temporary HOME/XDG roots.
   - Check stable launcher targets, versioned releases, ownership, exact private modes, journal recovery, preservation rules, and cleanup.
5. Runtime lifecycle:
   - Build `sclaude` and `testdata/fake-backend`; use a temporary config pointing configured backends to the fixture and Screen to the resolved host executable.
   - Start a detached managed session, observe its exact record and Screen state, stop that exact session, and confirm stopped metadata without persisted prompts/arguments.
   - Run direct non-TTY mode and require its exact stdout/stderr/exit status without session state.
   - Exercise invocation-name dispatch through a disposable `sclaudex` link.
   - Run doctor's uniquely named disposable Screen lifecycle and prove observe/stop/disappear behavior without touching unrelated sessions.
   - Run managed models/messages checks only against authenticated localhost fixtures.
6. Plugin compatibility:
   - Verify the project declaration pins `openai/codex-plugin-cc` to the approved tag and enables `codex@openai-codex`.
   - Verify managed `sclaudex` supplies its private overlay but does not pass `--setting-sources`, so normal user/project/local settings remain available.
   - Do not install, load, or invoke plugin code during verification.

GNU Screen 4.00.03 may return status 1 from a usable `screen -ls`; parse socket output before treating the exit status as fatal.

## Result handling

- Record every command and result in `STATUS.md`.
- On the first failure, record the exact failure and any skipped remainder, make no remote changes, and stop. Focused diagnosis is allowed; a second complete matrix is not.
- On success, add remaining low-priority ideas to the `STATUS.md` backlog, run a whitespace check for that file only, commit the verification checkpoint, and make no later source/config changes.
