# Status checkpoint

Updated: 2026-07-20

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

## Explicitly authorized complete matrix result

The user explicitly authorized a complete rerun from frozen source/config checkpoint `60750d2`, including disposable module downloads and all planned local gates. The run began on 2026-07-19 and failed in the full Go gate.

Passed before the failure:

1. Static gates: clean worktree, empty `gofmt -l .`, `go mod verify`, shell syntax, safe parsing of both workflow YAML files, project settings JSON, focused plugin contract tests, `git diff --check`, and successful disposable HOME/XDG/Go-cache cleanup.

Go-gate failures:

- `go test ./...` failed `internal/app.TestRunSessionStartsWhileCreatorHoldsStateRootAdmission` at `internal/app/dispatch_test.go:419`: `_run-session blocked on the creator-held state-root admission lock`.
- The combined shell wrapper incorrectly continued instead of stopping at that nonzero test result. `go test -race ./...` then failed the same admission-lock test and `internal/app.TestRunInstallReleaseFromPlatformAsset` at `internal/app/dispatch_test.go:815`, whose command returned `sclaude: install ledger paths do not match the requested layout`.
- `go vet ./...` subsequently ran without diagnostics, and disposable cache cleanup succeeded. The wrapper's final `Matrix Go gates passed` line and zero shell status are invalid because earlier commands in that wrapper failed; future matrix commands must explicitly test each command status instead of relying on this shell's `set -e` behavior through a helper function.

Skipped after the required stop:

- Four-platform release builds, exact asset-set checks, and checksum verification.
- Isolated install/update/rollback/uninstall/purge lifecycle tests.
- Runtime GNU Screen, direct mode, invocation dispatch, doctor, and localhost proxy fixture checks.
- Final plugin compatibility recheck, GitHub reconciliation, push, and CI wait.

Only `STATUS.md` changed after the matrix began. No GitHub, remote, deploy-key, environment, tag, release, OAuth, service, credential, plugin, or transcript mutation occurred.

## Focused repair of the Go-gate failures

The separately authorized repair task diagnosed and fixed both `internal/app` failures from the `60750d2` matrix without changing any production code.

Root causes:

- `TestRunSessionStartsWhileCreatorHoldsStateRootAdmission` was a false deadlock detector, not a product regression. `_run-session` loads runtime with `config.Load`, `runSession` never acquires state-root admission, and session-store serialization uses a separate lock-file inode from the admission directory flock, so no lock contention exists. The test instead required the entire runner lifecycle—launch consumption, subprocess start and exit, `MarkStarted`/`Finish` atomic writes and fsyncs—to finish within one second, which a cold-cache concurrent full-suite run can exceed. The relevant source was identical between the passing `d0b6673` matrix and the failing `60750d2` matrix.
- `TestRunInstallReleaseFromPlatformAsset` set a fresh `HOME` and `XDG_DATA_HOME` but inherited the outer `XDG_STATE_HOME`, which the matrix shared between the normal and race Go commands. `setup.DefaultInstallLayout` derives the install state directory from `XDG_STATE_HOME`, so the normal run's ledger contaminated the race run and the layout guard correctly rejected the mismatch.

Repair (tests and process only; `internal/app/dispatch.go`, `internal/session`, `internal/stateroot`, and `internal/setup` untouched):

- The admission test now uses a backend fixture that writes a started marker and blocks on a test-controlled release file passed through the launch request. With the creator-held admission flock still held, the test waits under a generous bounded deadline for the marker plus a durable `StateRunning` record with runner/backend PIDs, failing early if the runner exits first. Only after proving that milestone does it release admission, open the gate, and require exit 0, stopped state, and launch-request consumption. Idempotent cleanup opens the gate, drains the runner, and releases admission exactly once so failure paths cannot leak the subprocess.
- The install test now sets fresh `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_DATA_HOME` beneath its temporary `HOME`.
- The `verify` skill now requires each Go gate command to run in its own disposable root with dedicated `HOME`, all three XDG roots, `GOMODCACHE`, and `GOCACHE`, forbids sharing application state between normal and race commands, and requires explicitly inspecting every command and cleanup status before the next command, noting that `set -e` through helper functions is insufficient. No repository wrapper script was added; CI already isolates gates as separate steps.

