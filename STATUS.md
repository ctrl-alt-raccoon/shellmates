# Status checkpoint

Updated: 2026-09-05

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

Storage hardening and the initial Screen-level lifecycle fixes are committed as `6fbcbe3`. Their focused gate passed; the stronger native integration check below subsequently exposed a remaining end-to-end shutdown defect, so lifecycle reliability is not yet complete:

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

### Native Codex draft: focused gate blocked

The uncommitted native implementation adds:

- Native `codex` backend and `scodex` invocation-name dispatch, with exact backend argv and inherited environment forwarding, no Claude proxy overlays, and backend-specific interactive/noninteractive classification. In particular Codex `-p` selects a profile; it is not Claude print mode. `exec`, `review`, utility commands, help, and version stay direct, including under `SCLAUDE_FORCE`. Unknown options are delegated unchanged.
- Runtime schema 2 with explicit enabled backends, `real_codex`, Codex-only setup/doctor, optional `--codex-executable`, and strict contradictory-option checks. Schema-1 runtime files still load without rewriting. Native state and authentication remain owned by Codex; wrapper setup never reads or migrates `~/.codex/`.
- Native resume pickers instead of treating Screen record IDs as conversation IDs. README/SECURITY now distinguish native Codex, managed Claude proxy routing, optional Claude plugin tooling, and deliberate cross-provider resume.
- Three-launcher installer schema 3 with schema-1/2 migration and schema-2 journal recovery. Existing launcher identities and unmanaged `scodex` collisions are preserved. Candidate-owned update handoff is tested locally. Rollback to pre-native releases is deliberately refused; rollback between native-capable releases is supported.
- Regression tests for native setup, argument boundaries, stdio/exit forwarding, signals, launcher collision/rollback, candidate-owned migration, and opt-in real Screen lifecycle.
- CI/release toolchain pinned to Go 1.26.8, with vulnerability and integration gates added. Go 1.26.8 ran successfully from a disposable cache; the Mac's default Go installation was not changed. The new vulnerability gate and complete cross-build matrix have NOT yet run.

Native focused verification used `/private/tmp/sclaude-native-20260904.79kcft/check.sh`, Go 1.26.8, fresh HOME/XDG/Screen/TMPDIR for each command, and shared disposable Go caches (not user application state). The declared maximum was three rounds:

1. `go test -count=1 ./internal/config ./internal/backend ./internal/app ./internal/setup ./internal/ui ./internal/session ./internal/screen` passed all seven packages, including setup 13.567s. Integration tests were not enabled in this round.
2. `SCLAUDE_SCREEN_INTEGRATION=1 SCLAUDE_RELEASE_INTEGRATION=1 go test -race -count=1 ./internal/config ./internal/backend ./internal/app ./internal/setup ./internal/ui ./internal/session ./internal/screen` passed six packages; app failed a new integration assertion. That first failure was a test bug: unmarshalling into a reused slice retained omitted zero-value JSON fields from the previous running record. The test now decodes fresh records and uses the correct `prune --all-stopped` option. No product metadata fix was needed. Candidate-owned installer migration and real bare Screen lifecycle passed.
3. `SCLAUDE_SCREEN_INTEGRATION=1 SCLAUDE_RELEASE_INTEGRATION=1 go test -race -count=1 ./internal/app ./internal/setup ./internal/session ./internal/screen` passed setup 16.052s, session 2.342s, and screen 4.662s. App failed `TestNativeCodexScreenLifecycle` at `native_integration_test.go:147`: `fake backend survived stop`.

Final-round survivor (high priority; no fourth fix/check round started):

- On this Mac's `/usr/bin/screen`, manager stop removes the Screen socket and writes a stopped record while the login -> runner -> backend process chain can remain alive. Read-only process inspection confirmed the fixture backends were live (`S+`), not zombies, after socket removal: runner/backend 94910/94911 from round 2 and 95109/95110 from round 3, under login processes 94909 and 95108. This is not proved to be Codex-specific; no real vendor backend was invoked. `runSession` currently has no signal forwarding/shutdown protocol, and manager stop only confirms socket absence.
- A follow-up bounded task must design reliable runner/backend shutdown and acknowledgement, preserve stop intent until that stronger condition is met, handle timeout/retry without unsafe PID-reuse signaling, and extend failure cleanup in the new integration test so it cannot leak its fixture if the socket disappears first. Merely weakening the integration assertion would hide the defect.
- All six identified native fixture processes were confirmed absent after explicitly terminating only the two fake backends. The bare health-check test also left sleep-loop processes despite passing its socket-only assertion; its two process groups were identified by their exact fixture command/start times (23:49:40 and 23:50:23) and cleaned up separately. A matching older fixture predating these gates was left untouched. Future health checks need process-exit verification, not only socket verification. No pre-existing user Screen sessions or processes were stopped.

