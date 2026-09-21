#!/bin/sh
#
# Install docket: one static binary, no runtime.
#
#   curl -fsSL https://raw.githubusercontent.com/didenkolab/docket/main/install.sh | sh
#
# It downloads the release for this platform, checks it against the
# checksums the release publishes, and puts the binary on your PATH.
#
# Environment:
#   DOCKET_VERSION  a tag such as v0.6.0 (default: the latest release)
#   DOCKET_BIN_DIR  where to put it (default: ~/.local/bin, or /usr/local/bin
#                   when that is writable and ~/.local/bin is not on PATH)

set -eu

REPO=didenkolab/docket

say()  { printf '%s\n' "$*"; }
die()  { printf 'install: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required and was not found"; }

need uname
need mktemp
command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 ||
  die "curl or wget is required and neither was found"

fetch() {
  # fetch URL DESTINATION
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  else
    wget -qO "$2" "$1"
  fi
}

# --- platform ---------------------------------------------------------------

os=$(uname -s)
case "$os" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *) die "no release is built for $os. Build from source: https://github.com/$REPO#install" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "no release is built for $arch. Build from source: https://github.com/$REPO#install" ;;
esac

# --- version ----------------------------------------------------------------

version=${DOCKET_VERSION:-}
if [ -z "$version" ]; then
  tmp_v=$(mktemp) || die "cannot create a temporary file"
  fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp_v" ||
    die "cannot reach GitHub to ask for the latest release"
  version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp_v" | head -n 1)
  rm -f "$tmp_v"
  [ -n "$version" ] || die "could not read the latest release tag from GitHub"
fi

name="docket_${version}_${os}_${arch}"
base="https://github.com/$REPO/releases/download/$version"

say "docket $version for $os/$arch"

# --- download and verify ----------------------------------------------------

work=$(mktemp -d) || die "cannot create a temporary directory"
trap 'rm -rf "$work"' EXIT INT TERM

say "  downloading"
fetch "$base/${name}.tar.gz" "$work/${name}.tar.gz" ||
  die "download failed: $base/${name}.tar.gz"
fetch "$base/checksums.txt" "$work/checksums.txt" ||
  die "download failed: $base/checksums.txt"

# A release that cannot be checked is a release you should not install. If no
# checksum tool is present we stop rather than shrug: the whole point of this
# script is that you did not read it before piping it to a shell.
if command -v sha256sum >/dev/null 2>&1; then
  sum=$(sha256sum "$work/${name}.tar.gz" | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
  sum=$(shasum -a 256 "$work/${name}.tar.gz" | cut -d' ' -f1)
else
  die "neither sha256sum nor shasum is present, so the download cannot be verified"
fi

want=$(grep " ${name}.tar.gz\$" "$work/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || die "the release publishes no checksum for ${name}.tar.gz"
[ "$sum" = "$want" ] || die "checksum mismatch for ${name}.tar.gz
  expected $want
  got      $sum
Do not use this file. Report it at https://github.com/$REPO/issues"

say "  checksum ok"

tar -xzf "$work/${name}.tar.gz" -C "$work" || die "cannot unpack the archive"
binary="$work/$name/docket"
[ -f "$binary" ] || die "the archive does not contain a docket binary"

# macOS marks anything a browser downloaded, and refuses to run it unsigned.
# This binary is ad-hoc signed rather than notarized, so clear the flag that
# curl did not set but a browser would have.
if [ "$os" = darwin ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "$binary" 2>/dev/null || true
fi

# --- install ----------------------------------------------------------------

dir=${DOCKET_BIN_DIR:-}
if [ -z "$dir" ]; then
  case ":$PATH:" in
    *":$HOME/.local/bin:"*) dir="$HOME/.local/bin" ;;
    *) if [ -w /usr/local/bin ] 2>/dev/null; then dir=/usr/local/bin
       else dir="$HOME/.local/bin"; fi ;;
  esac
fi

mkdir -p "$dir" || die "cannot create $dir"
[ -w "$dir" ] || die "$dir is not writable. Set DOCKET_BIN_DIR to somewhere you own."

chmod +x "$binary"
mv "$binary" "$dir/docket" || die "cannot write $dir/docket"

say "  installed $dir/docket"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) say ""
     say "$dir is not on your PATH. Add it:"
     say ""
     say "  export PATH=\"$dir:\$PATH\""
     say "" ;;
esac

"$dir/docket" --version >/dev/null 2>&1 || die "the installed binary does not run"

say ""
say "Next:  docket init --key ENG --name Engineering ."
say "Docs:  https://github.com/$REPO#readme"
