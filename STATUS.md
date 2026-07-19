# Status checkpoint

Updated: 2026-07-19

## Completed tasks

- Restored the baseline and lifecycle integration.
- Implemented transactional install, update, rollback, uninstall, and purge behavior.
- Hardened runtime configuration, proxy credentials, managed settings, proxy YAML handling, setup transactions, dependency policy, doctor/verify checks, and CLI argument contracts.
- Removed automatic execution of mutable third-party installer scripts; Codex CLI is optional and system-level `sudo` remains separately authorized or instruction-only.
- Hardened private configuration reads, setup/install transaction admission and recovery, release publication recovery, owned-release pruning, and session/uninstall lifecycle serialization.
- Built and checksum-verified Darwin/Linux release binaries for amd64/arm64.
- Verified the disposable GNU Screen doctor lifecycle and explicit local proxy fixtures.
- Diagnosed and fixed the managed GNU Screen startup deadlock: `_run-session` no longer reacquires the creator-held state-root admission lock.
- Added a regression test for startup while the creator holds state-root admission.
- Verified a real managed Screen session reaches `running`, records Screen/runner/backend PIDs, stops cleanly, and records `stopped-by-manager`.
- Resolved all six final verified finding classes:
  1. Install-journal recovery is strict and fail closed, including exact launcher snapshots, release digests, trailing-data rejection, and committed-release verification.
  2. Uninstall tolerates an already-missing inactive historical release while retaining current/previous and modification checks.
  3. Homebrew executable/config trust is contained beneath the canonical active prefix after path resolution.
  4. Homebrew-installed GNU Screen and CLIProxyAPI are discovered outside `PATH`.
  5. Release verification/build jobs are read-only and publication is isolated, serialized, draft-first, exact-asset verified, and explicitly targeted through `GH_REPO`.
  6. Documentation accurately describes installer/checksum provenance, pipe-to-shell execution, and macOS linking.

Focused Task 25 verification passed:

- `go test ./internal/setup`
- Ruby YAML parsing for `.github/workflows/ci.yml` and `.github/workflows/release.yml`
- `sh -n install.sh scripts/build-release.sh`
- `git diff --check`
- Direct inspection confirmed both checkout-free publication steps set `GH_REPO` and all seven `gh release` invocations execute within those environments.
- `actionlint` is not installed; GitHub Actions remains the authoritative semantic workflow validator.
- Aligned managed `sclaudex` with the current CLIProxyAPI runbook by enforcing `CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION=1000` in the child environment and strict private settings overlay.
- Added regression coverage proving the setting is required exactly, changed or missing overlay values fail closed, and stale duplicate inherited values are replaced by one authoritative managed value.
- Focused Task 26 verification passed: `go test ./internal/managedsettings ./internal/backend`.
- Added repository-portable development rules in `CLAUDE.md`, expanded the isolated one-shot `verify` skill, and added model-agnostic `review-unbiased` and `honesty` skills without personal model, orchestration, or output-style policy.
- Focused Task 27 validation passed: Ruby safe-YAML/frontmatter and content checks confirmed the exact three skill names, directory/name matching, required summaries/headings, required `CLAUDE.md` sections, and absence of selected machine/model-specific guidance; `git diff --check` passed before the status checkpoint.
- Declared optional project plugin `codex@openai-codex` from marketplace `openai-codex`, pinned to `openai/codex-plugin-cc` tag `v1.0.6`, without vendoring or invoking plugin code.
- Added a strict static project-settings contract test requiring one JSON document, the exact marketplace source/tag and enabled plugin, plus an explicit managed-backend assertion that normal setting sources remain enabled.
- Documented the collaborator trust prompt, user-privilege execution, optional Codex CLI status, and the plugin configuration/authentication/state/app-server/review/transfer boundary in `README.md` and `SECURITY.md`.
- Focused Task 28 checks passed: `python3 -m json.tool .claude/settings.json`, `go test ./internal/backend`, and `git diff --check`.
- Isolated `claude doctor` validation was skipped: the permission classifier blocked it before execution because Claude Code 2.1.215 may obtain or load the enabled external plugin. No plugin code was installed or run; the no-install/no-invocation boundary was preserved.
- After the first matrix stopped, the user separately authorized a new verification task. The project `verify` skill now requires dedicated disposable `GOMODCACHE` and `GOCACHE` directories and makes the exact temporary root user-writable before removal.
- Focused harness verification passed: an isolated `go test ./internal/setup` downloaded modules into the dedicated disposable cache, the cache files were made read-only to reproduce Go cache permissions, cleanup succeeded, and the exact temporary root was confirmed absent.

## Prior one-shot matrix result

The frozen implementation at `d0b6673` was checked once on 2026-07-19. The matrix stopped at the first nonzero command, as required.