Per `CLAUDE.md`'s three-round cap, native work is left uncommitted and explicitly unfinished. The final one-shot complete matrix has NOT started; full tests/race/vet, vulnerability scan, four-target builds, release checksums, and hosted CI remain unverified for this tree. No live setup, vendor login/inference, shell-profile edits, service changes, plugin execution, push, tag, or release occurred. The earlier audit file is preserved separately as the historical pre-fix snapshot.

Cleanup: removed the three lifecycle-check roots and the native gate's four disposable state roots plus Go module/build caches (approximately 1 GB of regenerable data). Removal commands returned 0; the lifecycle roots were confirmed absent and the native root retains only its 4 KiB harness. The earlier 33 MiB audit artifact root remains intact.

### 2026-09-05: authorized bounded backend-shutdown pass

The user authorized a fresh bounded pass and clarified that Linux over SSH is the principal deployment target. A maximum of three focused fix/check rounds was declared. No complete matrix or adversarial review started.

Shutdown implementation in the working tree:

- The runner claims shutdown responsibility under the store lock BEFORE spawning its backend. If stop wins that lock, a later runner cannot start a child. If the runner wins, exit acknowledgement is required even when startup subsequently fails.
- New records gain `shutdown_protocol: 1` when claimed; `backend_exited` is published only after the owning runner waits for its child (or Start fails). A requested stop remains `stopping` until the backend acknowledgement AND Screen absence are confirmed.
- The runner observes durable stop intent independently of the manager/SSH connection. It sends SIGTERM through the actual `os.Process` handle returned by Start and escalates to SIGKILL after two seconds; it still waits before acknowledging. The manager waits up to five seconds by default and preserves unconfirmed stop intent for retry. Persisted PIDs are never used as signaling authority.
- SIGTERM/SIGHUP and canceled runner contexts trigger shutdown; Ctrl-C is left to the foreground backend, with the runner remaining alive to reap it. Foreground process groups and terminal I/O are not changed.
- Once per second, the runner checks for confirmed Screen absence, covering the Mac login wrapper swallowing HUP. An ambiguous Screen probe alone does not trigger shutdown. Store-access failure initiates child shutdown but cannot forge a durable exit acknowledgement.
- Reconciliation and direct store deletion refuse to treat missing sockets as proof of backend exit, including legacy records with unconfirmed process metadata. New and old uncertain records block prune/uninstall. Legacy runners or an uncatchably killed runner may still require manual recovery.
- Doctor's indefinite shell/sleep loop was replaced with an exec'd terminal reader that exits on terminal EOF/hangup. Its real-Screen test now checks process exit as well as socket removal.
- The native integration fixture now has its own stop-file/bounded-lifetime escape hatch, checks TTY input and survival after the creator returns, and tests both manager stop and externally removed Screen sockets. Unit tests cover acknowledgement ordering, timeout/retry, interrupted startup, missing sockets, legacy PIDs, graceful termination, ignored TERM/escalation, context cancellation, storage failures, and Ctrl-C semantics.

Scope: this confirms exit of the directly launched backend, not arbitrary daemonized descendants. Custom/opaque launchers must exec their real backend or forward signals and wait. This boundary is explicit in README/SECURITY. No real vendor backend, credentials, or SSH server was used; SSH-relevant detach/independent-caller behavior was exercised locally, not an actual network disconnect.

Focused gates used Go 1.26.8 and isolated state. Mac commands ran via `/private/tmp/sclaude-shutdown-20260905.naRRnX/check.sh`. Linux used a disposable Debian Bookworm/arm64 container, GNU Screen 4.9.0, user `nobody`, `--init`, no network during testing, a read-only project mount, and tmpfs HOME/XDG/cache. The official Go image and Screen/modules were obtained during separate test-image preparation; no Mac packages or services were changed.

1. Round 1, both platforms: `SCLAUDE_SCREEN_INTEGRATION=1 go test -race -count=1 ./internal/session ./internal/app ./internal/screen`.
   - Linux passed all three packages (session 1.505s, app 22.553s, screen 1.117s), including the real native/Screen lifecycle.
   - Mac session 2.374s and screen 2.324s passed; app failed the new TTY-input fixture assertion, before manager stop. The fixture was corrected to select Screen window 0 explicitly and inject carriage return as Enter. No shutdown production code changed for this correction.
2. Round 2, both platforms: `SCLAUDE_SCREEN_INTEGRATION=1 SCLAUDE_RELEASE_INTEGRATION=1 go test -race -count=1 ./internal/session ./internal/app ./internal/screen ./internal/setup`.
   - Mac session 3.381s, app 11.524s, and screen 10.222s passed: the original live-backend leak no longer reproduced, both manager and socket-loss shutdown passed, and the health-check process exited. Setup failed `TestEnsureProxyConfigRemovesSpecialModeBits/setgid` before reaching the production assertion because the disposable root inherited group `wheel`, not the running user's group 20. The harness now sets the root to the caller's primary group before creating fresh state.
   - Linux session 1.191s, app 43.070s, and screen 1.449s passed. Setup failed `TestRunWorkflowRejectsCustomExecutableBeforeProxyMutation`: expected `active brew --prefix`, got `resolve Homebrew executable: brew: executable file not found in $PATH`.
