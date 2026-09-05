# Shellmates development rules

## Safety boundaries

- Run automated tests with temporary `HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_DATA_HOME` where user state is involved.
- Use explicit localhost test fixtures for proxy models/messages checks. Do not use live CLIProxyAPI credentials, OAuth state, services, or production configuration.
- Never read or modify `~/.codex/`, Claude credential/keychain storage, or live Claude transcript JSONL files.
- Never log users out, modify real shell profiles, or run system-level `sudo` automatically. System-level systemd remains instruction-only.
- Treat an existing external `claudex` as opaque. Do not read or rewrite it.
- Session records and logs must never persist prompts, backend arguments, authorization headers, API keys, or response bodies.

## Review and verification

- Declare a hard cap before every review/verify/fix loop; the default maximum is three rounds.
- End a loop when a review finds nothing or only low-severity issues. Record low-severity and final-round survivors in `STATUS.md` instead of starting another round.
- Route review findings back to the writer that made the change while that context is available. Use a fresh reviewer with scope only—paths, commit, branch, or area—and do not steer the expected findings.
- Report failing or skipped checks, warnings, uncertainties, and incomplete work explicitly. Do not claim a concern is fixed without evidence.
- Batch implementation edits, then run the relevant focused gate. Reserve the complete matrix for the approved final frozen tree.

## Checkpoints and commits

- Update `STATUS.md` whenever a tracked task is completed. Record exact verification commands/results and maintain a backlog for deferred ideas.
- Commit each completed tracked task separately. Push only after the approved pre-push gates pass.
- Do not create a tag or release unless the user explicitly authorizes that outward action.

## Current stop rule

For the current pre-push task, finish the approved implementation, run the complete final verification matrix exactly once, and do not start another adversarial review. If the matrix fails, record the failure and stop without remote mutation. If it passes, only `STATUS.md` may change before the verification checkpoint commit.
