# Shared working agreements

These are working defaults, not a security boundary or permission to expand a task.
Follow system and managed policy, the user's current request, and applicable
repository instructions. Flag consequential conflicts rather than guessing.

## Context and ownership

- Discover native instructions and `project.md` before changing a repository.
  Also check legacy `.claude/project.md` and `.claude/code-style.md` when present;
  reconcile conflicting sources. Follow narrower directory instructions.
- Keep shared repository facts, commands and constraints in `project.md`.
  Keep provider-specific compatibility separate. Skills are on-demand workflows,
  not another always-loaded handbook. Do not hand-edit generated instruction blocks.
- Inspect the working tree and preserve unrelated edits. Do not overwrite another
  person's work, expose credentials, publish, deploy, or rewrite history without
  authorization covering the action and target.

## Implementation and communication

- Answer directly, use plain language, and investigate facts before asking routine
  questions. State consequential assumptions and uncertainty. Do not invent access,
  model identity, results, or completion.
- Prefer existing code, native capabilities and the standard library. Add a feature,
  dependency or abstraction only for a demonstrated need today: YAGNI.
- Trace the affected flow, fix root causes, and keep changes scoped. Preserve
  validation, useful types, security, accessibility and protections against data loss.
- Use checks appropriate to the risk and the project's documented commands. Add
  regression coverage for changed behavior; never weaken tests to obtain a pass.

## Review and completion

- Cross-agent review is explicit, not automatic. Use one independent reviewer and
  a bounded, approved packet. Supply requirements and evidence, not an expected
  verdict. The implementer evaluates findings and owns authorized fixes.
- Set a limit before a review/fix loop, normally at most three focused rounds.
  Do not add background agents, recursive review, or orchestration without a real need.
- Wait for owned jobs and inspect their results. Report actual checks, failures,
  skips, and remaining risks. A change is not proof of a fix; a timeout is not approval.
- A handover records established context and remaining work. It does not transfer
  credentials, raw transcripts, or authority for unapproved actions.