3. Round 3, Mac: the same race command expanded with `./internal/config ./internal/backend ./internal/ui`. Session 3.354s, screen 16.878s, setup 26.645s, config 4.585s, backend 3.842s, and UI 3.234s passed. App failed `TestRunSessionStartsWhileCreatorHoldsStateRootAdmission` at `dispatch_test.go:448`: runner exited 143 before startup observation. The native integration and new shutdown tests did not report failures. No fourth round or silent full-matrix rerun was performed.

Final-round fixture survivors, diagnosed read-only and NOT repaired after the cap:

- The startup-admission test launches `_run-session` directly with no Screen session, but points its runtime at real `/usr/bin/screen`. Its slow-path execution now correctly trips the runner's missing-socket watchdog after one second. Keep the admission-lock contract; provide a synthetic Screen liveness fixture for the exact record, rather than weakening the production watchdog or shortening the test deadline.
- The custom-Homebrew-executable workflow test creates a fake `brew` only on PATH. Production intentionally ignores PATH and resolves trusted `/opt/homebrew/bin/brew` or `/usr/local/bin/brew`, so the test accidentally relies on the Mac's installed Homebrew and fails on Linux without it. Its no-proxy-mutation assertion remains important. Adapt the test's trusted-prefix seam/expected fail-closed unavailable case without installing Homebrew on Linux or relaxing production executable trust.

The shutdown implementation and earlier native draft remain uncommitted because the broader focused suite is not green. The original end-to-end leak has passing real-Screen regression evidence on BOTH platforms, but this is not a claim that all verification is complete. Full tests/race/vet, vulnerability scan, cross-build/release assets, hosted CI, and final verification checkpoint remain pending. `gofmt` and `git diff --check` passed. No live account, vendor inference, user Screen session, shell profile, plugin, remote repository, tag, or release was mutated.

Cleanup for this pass: both disposable Linux containers removed themselves; no matching Mac fixture backend/runner remained in the scoped process check. Removed the exact unused test image `sclaude-shutdown-check:20260905-narrnx` and newly pulled `golang:1.26.8-bookworm` tag (base digest `sha256:9fdc884aacc3bec89b20ffc69f4bb369c78210e3e4f600387b5128b12c199f81`), without global Docker pruning. Removed approximately 584 MiB of disposable Mac caches and three state roots. Every removal returned 0. The small harness/Dockerfile/module-manifest recipe remains under the named temporary root for reproduction; prior audit evidence remains unchanged.

### 2026-09-05: fixture repair and native/shutdown implementation checkpoint

The user authorized continuing the proposed verification work and explicitly requested a persistent goal. The goal covers the bounded fixture repair, Mac/Linux checks, an isolated real SSH regression, an implementation freeze, one complete matrix, and an agent-scaffolding boundary recommendation. It does not authorize deployment, remote mutation, or an unbounded claim of perfection.

The newly declared maximum was three focused fix/check rounds; the pass ended successfully after two:

1. Repaired `TestRunSessionStartsWhileCreatorHoldsStateRootAdmission` with a synthetic live socket for its exact record. It now waits for the watchdog probe while admission remains held, and releases admission before draining on failure. The production watchdog is unchanged. Repaired the custom Homebrew workflow fixture through an internal resolver dependency seam: production still uses only its fixed trusted locations; the test supplies a fixture-owned trusted installation and isolates all three XDG roots. No Homebrew installation on Linux or PATH trust relaxation was used.
   - Mac: `SCLAUDE_SCREEN_INTEGRATION=1 SCLAUDE_RELEASE_INTEGRATION=1 go test -race -count=1 ./internal/session ./internal/app ./internal/screen ./internal/setup ./internal/config ./internal/backend ./internal/ui` passed, respectively 2.557s, 11.979s, 10.598s, 22.116s, 2.026s, 2.757s, 3.067s.
   - Linux arm64, unprivileged container, same command: all passed, respectively 1.184s, 15.505s, 1.083s, 13.488s, 1.021s, 1.016s, 1.009s.
   - The new opt-in SSH test initially failed before application setup with `Permission denied (publickey)`. Its generated authorized key was outside the account's passwd home, beneath world-writable `/tmp`; OpenSSH strict path checks remained enabled. This was a test-server fixture failure, not evidence of an application lifecycle failure.
2. Moved SSH fixture files beneath the test account's real home on a private container tmpfs, with explicit key ownership and private mode. `SCLAUDE_SSH_INTEGRATION=1 go test -race -count=1 -v -run '^TestLinuxSSHReconnect$' ./internal/app` passed (test 9.97s, package 10.982s). Unprivileged native-only setup, detached creation, two abrupt SSH transport losses, reattach/input to the same backend PID, acknowledged stop, backend absence, and prune all passed. No product code changed in this round. The third round was not needed.

