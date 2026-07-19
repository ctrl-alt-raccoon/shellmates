#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
version=${1:-snapshot}

if [ "$#" -gt 1 ]; then
  echo "usage: $0 [snapshot|vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]]" >&2
  exit 2
fi

if [ "$version" != snapshot ]; then
  if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'; then
    echo "invalid release version: $version" >&2
    exit 2
  fi
  prerelease=${version#*-}
  if [ "$prerelease" != "$version" ]; then
    prerelease=${prerelease%%+*}
    old_ifs=$IFS
    IFS=.
    for identifier in $prerelease; do
      case "$identifier" in
        0|[1-9][0-9]*) ;;
        0[0-9]*) echo "invalid release version: numeric prerelease identifiers cannot have leading zeroes" >&2; exit 2 ;;
      esac
    done
    IFS=$old_ifs
  fi
fi

out="$root/dist/$version"
case "$out" in
  "$root"/dist/*) ;;
  *) echo "refusing release output outside dist" >&2; exit 1 ;;
esac

rm -rf -- "$out"
mkdir -p -- "$out"

for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  os=${target%/*}
  arch=${target#*/}
  name="sclaude_${os}_${arch}"
  echo "building $name"
  (
    cd "$root"
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build \
      -trimpath \
      -ldflags "-s -w -X main.version=$version" \
      -o "$out/$name" ./cmd/sclaude
  )
done

cp -- "$root/install.sh" "$out/install.sh"
(
  cd "$out"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 sclaude_* install.sh > SHA256SUMS
  else
    sha256sum sclaude_* install.sh > SHA256SUMS
  fi
)

printf 'release assets: %s\n' "$out"
