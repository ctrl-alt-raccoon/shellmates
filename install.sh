#!/bin/sh
set -eu

umask 077

REPO=${SCLAUDE_REPO:-ctrl-alt-raccoon/sclaude}
VERSION=${SCLAUDE_VERSION:-latest}
BIN_DIR=${SCLAUDE_BIN_DIR:-"$HOME/.local/bin"}
RUN_SETUP=1

usage() {
  cat <<'EOF'
Install sclaude from a verified GitHub release.

Usage: install.sh [--version TAG] [--bin-dir DIR] [--no-setup] [-- SETUP_ARGS...]

Environment:
  SCLAUDE_REPO       GitHub owner/repository (default: ctrl-alt-raccoon/sclaude)
  SCLAUDE_VERSION    Release tag or latest
  SCLAUDE_BIN_DIR    Absolute command directory (default: $HOME/.local/bin)
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || { echo "--version requires a value" >&2; exit 2; }
      VERSION=$2
      shift 2
      ;;
    --bin-dir)
      [ "$#" -ge 2 ] || { echo "--bin-dir requires a value" >&2; exit 2; }
      [ -n "$2" ] || { echo "--bin-dir requires a non-empty value" >&2; exit 2; }
      BIN_DIR=$2
      shift 2
      ;;
    --no-setup)
      RUN_SETUP=0
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    *)
      echo "unknown installer option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ "$(id -u)" -eq 0 ] && [ "${SCLAUDE_ALLOW_ROOT:-0}" != 1 ]; then
  echo "Refusing a root install. Run as the user who will use sclaude." >&2
  exit 1
fi

command -v curl >/dev/null 2>&1 || {
  echo "curl is required to download sclaude." >&2
  exit 1
}

printf '%s\n' "$REPO" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || {
  echo "Invalid GitHub repository: $REPO" >&2
  exit 2
}
case "$BIN_DIR" in
  /*) ;;
  *) echo "--bin-dir must be an absolute path" >&2; exit 2 ;;
esac
case "/$BIN_DIR/" in
  */../*|*/./*) echo "--bin-dir must be normalized" >&2; exit 2 ;;
esac

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in
  darwin|linux) ;;
  *) echo "Unsupported operating system: $os" >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
esac

if [ "$VERSION" = latest ]; then
  effective=$(curl --proto '=https' --proto-redir '=https' -fsSL \
    --retry 3 --connect-timeout 15 --max-time 60 \
    -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest")
  case "$effective" in
    "https://github.com/${REPO}/releases/tag/"*) VERSION=${effective##*/} ;;
    *) echo "Unable to resolve the latest release tag." >&2; exit 1 ;;
  esac
fi
printf '%s\n' "$VERSION" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || {
  echo "Invalid release version: $VERSION" >&2
  exit 2
}
prerelease=${VERSION#*-}
if [ "$prerelease" != "$VERSION" ]; then
  prerelease=${prerelease%%+*}
  old_ifs=$IFS
  IFS=.
  for identifier in $prerelease; do
    case "$identifier" in
      0|[1-9][0-9]*) ;;
      0[0-9]*) echo "Invalid release version: numeric prerelease identifiers cannot have leading zeroes." >&2; exit 2 ;;
    esac
  done
  IFS=$old_ifs
fi

asset="sclaude_${os}_${arch}"
base="https://github.com/${REPO}/releases/download/${VERSION}"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/sclaude-install.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

curl --proto '=https' --proto-redir '=https' -fL --retry 3 \
  --connect-timeout 15 --max-time 300 --max-filesize 134217728 \
  -o "$tmp/$asset" "$base/$asset"
curl --proto '=https' --proto-redir '=https' -fL --retry 3 \
  --connect-timeout 15 --max-time 60 --max-filesize 1048576 \
  -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"

expected=$(awk -v wanted="$asset" '
  BEGIN { count = 0; bad = 0 }
  {
    if (length($0) == 0) next
    hash = substr($0, 1, 64)
    sep = substr($0, 65, 2)
    name = substr($0, 67)
    if (length(hash) != 64 || hash !~ /^[0-9A-Fa-f]+$/ || (sep != "  " && sep != " *") || name == "") {
      bad = 1
      next
    }
    if (name == wanted) { count++; selected = tolower(hash) }
  }
  END {
    if (bad || count != 1) exit 1
    print selected
  }
' "$tmp/SHA256SUMS") || {
  echo "Invalid checksum manifest or no unique checksum for $asset." >&2
  exit 1
}

if command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$asset" | cut -d ' ' -f 1)
elif command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$asset" | cut -d ' ' -f 1)
else
  echo "shasum or sha256sum is required for verification." >&2
  exit 1
fi
[ "$actual" = "$expected" ] || { echo "Checksum verification failed." >&2; exit 1; }

chmod 700 "$tmp/$asset"
"$tmp/$asset" _install-release --source "$tmp/$asset" --version "$VERSION" --bin-dir "$BIN_DIR"

if [ "$RUN_SETUP" -eq 1 ]; then
  set -- "$@" --bin-dir "$BIN_DIR"
  "$BIN_DIR/sclaude" setup "$@"
fi