The native Codex, configuration/argument separation, installer migration, runner shutdown acknowledgement, and related fixtures are now ready for their implementation checkpoint. The final complete matrix has NOT started at this boundary. Both platforms used Go 1.26.8. The SSH test uses OpenSSH and Screen 4.9.0 in a disposable Linux container, binds only its loopback interface, generates fixture keys, and invokes no vendor backend. It does not simulate silent half-open TCP connections, prove behavior of real Claude/Codex releases, or guarantee exit of daemonized descendants beyond the documented launcher contract.

Preparation: the pinned `govulncheck@v1.7.0` was built in a disposable directory, not installed globally. The Linux test image contains declared modules and test dependencies before the matrix. The first image build emitted a tar warning because its overly broad temporary build context overlapped a growing Go cache; the build itself exited 0 and copied only the two module manifests. The checked-in SSH recipe now has a restrictive `.dockerignore`. No host SSH service, live credentials, plugin execution, shell profile, remote repository, tag, or release was changed.

Agent scaffolding recommendation: keep this repository focused on reliable Screen sessions, backend boundaries, SSH recovery, workflow automation and usability. The existing `CLAUDE.md` and three small project verification/reporting/review skills are appropriate project-specific guidance. There is currently no tracked `AGENTS.md` or `.agents/skills` adapter; the optional pinned Claude Code Codex plugin declaration is separate from native `scodex`. Keep reusable personal/team agent rules, general skills, model defaults, MCP/plugin bundles and opt-in bootstrap templates in a separately versioned agent-toolkit repository; never put credentials there. Future thin Claude/Codex entrypoints here should share project rules rather than duplicate policy. This follows the supported global-versus-repository distinction in [official Codex customization guidance](https://learn.chatgpt.com/docs/customization/overview) and [skill scopes](https://learn.chatgpt.com/docs/build-skills). No scaffold migration or new repository was performed.

### One-shot final matrix from `2cbc319`: stopped at offline module-cache preparation error

Source/configuration were frozen and committed at `2cbc319`. The historical `AUDIT-2026-09-04.md` remained untracked and was excluded from the checkpoint and frozen source archive. The repository Verify skill's one-shot rule was used; no adversarial review or source/config edits followed the matrix start.

- Static gate passed: `gofmt -l .` empty, `sh -n install.sh scripts/build-release.sh`, ShellCheck for those scripts, safe Ruby YAML parsing of both workflows, JSON parsing of `.claude/settings.json`, and `git diff --check`. The command and cleanup both exited 0; `/private/tmp/scv.w5zN2n` was confirmed absent.
- `go mod verify` then exited 1: `gopkg.in/yaml.v3@v3.0.1 requires gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405: module lookup disabled by GOPROXY=off`. Cleanup exited 0; `/private/tmp/scv.PQ0Kh6` was confirmed absent.
- Read-only diagnosis: `go.sum` already contains both the module and go.mod checksums for this dependency, and yaml.v3's own `go.mod` requires it. The prepared seed contained only `cache/download/gopkg.in/yaml.v3`, not `check.v1`. The agent's offline-cache preparation was incomplete; no product assertion failed at this gate, but module verification did NOT pass.
- Skipped after that first failure: the matrix's static plugin test, full tests/race/vet on Mac and Linux, vulnerability scan, four-target release build/checksums, isolated install lifecycle, final runtime/doctor/proxy regressions, and final plugin contract check. The earlier focused Mac/Linux races and isolated SSH test remain passing evidence, not substitutes for those skipped gates. The scanner was prepared but NOT run.

No second complete matrix was started. A fresh authorization was requested to prepare the complete declared module graph (`go mod download all`, including dependency tests), validate the disposable cache preparation, and run one new matrix against the same frozen implementation. This should change only verification scaffolding and status, not product source. The goal remains open; this is not a release-ready or perfect-state claim.

Cleanup: the three focused Linux containers removed themselves and the scoped Docker query returned no matching container. A scoped Mac executable-name-only process check returned no matching fixture (the initial sandbox-denied check was repeated with approved read-only access). The two focused HOME/XDG roots and approximately 329 MiB of build cache were removed with status 0. Both matrix roots were already removed and checked by their wrappers. The isolated module/tool seed (about 350 MiB), named test image `sclaude-ssh-check:20260905-epf0hs`, logs, and small frozen-source/harness snapshot are retained for the requested follow-up; they contain test tooling and fixtures, not live account configuration. No global Docker prune was used.

### Newly authorized matrix from `2cbc319`: passed

The user explicitly authorized completing the dependency cache and running one fresh verification matrix without changing product source. This supersedes only the prior attempt's exhausted one-shot allowance; no push, deployment, source repair, or additional matrix is authorized.

Cache/tool preparation was capped at three focused rounds and completed in the first. `go mod download -json all` populated the declared yaml.v3 dependency-test module `gopkg.in/check.v1` with the checksums already present in `go.sum`; no tracked manifest changed. The Mac wrapper then passed `go mod verify` with `GOPROXY=off` in a fresh root (command 0, cleanup 0, root absent). A tiny temporary Dockerfile extended only the retained test image's module cache using `go mod download all && go mod verify`; the actual unprivileged Linux matrix wrapper also passed offline `go mod verify` and cleanup with status 0. The image is `sclaude-ssh-check:20260905-complete`. `govulncheck -version` confirmed Go 1.26.8, scanner v1.7.0, and the official vulnerability database updated 2026-09-02; this was tooling preflight, not a vulnerability scan.

Before the new matrix, `git diff --exit-code 2cbc319 -- . ':!STATUS.md'` was clean, and SHA-256 comparison confirmed the disposable frozen archive matches every tracked file except the historical status log. Release output for this matrix did not exist. Source/configuration remain frozen at `2cbc319`; only this status log changed. The new matrix ran exactly once, with separately inspected command and cleanup statuses and fresh HOME/XDG/module/build-cache roots for each Go gate.

All 24 gates below passed. Every command and per-command cleanup returned 0 before the next gate started. No gate was retried, no source/config changed, and no adversarial review was started.

Harness: Mac commands used `sh /private/tmp/sclaude-finish-20260905.Epf0HS/matrix.sh <label> <command>` with `v2-` labels and a new `/private/tmp/scv.*` root each time. The wrapper selected Go 1.26.8, cleared the inherited environment, seeded independent module caches, and set `GOPROXY=off`, `GOSUMDB=off`, and `GOFLAGS=-mod=readonly`. Linux commands used `docker --config /private/tmp/sclaude-finish-20260905.Epf0HS/docker-config --host unix:///var/run/docker.sock run --rm --init --name sclaude-matrix-v2-<gate> --network none --read-only --user nobody --pids-limit 256 --memory 3g --cpus 2 --tmpfs /tmp:rw,exec,nosuid,size=2g`, read-only source and `linux-matrix.sh` mounts, and `sclaude-ssh-check:20260905-complete sh /matrix.sh <command>`. Each Linux container had its own fresh HOME/XDG/module/build-cache root. The SSH gate alone omitted `--user nobody` for sshd privilege separation and added the private `/home/sclaude-test` tmpfs; its application/backend commands still ran as UID 1000.

1. Static gates (three commands): `static.sh` passed empty `gofmt -l .`, `sh -n install.sh scripts/build-release.sh`, ShellCheck of those scripts, safe YAML parsing of both workflows, JSON parsing of `.claude/settings.json`, and `git diff --check`; `go mod verify` reported `all modules verified`; `go test -count=1 -run '^(TestProjectCodexPluginDeclaration|TestManagedProxyPreservesDefaultSettingSources)$' ./internal/backend` passed (0.724s).
2. Full Go gates (six separate commands):

   | Platform | `go test ./...` | `go test -race ./...` | `go vet ./...` |
   | --- | --- | --- | --- |
   | macOS arm64 | All packages passed; app 8.114s, setup 17.135s | All packages passed; app 7.880s, setup 17.499s | Passed, no diagnostics |
   | Linux arm64 | All packages passed; app 1.665s, setup 0.573s | All packages passed; app 2.736s, setup 2.398s | Passed, no diagnostics |

3. Vulnerability gates (four commands): `env GOOS=<os> GOARCH=<arch> CGO_ENABLED=0 govulncheck ./...` for darwin/arm64, darwin/amd64, linux/arm64, and linux/amd64. All four reported `No vulnerabilities found.` using the pinned v1.7.0 scanner and official Go vulnerability database. This is a known-vulnerability scan, not a guarantee against undiscovered defects.
4. Release gate: `release-check.sh` ran the frozen archive's `scripts/build-release.sh v0.0.0-matrix.2cbc319`, building all four `CGO_ENABLED=0` targets. It required exactly `SHA256SUMS`, `install.sh`, `sclaude_darwin_amd64`, `sclaude_darwin_arm64`, `sclaude_linux_amd64`, and `sclaude_linux_arm64`. `shasum -a 256 -c SHA256SUMS` returned OK for every entry. `file` confirmed both Mach-O architectures and both statically linked ELF architectures. Nothing was published.
5. Installer gates (Mac and Linux): `SCLAUDE_RELEASE_INTEGRATION=1 go test -count=1 -run 'Test.*(Install|Update|Rollback|Uninstall|Purge|Ledger|Journal|NativeLauncher)' ./internal/setup ./internal/app`. Mac setup/app passed in 7.979s/1.260s; Linux in 0.625s/0.003s. Coverage includes private layout/ownership, journal recovery, preservation, update/rollback/uninstall/purge, legacy launcher migration, and the actual candidate-owned installer subprocess.
6. Built-release runtime gates (three commands): built `testdata/fake-backend` and the previously prepared disposable `cmd/matrix-smoke` helper separately, then ran that helper with the built Darwin/arm64 release and fake backend. Real Screen reached detached state with matching backend PID/marker, consumed the private launch, retained no backend flags in the session record, acknowledged stop, and removed its socket. The helper confirmed backend process absence during cleanup. Direct non-TTY `sclaude` and disposable `sclaudex` invocations each produced exact empty stdout/stderr, exit 7, and no extra records. Doctor passed all six configured checks and left no disposable socket.
7. Native/Screen runtime gates (Mac and Linux): `SCLAUDE_SCREEN_INTEGRATION=1 go test -count=1 -run '^(TestRealScreenLifecycle|TestNativeCodexScreenLifecycle)$' ./internal/screen ./internal/app`. Mac screen/app passed in 0.694s/2.938s; Linux in 0.106s/0.823s. These cover native argv/TTY boundaries, manager stop, externally removed sockets, direct backend exit, health-check process exit, and prune.
8. Linux SSH gate: `SCLAUDE_SSH_INTEGRATION=1 go test -race -count=1 -v -run '^TestLinuxSSHReconnect$' ./internal/app` passed (test 10.08s, package 11.096s). It exercised unprivileged native-only setup, detached creation over SSH, two abrupt transport disconnects, reattach/input to the same backend PID, acknowledged stop, confirmed process absence, and prune. The SSH server used generated fixture keys and only container loopback; no host port was published.
9. Local proxy gate: `go test -count=1 -run '^(TestCommandVerify|TestAuthenticatedProxyVerification|TestVerifyProxyModels|TestVerifyProxyInference)' ./internal/app ./internal/setup` passed (1.009s/1.078s), using authenticated localhost models/messages fixtures only.
10. Final plugin gate: `go test -count=1 -run '^(TestProjectCodexPluginDeclaration|TestManagedProxy)' ./internal/backend` passed (0.521s). This checks the pinned optional declaration and the private managed overlay while preserving normal setting sources; no plugin was installed, loaded, or invoked.

Cleanup and completion audit:

- All 18 Mac matrix roots and both offline-cache/scanner preflight roots were independently confirmed absent. Each Linux wrapper returned cleanup 0 and all six matrix containers removed themselves. Scoped Docker queries found no remaining matrix container or container using either test image.
- A Mac executable-name-only process check against the exact disposable paths returned no matching process; no process arguments or live user sessions were inspected. The runtime regressions separately asserted their fixture backend/health-check process exits.
- Removed approximately 380 MiB of disposable module/tool caches, binaries, and extracted frozen source, plus the two task-specific test images, with status 0. Shared base images and unrelated Docker state were left intact; no global prune was used. Small logs, the checksum manifest, source archive, and reproduction helpers remain in `/private/tmp/sclaude-finish-20260905.Epf0HS` (about 1.2 MiB). Generated artifacts can be rebuilt from `2cbc319`; no user data was deleted.
- Final `git diff --exit-code 2cbc319 -- . ':!STATUS.md'` remained clean. Both repaired fixture definitions are present; full cross-platform checks and the opt-in regressions provide current evidence for them. The agent-scaffolding assessment and repository/toolkit boundary recommendation above remain recorded and were already delivered to the user.
- This completes the approved local verification objective. Real vendor inference/login, silent half-open SSH connections, arbitrary daemonized backend descendants, and hosted CI were not exercised. No live credentials, user service/profile state, plugin execution, remote repository mutation, push, tag, release, or deployment occurred. The separately authorized next stage may be a real-backend pilot on the intended Linux host; it is not part of this completed matrix.

## Shellmates rename and first publication — 2026-09-05

The user selected Shellmates, provided `https://github.com/ctrl-alt-raccoon/shellmates`, explicitly authorized pushing, and requested a complete README/documentation handoff. This is a new, scoped follow-up to the completed `2cbc319` verification freeze, not a retry of that matrix. The rename/focused correction loop is capped at three rounds; the new final frozen tree gets one complete matrix, with no source/config edits afterward and no remote mutation on failure.

GitHub main initially contained only license commit `e29cdeb4c19862d0e4c29d8ee5c27ac3bf26cb7f`. Its `LICENSE` is byte-identical to the local copy. The two histories were unrelated; a conflict-free, non-squashing merge preserves both histories and permits a normal main push without force. GitHub had no releases at inspection. The untracked historical `AUDIT-2026-09-04.md` is excluded from publication.

Implemented the Shellmates README/front-page branding, Go module/import path, bootstrap and updater default repository, and help/chooser headings. Added `CONTRIBUTING.md`, source-build/setup instructions, documentation links, and explicit notice that release downloads are not yet available. The MIT license, security policy, and CI/release workflows remain present. Launcher names, `SCLAUDE_*` variables, config/state/data paths, private-file/session naming, install ledgers, and `sclaude_*` asset names are intentionally unchanged. No `shellmates` executable or storage migration was introduced.

The Claude scaffolding remains tracked: all three `.claude/skills` definitions and `.claude/settings.json` are unchanged; only the project heading in `CLAUDE.md` changed. Runtime backend boundaries, the fake-backend fixture, and isolated SSH regression remain intact apart from Go imports. No Codex-specific `AGENTS.md`/`.agents/skills` adapter or reusable personal harness configuration was added; forthcoming house rules remain a separate task.

Focused round 1 passed: `go test -count=1 -run '^(TestUpdate.*|TestReleaseAssetName|TestBootstrapDefaultReleaseRepository|TestRunHelpCommand|TestStoppedSessionOffersNativeResumeOrNew)$' ./internal/setup ./internal/app ./internal/ui` (setup 0.679s, app 0.748s, UI 1.077s; command 0, cleanup 0, disposable root confirmed absent). New coverage verifies the Shellmates default latest/manifest/binary paths with a localhost fixture, retains the old asset names and repository override, and checks the branded help/chooser. `gofmt` and `git diff --check` passed; a tracked-tree search found no old repository URL outside this historical status log.

Verification preparation used only `/private/tmp/shellmates-publish-20260905.wzkxbf`: Go 1.26.8, all checksum-declared module dependencies, and govulncheck v1.7.0 were prepared successfully. Disposable image `shellmates-verify:20260905-wzkxbf` contains Go 1.26.8, GNU Screen, OpenSSH fixtures, and a complete offline module cache; `go mod verify` passed during its build. Docker warned that its legacy builder is deprecated; noninteractive package installation emitted expected debconf/service-start warnings inside the disposable image. No host services or vendor tools were installed or changed.

The rename and documentation were committed as `f670b0175f16d6ce708bc77e4ebe0074f83e0368`, a merge commit preserving both local history and GitHub's license-only initial commit. All 103 tracked files matched the disposable frozen archive by SHA-256. The project `verify` and `honesty` skills govern isolation, stop-on-first-failure, and evidence reporting. No tag, release, deployment, repository-permission change, or live vendor invocation is authorized by this task.

### Frozen rename matrix: stopped at a temporary documentation-checker error

Before the matrix, `matrix.sh preflight-modules go mod verify` passed offline (command 0, cleanup 0, root absent); the unprivileged Linux wrapper also passed offline module verification and cleanup with status 0. `matrix.sh preflight-scanner govulncheck -version` confirmed Go 1.26.8, scanner v1.7.0, and official database timestamp 2026-09-02 19:12:04 UTC (command 0, cleanup 0, root absent). These were preparation, not final matrix gates or vulnerability scans.

The first matrix command was `sh /private/tmp/shellmates-publish-20260905.wzkxbf/matrix.sh matrix-static sh /private/tmp/shellmates-publish-20260905.wzkxbf/static.sh`. Empty `gofmt -l .`, `sh -n install.sh scripts/build-release.sh`, ShellCheck, workflow YAML parsing, and `.claude/settings.json` parsing succeeded before the temporary Ruby documentation-link checker failed at `docs-check.rb:3`: `invalid byte sequence in US-ASCII (ArgumentError)`. Matrix command status was 1; cleanup was 0 and `/private/tmp/smv.k29Ifw` was confirmed absent. The final whitespace subcheck in that script did not run; the separate pre-freeze whitespace check had passed.

This is an agent-authored verification-harness error: the wrapper clears the inherited environment, Ruby defaults to US-ASCII, and `File.read` did not explicitly select UTF-8. Focused read-only diagnosis reproduced `Encoding.default_external == US-ASCII` under `env -i` and independently confirmed all four input documents are valid UTF-8. The helper has not been changed or rerun. Correcting its file-decoding declaration is the proposed next step, not an application-source fix.

The one-shot stop rule was followed. No later matrix gate ran: final module/plugin gates, full Mac/Linux normal/race/vet suites, vulnerability scans, cross-build/checksums, installer regressions, real Screen/native runtime, Linux SSH, local proxy, and final plugin checks remain unverified for `f670b01`. The earlier complete matrix still applies only to the prior `2cbc319` source. No second matrix, adversarial review, origin write, metadata change, push, tag, release, or deployment occurred. A separately authorized helper repair and fresh matrix are required before publication.

Post-stop `git diff --exit-code f670b01 -- . ':!STATUS.md'` was clean, and a scoped Docker query found no remaining container using the test image. All per-command Mac test/preflight roots were removed by their wrappers. The 505 MiB preparation cache, scanner, frozen source, small logs/helpers, and named disposable test image are retained for that potential follow-up; no live vendor/account data is included. The local Git remote list is still empty, and the historical audit remains untracked and excluded.

### Authorized helper repair and fresh frozen matrix — 2026-09-06

The migration follow-up explicitly authorized the temporary checker repair and
one new frozen-source matrix, but prohibits publishing, pushing, tagging,
releasing or deploying. This supersedes the earlier publication permission for
the current run. Shellmates remains the terminal/session layer; the general
agent harness is a separate repository.

Changed only the temporary `docs-check.rb` helper: both `File.read` calls now
specify `encoding: 'UTF-8'`. Its cleared-environment preflight passed. No
application source, tracked configuration, dependency declaration or test was
changed. All 102 non-STATUS tracked files still matched the frozen `f670b01`
archive by SHA-256; `git diff --exit-code f670b01 -- . ':!STATUS.md'` passed before
and after the matrix.

All **24 fresh gates passed**, each with command status 0 and cleanup status 0:

1. Static formatting, shell syntax/ShellCheck, YAML/JSON, UTF-8 documentation
   links and whitespace; offline module verification; static plugin declaration.
2. Full normal, race and vet checks on macOS arm64 and Linux arm64, using Go
   1.26.8 and isolated HOME/XDG/module/build roots. Mac normal app/setup:
   8.850s/17.102s; race: 8.378s/17.264s. Both Linux suites and both vet gates passed.
3. Four vulnerability gates (Darwin/Linux, arm64/amd64), pinned govulncheck v1.7.0:
   each reported `No vulnerabilities found.` This is a known-vulnerability scan.
4. Release cross-build/checksums for `v0.0.0-matrix.f670b01`: the four binaries,
   installer and checksum manifest were the exact six expected assets. All
   checksums passed. No asset was published.
5. Mac/Linux installer/update/rollback/uninstall/purge/ledger/journal/native
   launcher regressions; separate fake-backend and runtime-probe builds; built
   release runtime, including private argv consumption, detached Screen, backend
   PID/marker, acknowledged stop/socket removal, direct non-TTY exit 7 without
   output or extra records, and all six doctor checks.
6. Real Screen/native lifecycle checks on Mac and Linux, followed by the Linux
   SSH race regression: test 9.86s, package 10.872s. It covered two abrupt transport
   disconnects, reattach/input to the same backend PID, acknowledged shutdown,
   process absence and prune. The SSH fixture used container loopback, disposable
   keys, no host port and no live account.
7. Authenticated localhost proxy tests and the final plugin contract gate. No
   live plugin was loaded, installed or invoked.

Logs/helpers and the prepared offline cache remain in
`/private/tmp/shellmates-publish-20260905.wzkxbf`; the gate ledger is
`/private/tmp/agent-harness-migration-20260906.zNN0f2/shellmates-results.json`.
All wrappers confirmed cleanup; scoped Docker inspection found no remaining
container using `shellmates-verify:20260905-wzkxbf`. The named preparation image
and cache remain available for the separate Linux stage; no global prune or user
data deletion was performed.

This closes the temporary Ruby blocker and the fresh local matrix. It does not
prove real vendor inference/login, silent half-open SSH behavior, arbitrary
daemonized descendants, hosted CI, or a deployed Linux installation. Read-only
inspection of an existing SSH host found native Claude/Codex and Python 3.13.5,
but no Screen/scodex in the checked locations. That pilot remains separate.

The existing Codex terminal status-line configuration was checked read-only by
the separate harness migration: model/effort, context remaining/window, used
tokens, five-hour/weekly limits, current directory and Git branch remain selected.
No status-line setting or desktop-app configuration was changed.

## Remaining tasks

1. Complete the real Linux/SSH backend pilot, including native arguments,
   reconnect, clean shutdown and a genuinely half-open transport. The new local
   Mac/Linux matrix for `f670b01` is complete and passed. Do not rerun it merely
   because external pilot work remains.
2. General house rules and shared skills belong to agent-harness. A future
   Shellmates instruction merge must preserve its project-owned verification
   discipline and the current frozen-source boundary.
3. Publication is ready for a separately authorized next stage, not authorized
   by this migration. No origin was added, and no push, tag, release, deployment
   or hosted-CI observation occurred.

## Key decisions and constraints

- `sclaude` routes to ordinary Claude Code/Anthropic; managed `sclaudex` keeps Claude Code as the harness and routes through CLIProxyAPI at `http://127.0.0.1:8317`.
- `scodex` runs native Codex, not Claude over the proxy. The complete local Mac/Linux matrix, release cross-builds, and isolated Linux SSH reconnect regression passed from `2cbc319`. Actual deployment, real-vendor pilot, and hosted CI remain separately scoped.
- An existing external `claudex` remains opaque and is referenced only by a stable absolute path.
- Prompts/backend arguments exist only in private one-use launch files and are never persisted in session records.
- Automated verification uses temporary HOME/XDG roots and explicit localhost fixtures; it must not touch live OAuth, proxy, service, shell-profile, or credential state.
- Never read or modify `~/.codex/`, inspect Claude credential/keychain storage, expose secrets, log users out, or automatically run system-level `sudo`.
- Existing release assets must never be overwritten in place; fixes require a new release tag.
- The September 5 Shellmates follow-up authorized the main push and matching
  branding. The September 6 migration explicitly withholds all outward mutation
  during this run; a later publication step needs renewed authorization.
- Create a local commit after each tracked task is completed and update this file at each boundary.
- After each authorized complete matrix starts, only `STATUS.md` may change before its verification checkpoint commit.
- Do not run another adversarial review. Remaining ideas belong in the backlog below.

## Backlog

- Add local `actionlint` coverage in a future task if a trusted installation path is selected; do not block this task on installing it.
- Consider automating GitHub attestation verification in the installer in a future release, with a separately reviewed trust model.
- Add thin Codex project guidance when the user supplies the forthcoming house rules; keep cross-project agent behavior in a separate toolkit and avoid automatic vendor-config changes.
- A user-authorized real-vendor pilot on the intended Linux host, plus silent half-open SSH/keepalive behavior, remains distinct from the isolated fake-backend regression.
