#!/bin/sh
# DSKY — no-root installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/uplinkresearch/dsky/main/install.sh | sh
#
# Downloads the latest release binary for this machine into ~/.local/bin (or
# $DSKY_BIN, the same variable `dsky uninstall` reads), verifies it against
# the release's SHA256SUMS.txt, and installs both `dsky` and the `compose`
# alias. No sudo. Set GITHUB_TOKEN to install from a private fork.
set -eu

REPO="uplinkresearch/dsky"
BIN="${DSKY_BIN:-$HOME/.local/bin}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in linux|darwin) ;; *) echo "unsupported OS: $OS" >&2; exit 1 ;; esac
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

auth() { if [ -n "${GITHUB_TOKEN:-}" ]; then printf 'Authorization: Bearer %s' "$GITHUB_TOKEN"; else printf 'X-DSKY: 1'; fi; }
get() { curl -fsSL -H "$(auth)" -H 'Accept: application/octet-stream' -H 'User-Agent: dsky-installer' "$@"; }

API="https://api.github.com/repos/$REPO/releases/latest"
JSON=$(curl -fsSL -H "$(auth)" -H 'User-Agent: dsky-installer' "$API")
TAG=$(printf '%s' "$JSON" | tr -d '\n' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
NAME="dsky-$TAG-$OS-$ARCH"

# Asset API URLs work for public and private repos alike.
asset_url() {
  printf '%s' "$JSON" | tr '{' '\n' | grep "\"name\": *\"$1\"" | sed 's/.*"url": *"\([^"]*\)".*/\1/' | head -1
}
URL=$(asset_url "$NAME")
SUMS_URL=$(asset_url "SHA256SUMS.txt")
[ -n "$URL" ] || { echo "release $TAG has no $OS-$ARCH build" >&2; exit 1; }
[ -n "$SUMS_URL" ] || { echo "release $TAG publishes no SHA256SUMS.txt; refusing to install unverified" >&2; exit 1; }
SUMS=$(get "$SUMS_URL")

sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }

# fetch_verified downloads to a temp name and only moves it into place once
# the hash matches, so a bad download never replaces a working install. A
# file the checksum list does not name is refused, as self-update refuses it.
fetch_verified() { # asset-name url dest
  tmp="$3.download"
  get -o "$tmp" "$2"
  want=$(printf '%s\n' "$SUMS" | awk -v n="$1" '$2 == n {print $1; exit}')
  [ -n "$want" ] || { rm -f "$tmp"; echo "SHA256SUMS.txt does not list $1; refusing to install it unverified" >&2; exit 1; }
  got=$(sha256 "$tmp")
  [ "$want" = "$got" ] || { rm -f "$tmp"; echo "SHA-256 mismatch for $1: got $got, release lists $want" >&2; exit 1; }
  mv -f "$tmp" "$3"
}

mkdir -p "$BIN"
echo "Downloading $NAME..."
fetch_verified "$NAME" "$URL" "$BIN/dsky"
chmod +x "$BIN/dsky"
ln -sf dsky "$BIN/compose"
echo "SHA-256 verified."

# Linux: an app-drawer launcher (macOS has no equivalent drop-in; use the CLI).
# The names match what `dsky uninstall` removes.
if [ "$OS" = "linux" ]; then
  APPS="$HOME/.local/share/applications"
  ICONS="$HOME/.local/share/icons"
  mkdir -p "$APPS" "$ICONS"
  ICON_URL=$(asset_url "dsky.png")
  ICON=""
  if [ -n "$ICON_URL" ] && (fetch_verified dsky.png "$ICON_URL" "$ICONS/dsky.png") 2>/dev/null; then
    ICON="$ICONS/dsky.png"
  fi
  cat > "$APPS/dsky.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=DSKY
Comment=Build and flash bootable OS installers
Exec=$BIN/dsky serve --open
Icon=${ICON:-drive-removable-media}
Terminal=false
Categories=System;Utility;
EOF
  update-desktop-database "$APPS" >/dev/null 2>&1 || true
  echo "Added an app-drawer launcher (DSKY)."
fi

echo ""
echo "Installed DSKY $TAG to $BIN (dsky, compose)."
case ":$PATH:" in
  *":$BIN:"*)
    # An older build (uplink, bootwright) also installed a `compose`; if its
    # directory comes first, the alias keeps running the old program.
    found=$(command -v compose 2>/dev/null || true)
    if [ -n "$found" ] && [ "$found" != "$BIN/compose" ]; then
      echo "Warning: \`compose\` resolves to $found, not this install. Remove that older copy or put $BIN ahead of it on PATH." >&2
    fi
    ;;
  *) echo "Add it to your PATH:  export PATH=\"$BIN:\$PATH\"   (put that in your shell profile)" ;;
esac
echo "Then, in a workspace:  compose <recipe>"
echo "No workspace yet?      dsky init --org \"Your Org\" my-workspace"