Passed before the stop:

1. Static gates: clean worktree, empty `gofmt -l .`, `go mod verify`, shell syntax, safe parsing of both workflow YAML files, project settings JSON, focused plugin contract tests, and `git diff --check`.
2. Go gates: `go test ./...`, `go test -race ./...`, and `go vet ./...`.
3. Release gates: four `CGO_ENABLED=0` Darwin/Linux amd64/arm64 builds, exact six-file pre-SBOM asset set, and successful SHA-256 verification for every entry. The disposable release directory was removed by its cleanup trap.
4. Isolated install lifecycle test groups all reported `ok`: install/update/rollback/uninstall, strict journal recovery, ownership/preservation/purge/private state-root behavior, and localhost release-download/checksum policy.

Failure and cleanup:

- The isolated install command set `HOME` to a fresh temporary root but did not preserve or separately set `GOMODCACHE`, so Go downloaded `gopkg.in/yaml.v3` into that temporary home using read-only module-cache permissions. All four test groups passed, then the shell trap's `rm -rf` failed with `Permission denied`, making the matrix command exit 1.
- The exact disposable root was subsequently made user-writable and removed. No live user, credential, OAuth, proxy, service, shell-profile, plugin, Codex, or transcript state was touched.

Skipped after the required stop:

- Runtime lifecycle: real managed GNU Screen create/observe/stop, direct non-TTY behavior, disposable `sclaudex` invocation-name dispatch, doctor disposable Screen lifecycle, and localhost models/messages fixtures.
- Final plugin compatibility recheck beyond the already-passed static declaration and managed setting-source contract.
- GitHub metadata/environment/deploy-key changes, push, and CI wait.

## Newly authorized matrix attempt

The frozen checkpoint at `60750d2` began a newly authorized complete matrix on 2026-07-19 and stopped at its first blocked gate.

Passed before the stop:

1. Static gates: clean worktree, empty `gofmt -l .`, `go mod verify`, shell syntax, safe parsing of both workflow YAML files, project settings JSON, focused plugin contract tests, `git diff --check`, and successful cleanup of the disposable HOME/XDG/Go cache root.

Blocked before execution:

- The combined full Go gate (`go test ./...`, `go test -race ./...`, and `go vet ./...`) was denied by the Claude Code auto-mode permission classifier before any of those commands ran. Its reason was that rerunning the bounded complete matrix while downloading and executing declared Go modules requires explicit user authorization that names the complete matrix rerun; the prior broad “go for it” reply was not accepted as sufficiently explicit.

Skipped after the required stop:

- Full Go tests, race tests, and vet.
- Four-platform release builds, exact asset-set checks, and checksum verification.
- Isolated install/update/rollback/uninstall/purge lifecycle tests.
- Runtime GNU Screen, direct mode, dispatch, doctor, and localhost proxy fixture checks.
- Final plugin compatibility recheck, GitHub reconciliation, push, and CI wait.

No source or configuration changed after the matrix started. No GitHub, remote, deploy-key, environment, tag, release, OAuth, service, credential, plugin, or transcript mutation occurred.

## Current task

Stopped after the newly authorized matrix was blocked at the full Go gate. No remote mutation is permitted.

## Remaining tasks

1. Obtain explicit authorization that names rerunning the complete verification matrix, then begin a fresh separately authorized matrix from the frozen checkpoint. The blocked attempt was not silently rerun.
2. Publish `main` only if a complete authorized matrix passes; no GitHub mutation or push has yet been performed.

## Key decisions and constraints

- `sclaude` routes to ordinary Claude Code/Anthropic; managed `sclaudex` keeps Claude Code as the harness and routes through CLIProxyAPI at `http://127.0.0.1:8317`.
- An existing external `claudex` remains opaque and is referenced only by a stable absolute path.
- Prompts/backend arguments exist only in private one-use launch files and are never persisted in session records.
- Automated verification uses temporary HOME/XDG roots and explicit localhost fixtures; it must not touch live OAuth, proxy, service, shell-profile, or credential state.
- Never read or modify `~/.codex/`, inspect Claude credential/keychain storage, expose secrets, log users out, or automatically run system-level `sudo`.
- Existing release assets must never be overwritten in place; fixes require a new release tag.
- Local commits and the final verified push of `main` are authorized. Do not create a tag or publish a release.
- Create a local commit after each tracked task is completed and update this file at each boundary.
- After each authorized complete matrix starts, only `STATUS.md` may change before its verification checkpoint commit.
- Do not run another adversarial review. Remaining ideas belong in the backlog below.

## Backlog

- Add local `actionlint` coverage in a future task if a trusted installation path is selected; do not block this task on installing it.
- Consider automating GitHub attestation verification in the installer in a future release, with a separately reviewed trust model.
