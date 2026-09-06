# Shellmates project guidance

Shellmates manages terminal sessions for native Claude Code and Codex CLI, plus
the existing Claude/CLIProxyAPI route. It owns GNU Screen, launching, reconnect,
shutdown, private lifecycle storage and usability, not general agent policy.

## Architecture and commands

- `cmd/sclaude`: shared entry point for sclaude, sclaudex and scodex.
- `internal/app`, `internal/backend`, `internal/ui`: dispatch, vendor boundaries, terminal UI.
- `internal/session`, `internal/screen`, `internal/fssecure`, `internal/stateroot`: lifecycle and private storage.
- `internal/config`, `internal/managedsettings`, `internal/setup`: configuration, optional proxy, installation and doctor.
- `internal/harness`: embedded project-local installer, shared defaults and two skills. Python 3.9+ standard library; no native-profile or session setup.
- `scripts/try-harness.py`: legacy disposable external-harness smoke helper, not the deployment interface.
- Go 1.26.8, macOS/Linux, amd64/arm64 release targets. GNU Screen is the session runtime.
- Build: `go build -o /absolute/disposable/path/sclaude ./cmd/sclaude`.
- Focused Go checks first; ordinary checks are `go test ./...`, `go test -race ./...`, `go vet ./...`.
- Harness/helper checks: `python3 -B -m unittest discover -s scripts/tests -v` (Python 3.9+, standard library). Native localhost fixtures opt in with `HARNESS_NATIVE_SMOKE=1`; no real model or live profiles.
- Native repository-entry freshness is checked by `TestProjectAgentGuidance` in the ordinary Go suite.

Public branding is Shellmates. Launcher names, SCLAUDE_* variables, storage paths
and sclaude_* assets remain compatibility contracts. Codex -p selects a profile;
Claude -p is print mode. Preserve backend argv after the manager's -- delimiter.
An external claudex is opaque. Native auth, permissions, models and skills stay
vendor-owned; never add a permission bypass or silently translate provider flags.

## Safety boundaries

- Run automated tests with temporary HOME, XDG_CONFIG_HOME, XDG_STATE_HOME and XDG_DATA_HOME where user state is involved; use dedicated GOMODCACHE/GOCACHE roots for Go.
- Use localhost fixtures for proxy models/messages checks, never live credentials, OAuth, services or production configuration.
- Never read or modify live ~/.codex/, Claude credential/keychain storage, or live Claude transcript JSONL files during this project's work.
- Never log users out, modify real shell profiles, or run system-level sudo automatically. System-level systemd remains instruction-only.
- Do not read or rewrite an existing external claudex.
- Session records/logs must never persist prompts, backend arguments, authorization headers, API keys or response bodies.
- Keep private preferences and audit material outside this public repository. Distributed harness defaults are public and provider-neutral. Personal preferences require an explicit --local installation in a selected project; never install them into this checkout for publication.

## Review, verification and checkpoints

- Declare a hard cap before a review/verify/fix loop: at most three focused rounds by default. Batch edits, then test.
- Review is explicit, not automatic. Keep reviewers independent, provide actual requirements/relevant source without steering the expected verdict, and use tested permissions rather than a prompt-only read-only promise.
- Stop after no actionable findings or only low-severity findings. Route concrete fixes back to the writer where practical; record survivors in STATUS.md, not another review loop.
- Report failures, skips, warnings, uncertainty and incomplete work. A change is not proof of a fix.
- Update STATUS.md with exact checks/results at each completed tracked task and make a local checkpoint commit. Publication needs separate authorization; no automatic push, tag, release or deployment.
- The project-owned verification/honesty workflows remain under .claude/skills; their instructions apply to either provider. Distributed handover/cross-review sources live once in internal/harness/assets and are installed as versioned project snapshots, never as global policy.

The complete pre-push matrix runs exactly once per separately authorized frozen
tree, following .claude/skills/verify/SKILL.md. Do not change source/config during
it. On failure, record and stop; no silent full rerun. On success, only STATUS.md
changes before the verification checkpoint commit, with no subsequent adversarial
review. A later explicit implementation task starts a new scope; earlier matrix
results remain evidence only for their recorded tree. See STATUS.md for that history.

This file owns repository knowledge. CLAUDE.md and AGENTS.md are generated native
projections, not separate handbooks. Regenerate them together from this file with
`python3 -B scripts/sync-project-guidance.py`. The ordinary Go suite verifies them.
