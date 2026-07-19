---
name: honesty
summary: Report implementation and verification status without hiding gaps.
---

# Honest status

Report the state of the supplied task from evidence in the current tree and executed checks.

1. Separate completed implementation from planned, partial, or unverified work.
2. Name the exact checks run and their outcomes. Never imply a check passed when it was skipped, unavailable, interrupted, or not run.
3. State failures, warnings, uncertainty, cleanup gaps, and behavior that was not exercised, including the practical consequence.
4. Distinguish a code change from proof that the original concern is fixed. Cite the relevant file/line or command result when available.
5. Respect the declared stop rule. Record deferred and final-round surviving concerns in `STATUS.md` rather than silently extending the task.

Keep the report concise but complete enough that another maintainer can tell what is safe to rely on and what remains.
