#!/bin/sh
# Builds the first-boot agent for Windows and embeds it, gzipped, for DSKY to
# stage onto media. Run before building DSKY itself: a DSKY built without it
# has no agent and composes media with the older generated scripts instead.
set -eu
cd "$(dirname "$0")"
VERSION="${1:-dev}"
LDFLAGS="-s -w -X github.com/uplinkresearch/dsky/internal/buildinfo.Version=${VERSION}"
for arch in amd64 arm64; do
  out="$(mktemp)"
  CGO_ENABLED=0 GOOS=windows GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/dsky-agent
  # -n: no timestamp or name in the header, so identical input gives an
  # identical file and DSKY's reproducible builds stay reproducible.
  gzip -9 -n -c "$out" > "internal/agentbin/bin/dsky-agent-${arch}.exe.gz"
  rm -f "$out"
  printf 'agent %s: %s bytes\n' "$arch" "$(wc -c < "internal/agentbin/bin/dsky-agent-${arch}.exe.gz")"
done
