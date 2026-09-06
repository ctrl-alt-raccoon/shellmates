---
name: cross-review
description: Obtain one independent review from Claude or Codex when the user requests cross-agent review or agrees to a second-agent review of a plan, change, or diff. Not an automatic step, implementation workflow, or publishing workflow.
---

Choose the other vendor explicitly: Claude implementer -> Codex reviewer;
Codex implementer -> Claude reviewer. Clarify unknown host identity; never guess
what an opaque `claudex` wrapper runs.

Use `scripts/review.py` beside this skill, resolving its actual installed path.
It sends a bounded packet, not the live repository, to one reviewer process.

```sh
python3 -B /path/to/cross-review/scripts/review.py --reviewer codex --repo /repo --diff working
python3 -B /path/to/cross-review/scripts/review.py --reviewer claude --repo /repo --file plan.md --file requirements.md
```

These commands preview the file list and byte count without contacting a model.
Inspect the selected diff/files for secrets before adding `--execute`, which
authorizes sending this packet to the selected provider. Never send source to a
provider the user or repository has not authorized. No secrets, environment files,
transcripts, or unrelated untracked files.

`--diff working` includes tracked staged and unstaged changes against HEAD, not
untracked files. `--diff staged` selects the index. `--base main` selects committed
changes since the merge base, not uncommitted changes. Use `--path` to narrow diffs
and `--file` for new files, requirements, relevant callers/tests and narrower
instructions. Reviewers cannot discover missing context. Shared preferences and
project instructions are included; arbitrary Markdown imports are not followed.

Use native CLIs verified by the transport tests. Codex must expose the `view_image`
feature control (verified: 0.153.4); older versions that cannot disable image-file
reads are refused before packet delivery. A silently accepted setting is not proof
that it works. Never bypass the refusal or automatically upgrade a host.
Use `--model` or `--effort` only when specified or agreed; otherwise CLI defaults
apply. The default deadline is 180 seconds. Timeout/authentication failure means
review unavailable, not approval. Do not retry automatically or weaken permissions.

Present material findings with evidence and missing-context limitations. Verify
them against actual requirements; reviewers can be wrong. The implementer owns
already-authorized corrections and checks. Stop after this independent pass.
No recursive review, automatic implementation, commits, or publishing.
