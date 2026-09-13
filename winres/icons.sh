#!/bin/sh
# Rebuilds every icon file from the one master, docs/logo/dsky-icon.png
# (1024x1024, transparent). Needs ImageMagick 7 and Python 3; run from the
# repository root:
#
#   sh winres/icons.sh && (cd cmd/dsky-app && go generate)
#
# dsky.ico    Windows: installer, shortcuts, and (via go generate) the .exe files
# dsky.png    Linux app launcher
# dsky.icns   macOS: the DSKY.app install.sh builds
# favicon.png the portal page
set -eu
M=docs/logo/dsky-icon.png
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

magick "$M" -filter Lanczos -define icon:auto-resize=256,128,64,48,40,32,24,20,16 dsky.ico
magick "$M" -filter Lanczos -resize 512x512 -strip dsky.png
magick "$M" -filter Lanczos -resize 64x64 -strip internal/webui/favicon.png

# ICNS is a list of (type, length, PNG) chunks. ImageMagick cannot write it and
# iconutil only exists on a Mac, but the format is small enough to write here.
for s in 16 32 64 128 256 512 1024; do
  magick "$M" -filter Lanczos -resize ${s}x${s} -strip "PNG32:$T/$s.png"
done
python3 - "$T" dsky.icns <<'PY'
import struct, sys
src, out = sys.argv[1], sys.argv[2]
# type -> pixel size. The @2x types reuse the next size up.
types = [("icp4", 16), ("icp5", 32), ("icp6", 64), ("ic07", 128), ("ic08", 256),
         ("ic09", 512), ("ic10", 1024), ("ic11", 32), ("ic12", 64), ("ic13", 256), ("ic14", 512)]
body = b""
for t, s in types:
    data = open(f"{src}/{s}.png", "rb").read()
    body += t.encode() + struct.pack(">I", 8 + len(data)) + data
open(out, "wb").write(b"icns" + struct.pack(">I", 8 + len(body)) + body)
PY
echo "icons rebuilt from $M"
