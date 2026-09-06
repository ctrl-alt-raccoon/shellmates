# Contributing to Shellmates

Shellmates focuses on reliable GNU Screen sessions, clear backend boundaries, SSH reconnection, and straightforward terminal workflows. Keep reusable personal agent instructions, general skills, and model defaults outside this repository; project-specific guidance belongs here.

## Getting started

Follow [Build from source](README.md#build-from-source), then read the shared development and safety rules in [project.md](project.md). CLAUDE.md and AGENTS.md project those same rules into native discovery; neither is a separate handbook. The optional project plugin is not a build or test dependency; do not install or invoke it merely to contribute.

Use Go 1.26.8, matching CI, and GNU Screen. macOS and Linux are supported; release cross-builds cover amd64 and arm64 on both. The public project is Shellmates, but command names, `SCLAUDE_*` variables, on-disk paths, and release filenames remain compatibility contracts, not unfinished search-and-replace work.

## Changes and tests

1. Keep changes scoped and include a regression test when behavior changes. Do not mix unrelated cleanup or agent-configuration changes into a fix.
2. Run the relevant focused tests before freezing the change. Tests that can resolve user state need fresh temporary `HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_DATA_HOME`, plus dedicated `GOMODCACHE` and `GOCACHE`. Prepare dependencies before the final matrix, and check both test and cleanup exit statuses.
3. Follow the [verification checklist](.claude/skills/verify/SKILL.md) for the one-shot final pre-push matrix: formatting, modules, shell/workflow validation, normal tests, race tests, vet, four-platform builds/checksums, installer lifecycle, fake-backend Screen lifecycle, and static plugin compatibility. Stop at the first failure and record it; do not silently retry the full matrix.
4. Update [STATUS.md](STATUS.md) with exact checks, outcomes, and any skipped coverage. Keep changes and verification checkpoints in Git; submit a focused pull request with a short explanation of the user-visible result.

The ordinary Go suite checks generated project guidance without requiring the
private agent-harness repository. Run `python3 -B -m unittest discover -s scripts/tests -v`
for the optional trial helper. See [the portable harness trial](docs/HARNESS.md);
never enroll this public checkout with private personal preferences for publication.

The opt-in [Linux SSH regression](testdata/ssh/README.md) runs an isolated SSH server and fake backend inside a disposable container. It does not require a real remote host or vendor account. Real vendor login/inference and deployment pilots are separate, explicitly authorized work.

Never use live vendor credentials, transcripts, proxy services, or shell profiles as test fixtures. Use authenticated localhost fixtures for proxy checks. Do not automatically install vendor tooling, log users out, run system-level `sudo`, or create release tags. Report security issues privately as described in [SECURITY.md](SECURITY.md).

## Finding your way around

- `cmd/sclaude`: the shared executable entry point for all three launchers.
- `internal/app`, `internal/backend`, and `internal/ui`: command dispatch, vendor boundaries, and terminal interaction.
- `internal/session`, `internal/screen`, `internal/fssecure`, and `internal/stateroot`: lifecycle and private storage.
- `internal/config`, `internal/managedsettings`, and `internal/setup`: configuration, optional proxy overlay, install/update/setup/doctor.
- `testdata`, `scripts`, and `.github/workflows`: test fixtures, release builds, and hosted verification/publication.

Contributions are covered by the repository's [MIT license](LICENSE).
