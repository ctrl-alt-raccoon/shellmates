---
name: verify
summary: Exercise sclaude through its real CLI and GNU Screen surface.
---

# Verify sclaude

Build isolated binaries:

```sh
go build -o bin/sclaude ./cmd/sclaude
go build -o bin/fake-backend ./testdata/fake-backend
ln -sf sclaude bin/sclaudex
```

Use temporary `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_DATA_HOME`. Write a test config pointing both real backends at `bin/fake-backend` and Screen at the host `screen` executable.

Drive these surfaces:

1. `sclaude new --backend claude --topic ... --detach -- --sleep ... --exit ...`, then `sclaude list --json`; verify stopped status/exit and no args in durable metadata.
2. Start a long detached session, observe `Running/Detached`, then stop its exact short ID and observe Stopped.
3. Run non-TTY `sclaude --exit N`; verify exact exit and clean stdout/stderr with no session state.
4. Run `sclaudex` against the fake external backend to exercise invocation-name dispatch.
5. Run the built release artifact (`dist/snapshot/sclaude_<os>_<arch> version`) and isolated `_install-release` into temporary XDG/HOME directories. Never modify real shell profiles or install dependencies.

Gotcha: macOS Screen 4.00.03 returns exit status 1 from `screen -ls` even when a socket is present; parse sockets before treating the exit code as failure.
