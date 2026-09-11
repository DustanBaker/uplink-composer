#!/bin/sh
# The Composer — no-root installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/DustanBaker/the-composer/main/install.sh | sh
#
# Downloads the latest release binary for this machine into ~/.local/bin,
# verifies its SHA-256 against the release's SHA256SUMS.txt, and installs
# both `composer` and the `compose` alias. No sudo. Set GITHUB_TOKEN to
# install from a private repository.
set -eu

REPO="DustanBaker/the-composer"
BIN="${COMPOSER_BIN:-$HOME/.local/bin}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in linux|darwin) ;; *) echo "unsupported OS: $OS" >&2; exit 1 ;; esac
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

auth() { if [ -n "${GITHUB_TOKEN:-}" ]; then printf 'Authorization: Bearer %s' "$GITHUB_TOKEN"; else printf 'X-Composer: 1'; fi; }

API="https://api.github.com/repos/$REPO/releases/latest"
JSON=$(curl -fsSL -H "$(auth)" -H 'User-Agent: the-composer-installer' "$API")
TAG=$(printf '%s' "$JSON" | tr -d '\n' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
NAME="composer-$TAG-$OS-$ARCH"

# Asset API URLs work for public and private repos alike.
asset_url() {
  printf '%s' "$JSON" | tr '{' '\n' | grep "\"name\": *\"$1\"" | sed 's/.*"url": *"\([^"]*\)".*/\1/' | head -1
}
URL=$(asset_url "$NAME")
SUMS=$(asset_url "SHA256SUMS.txt")
[ -n "$URL" ] || { echo "release $TAG has no $OS-$ARCH build" >&2; exit 1; }

mkdir -p "$BIN"
TMP="$BIN/.composer.download"
echo "Downloading $NAME..."
curl -fsSL -H "$(auth)" -H 'Accept: application/octet-stream' -H 'User-Agent: the-composer-installer' -o "$TMP" "$URL"

if [ -n "$SUMS" ]; then
  WANT=$(curl -fsSL -H "$(auth)" -H 'Accept: application/octet-stream' "$SUMS" | grep " $NAME\$" | awk '{print $1}')
  if command -v sha256sum >/dev/null 2>&1; then GOT=$(sha256sum "$TMP" | awk '{print $1}'); else GOT=$(shasum -a 256 "$TMP" | awk '{print $1}'); fi
  if [ -n "$WANT" ] && [ "$WANT" != "$GOT" ]; then rm -f "$TMP"; echo "SHA-256 mismatch: got $GOT, release lists $WANT" >&2; exit 1; fi
  [ -n "$WANT" ] && echo "SHA-256 verified."
fi

mv -f "$TMP" "$BIN/composer"
chmod +x "$BIN/composer"
ln -sf composer "$BIN/compose"

echo ""
echo "Installed The Composer $TAG to $BIN (composer, compose)."
case ":$PATH:" in
  *":$BIN:"*) ;;
  *) echo "Add it to your PATH:  export PATH=\"$BIN:\$PATH\"   (put that in your shell profile)" ;;
esac
echo "Then, in a workspace:  compose <recipe>"
echo "No workspace yet?      composer init --org \"Your Org\" my-workspace"
