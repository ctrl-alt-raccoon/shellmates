---
name: review-unbiased
summary: Review a supplied code scope with fresh eyes and no steering.
---

# Unbiased review

Review only the scope supplied by the caller: file paths, commit, branch, or subsystem. Do not ask for expected findings, hints, or the author's concerns before reviewing.

1. Read the scoped implementation and enough adjacent code/tests to establish its contracts.
2. Look for concrete correctness, security, data-loss, compatibility, concurrency, and test-coverage defects.
3. Verify every proposed finding against the current tree. Prefer a reproducible input/state and wrong outcome over speculative risk.
4. Return findings ordered by severity with a precise file/line anchor, failure scenario, and why existing guards/tests do not prevent it.
5. If no verified finding survives, say so. Keep optional style or low-value refactors separate from defects.

Do not edit files. Do not reveal prior review expectations to another reviewer. A review/fix loop ends after the declared cap, and zero or low-only findings end it early; unresolved items go to `STATUS.md` rather than another round.