Focused verification (authorized focused checks only; no complete matrix was run):

- `gofmt -l internal/app/dispatch_test.go` empty; `git diff --check` clean.
- `go test ./internal/app -run '^(TestRunSessionStartsWhileCreatorHoldsStateRootAdmission|TestRunInstallReleaseFromPlatformAsset)$' -count=20` passed (`ok`, 6.381s) under a fresh disposable root with dedicated `HOME`, three XDG roots, `GOMODCACHE`, and `GOCACHE`; test status 0, cleanup status 0, root confirmed absent.
- `go test -race ./internal/app -run '^(TestRunSessionStartsWhileCreatorHoldsStateRootAdmission|TestRunInstallReleaseFromPlatformAsset)$' -count=5` passed (`ok`, 2.923s) under a second independent disposable root with the same isolation; test status 0, cleanup status 0, root confirmed absent.
- Scoped diff review, cap three rounds: round one found the diff clean apart from one low-severity failure-path diagnostic ordering observation, recorded in the backlog below. The loop ended after round one per the low-only exit rule.
- Full `go test ./...`, race suite, vet, release, install-lifecycle, runtime, plugin, and GitHub stages were intentionally not run; they belong to the separately authorized complete matrix.

## Authorized complete matrix from `80a9c30`: passed

The user explicitly authorized one complete matrix from frozen source/config checkpoint `80a9c30`. It ran on 2026-07-26 and every stage passed. Each Go command used its own disposable root with dedicated `HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `XDG_DATA_HOME`, `GOMODCACHE`, and `GOCACHE`; every command and cleanup status was inspected explicitly before the next command, and each root was confirmed absent after removal.

1. Static gates: clean worktree at `80a9c30`, empty `gofmt -l .`, `go mod verify` reported all modules verified, `sh -n install.sh scripts/build-release.sh`, safe Ruby YAML parsing of both workflow files, `python3 -m json.tool .claude/settings.json`, `git diff --check`, and the plugin contract tests `TestProjectCodexPluginDeclaration` and `TestManagedProxyPreservesDefaultSettingSources` (`ok`, 0.339s).
2. Go gates, three separately checked commands: `go test ./...` (`ok` for app 4.060s, backend 1.004s, config 0.444s, fssecure 2.485s, managedsettings 2.044s, screen 4.345s, session 1.975s, setup 15.097s), `go test -race ./...` (all packages `ok`, setup 16.456s), and `go vet ./...` with no diagnostics. The two tests that failed the `60750d2` matrix now pass in both the ordinary and race suites.
3. Release gates: `scripts/build-release.sh v0.0.0-matrix.80a9c30` produced the four `CGO_ENABLED=0` binaries; the directory contained exactly `SHA256SUMS`, `install.sh`, and the four platform binaries; `shasum -a 256 -c SHA256SUMS` reported `OK` for all five entries; `file` confirmed Mach-O arm64 and statically linked ELF x86-64 outputs.
4. Isolated install lifecycle: the install/update/rollback/uninstall/purge, journal-recovery, ledger, ownership, preservation, state-root, download, and checksum groups in `internal/setup` reported `ok` (5.133s), including `TestUninstallRejectsModifiedInactiveHistoricalRelease`, `TestUninstallRecoversInterruptedTransaction`, `TestUninstallJournalFailsClosed`, and `TestUpdateBoundsAllResponsesAndDoesNotLeakBodies`.
5. Runtime lifecycle against the `testdata/fake-backend` fixture and host Screen 4.00.03: a detached managed session reached `running` with Screen PID 9301, runner PID 9303, and backend PID 9304 matching the backend's own marker, and its record persisted no prompts or backend arguments. A second session was stopped by the manager and recorded `stopped` with `end_reason: stopped-by-manager` and a removed socket. Direct non-TTY mode passed arguments through and preserved exit statuses 2, 7, and 3 while creating no session records, including through a disposable `sclaudex` link. `doctor` reported all checks OK and observed then removed its uniquely named disposable session, leaving no doctor socket. The launch directory was empty after consumption and the state root retained mode `0700`.
6. Managed proxy checks ran only against explicit localhost fixtures: `TestCommandVerifyRequiresConfiguredManagedMode`, `TestCommandVerifyUsesOnlyRuntimeConfiguredArtifacts`, `TestCommandVerifyRejectsInvalidArtifactsBeforeHTTP`, and `TestCommandVerifyDoesNotExposeEndpointResponseBody` all passed. No live credentials, OAuth state, service state, or production proxy configuration was read or changed.

Corrections made during the run, none of which were product failures:

- The first workflow-parse invocation used a Ruby API this host lacks, and the first asset-set comparison used a locale-dependent sort order. Both checks were reissued correctly and passed.
- The first runtime config fixture used the key `cliproxy_service`; the schema requires `cliproxyapi_service`, and strict parsing correctly rejected the file. The fixture was corrected.
- The first `stop` invocation received no confirmation on a non-TTY stdin and correctly declined to act, and the first fixture backend exited on its own 30-second sleep, so the manager-stop path was re-exercised with a long-lived backend and `--yes`.

Cleanup and scope:

- Every disposable HOME/XDG/Go-cache root, the generated release directory, and the managed sessions started by this matrix were removed. A pre-existing detached session `sc-claude-test-38b541c1` (PID 63978) from earlier work was left untouched because this matrix did not create it; remove it manually if it is no longer wanted.
- Only `STATUS.md` changed after the matrix began. No GitHub, remote, deploy-key, environment, tag, release, OAuth, service, credential, plugin, Codex, or transcript state was accessed or mutated.

## User-directed independent redo of the matrix and analysis: passed

The user directed that the preceding model's matrix run and analysis be redone from scratch by a different model, trusting none of its results. The redo ran on 2026-07-26 from the same frozen source/config checkpoint `80a9c30` (worktree clean at `36c5623`; `git diff 80a9c30..HEAD` touches only `STATUS.md`).

Re-derived analysis, from primary evidence rather than the prior record:

- `git diff --stat d0b6673 60750d2` touches only `.claude/skills/verify/SKILL.md` and `STATUS.md`, confirming the `60750d2` matrix failure had no product-code cause.
- `git show --stat 80a9c30` touches exactly the three authorized files; no production code changed in the repair.
- `_run-session` loads runtime via `config.Load` (`internal/app/dispatch.go:447`), and the only product `stateroot.Acquire` sites are `internal/session/manager.go:76` and `internal/setup/setup_transaction.go:114`, neither reachable from the runner; the session-store lock is a child lock file, a different inode from the admission flock on the state-root directory FD, so no contention between them is possible. The old test's one-second whole-lifecycle deadline was the defect; the repaired test asserts the actual contract.
- The repaired test was re-reviewed for data races and leak paths: all reads of the runner exit code and output buffers sit behind the `runnerDone` happens-before edge, the release-gate/drain cleanup runs before `t.TempDir` removal, and no test in the repository calls `t.Parallel()`.

Independent matrix rerun, every stage with explicitly captured command and cleanup statuses and per-command disposable roots (`HOME`, three XDG roots, `GOMODCACHE`, `GOCACHE`), each root confirmed absent after removal:

1. Static gates: empty `gofmt -l .`, `go mod verify`, `sh -n` on both shell entrypoints, Ruby safe-YAML parse of both workflows, `python3 -m json.tool .claude/settings.json`, `git diff --check`, and both plugin contract tests (`ok`, 0.255s). All passed.
2. Go gates: `go test ./...` (all packages `ok`, setup 15.599s), `go test -race ./...` (all `ok`, setup 17.462s), `go vet ./...` clean — three separate roots, statuses 0, cleanups 0.
3. Release gates: four `CGO_ENABLED=0` builds; exact six-file asset set under `LC_ALL=C` comparison; `shasum -a 256 -c` reported `OK` for all five entries; `file` confirmed Mach-O x86_64/arm64 and statically linked ELF x86-64/aarch64 for all four binaries.
4. Install lifecycle: the complete `./internal/setup` package (`ok`, 13.199s, `-count=1`) — a superset of the prior run's regex-selected groups.
5. Runtime lifecycle: detached managed session reached `running` with Screen/runner/backend PIDs 68679/68681/68682, the backend's self-written marker matching the recorded backend PID; the record contained no argument/prompt/environment fields, the launch file was consumed, and the state root held mode `0700`. Manager stop with `--yes` recorded `stopped-by-manager` and removed the socket. Direct non-TTY mode returned empty stdout/stderr with status 0 and preserved statuses 7 and 3 (the latter through a disposable `sclaudex` link) with no new session records. Doctor passed all checks and removed its disposable Screen session.
6. Managed proxy checks: all four `TestCommandVerify*` localhost-fixture tests passed in an isolated root. No live credentials, OAuth, service, or proxy state was touched.

Redo harness notes: one status capture initially used the bash-only `PIPESTATUS` in this zsh environment and produced an empty value; the command was rerun with direct exit-status capture before its result was accepted. The prior run's recorded results were all reproduced; no discrepancies were found. The pre-existing detached session `sc-claude-test-38b541c1` remains untouched. All disposable roots, the `dist/v0.0.0-redo.80a9c30` directory, and matrix-created Screen sessions were removed; only `STATUS.md` changed after the redo began, and no GitHub, remote, tag, release, credential, OAuth, service, plugin, Codex, or transcript state was accessed or mutated.

## Current task

2026-09-04: the user authorized the audit repairs followed by native Codex/scodex support, including backend-specific configuration and argument semantics. This is a new implementation task after the historical July freeze. Publication remains out of scope. New harness/house rules await the user's forthcoming instructions.

Lifecycle/storage implementation is complete at the focused-gate checkpoint:

- Screen accepts legacy and timestamped listings and reports ambiguous/partial output instead of declaring absence.
- Failed/unconfirmed stops remain active `stopping` records; explicit retry works. Terminal records with surviving sockets block prune/uninstall and can be stopped explicitly without resurrecting their terminal state.
- Prune fails closed on warnings and rechecks the current record under the store lock.
- Session directories and files use anchored descriptors, no-follow opens, owned/private regular-file checks, bounded reads, and lock identity revalidation. Symlinks, hardlinks and FIFOs are rejected without external mutation.
- The next store lock holder removes abandoned `.consume-*` and `.sclaude-*` files. Concurrent cleanup and consumers preserve one-use delivery. Session records are published before launch requests to remove a cleanup race.

Three-round focused verification cap declared before implementation. Using `/private/tmp/sclaude-audit-20260904.Q1IBnx/run-check.sh` with disposable HOME/XDG/cache/Screen/TMPDIR:

1. `go test -count=1 ./internal/session ./internal/screen ./internal/fssecure` passed.
2. `go test -race -count=1 ./internal/session ./internal/screen ./internal/fssecure ./internal/app`: the first three packages passed; app fixture startup was blocked by the sandbox's localhost bind restriction.
3. The exact race command rerun with localhost fixture permission passed all four packages (session 2.300s, screen 3.800s, fssecure 2.163s, app 2.019s).

`gofmt` and `git diff --check` passed. No live backend/account/service state was used. The final complete matrix is reserved for the native Codex implementation checkpoint.

## Remaining tasks

1. Publish `main` only when separately authorized: GitHub repository metadata and topics, protected `release` environment, deploy-key addition, origin configuration, push, and CI wait. None has been performed.
2. Do not create a tag or release; that remains explicitly unauthorized.

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
- Low-severity review survivor: in the admission test's failure-only cleanup path, release admission and open the gate before draining the runner so a hypothetical future lock regression diagnoses quickly instead of burning the 30-second drain; passing runs are unaffected.
- Document in README/SECURITY that resuming an existing Claude Code conversation under a different backend (for example continuing an Anthropic-backed session via a proxy-routed launcher) re-sends the prior transcript to the new provider; cross-backend resume should be a deliberate choice.
