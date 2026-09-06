---
name: handover
description: Summarize established work for a human or another coding agent when asked for a handover, session transfer, or continuation brief. Does not publish, change files, or start the next task.
---

Produce a short, self-contained handover from facts already established:

- Goal, repository/branch, and working-tree state if known.
- Changes and remaining work, with relevant paths and exact check results.
- Decisions and reasons; consequential assumptions, failures, and unverified behavior.
- The next concrete action, pending approvals, and boundaries such as "do not push".
- Pointers to `project.md` and applicable native instructions, not another handbook copy.

Do not invent a clean tree, completed job, or session ID. State unknowns.
Do not include credentials, raw transcripts, or unnecessary personal information.
Do not call tools solely for this summary unless fresh status was requested.
Save or send the handover only when requested; use the requested destination or
ask for one, and never overwrite an existing file without authorization.
A handover does not authorize the next agent to perform unapproved actions.
