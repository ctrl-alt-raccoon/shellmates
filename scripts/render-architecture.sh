#!/bin/sh
# Optional documentation renderer. Does not install a skill or fetch code.
set -eu
if [ "$#" -ne 2 ]; then
  printf '%s\n' 'Usage: sh scripts/render-architecture.sh /trusted/archify-checkout /new/output.html' >&2
  exit 64
fi
archify_root=$(CDPATH= cd -P "$1" && pwd -P)
shellmates_root=$(CDPATH= cd -P "$(dirname "$0")/.." && pwd -P)
archify_revision=c6519401f7b91b9d43011657880893b0a8955548
if [ "$(git -C "$archify_root" rev-parse HEAD)" != "$archify_revision" ] ||
   [ -n "$(git -C "$archify_root" status --porcelain --untracked-files=all)" ]; then
  printf '%s\n' "Use a clean, inspected Archify checkout at $archify_revision." >&2
  exit 1
fi
if [ -e "$2" ] || [ -L "$2" ]; then
  printf '%s\n' 'Choose a new HTML output path; this helper does not overwrite artifacts.' >&2
  exit 1
fi
command -v node >/dev/null
export ARCHIFY_UPDATE_CHECK_DISABLED=1
node "$archify_root/archify/bin/archify.mjs" validate architecture \
  "$shellmates_root/docs/architecture/shellmates.architecture.json" --quality showcase --json
node "$archify_root/archify/bin/archify.mjs" deliver architecture \
  "$shellmates_root/docs/architecture/shellmates.architecture.json" "$2" --quality showcase --json
