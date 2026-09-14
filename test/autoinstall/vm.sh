#!/usr/bin/env bash
# Boot a real Ubuntu ISO, built into install media by DSKY's linux.autoinstall,
# in a virtual machine: install onto a blank disk with nobody at the keyboard,
# then look inside the installed disk and boot it once.
#
# DSKY's autoinstall path was only ever checked against a synthetic ISO. This
# is the proof on the ISOs people actually download. It never touches a real
# disk: the "stick" is the built image file and the target is a qcow2 file.
#
#   test/autoinstall/vm.sh <case> [workdir]
#
# Cases:
#   server-24.04          Ubuntu Server 24.04, zero-touch (GRUB patched)
#   server-26.04          Ubuntu Server 26.04, zero-touch
#   desktop-26.04         Ubuntu Desktop 26.04, zero-touch, account in the
#                         answers: does the mechanism work on the desktop ISO?
#   desktop-26.04-prompt  Ubuntu Desktop 26.04 as Quick Install would make it:
#                         GRUB left alone (Ubuntu's own prompt kept) and no
#                         account in the answers. Observed, not pass/fail:
#                         screenshots show what a person would see.
#
# Needs: go, qemu-system-x86_64 with KVM, OVMF, qemu-img, qemu-nbd, socat,
# ImageMagick (convert), sudo for nbd mounts. Screenshots land in
# <workdir>/shots as PNG; results in <workdir>/result.md.
set -euo pipefail

CASE=${1:?case}
W=$(realpath -m "${2:-./autoinstall-work}")
REPO=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$W/shots"
RESULT="$W/result.md"
: >"$RESULT"
say() { echo "$*" | tee -a "$RESULT"; }

# releases.ubuntu.com sent GitHub's runners 0.2-1.4 MiB/s, and every case
# but one ran out of time still downloading. The kernel.org mirror carries the
# same files; the pinned sha256 below is what makes any mirror safe to use.
MIRROR=${UBUNTU_MIRROR:-https://mirrors.edge.kernel.org/ubuntu-releases}

case "$CASE" in
server-24.04)
  URL=$MIRROR/24.04/ubuntu-24.04.5-live-server-amd64.iso
  SHA=97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8
  KIND=server PATCH=true IDENTITY=true OBSERVE=false ;;
server-26.04)
  URL=$MIRROR/26.04/ubuntu-26.04.1-live-server-amd64.iso
  SHA=cc8a95cde20f6ced61a322420de00f10cc3c90ced545daa46cb9c1a117f1d927
  KIND=server PATCH=true IDENTITY=true OBSERVE=false ;;
desktop-26.04)
  URL=$MIRROR/26.04/ubuntu-26.04.1-desktop-amd64.iso
  SHA=601e30fbf5d97759367c632e2c33630665039b7e2158fd068403da3ccf1bda1f
  KIND=desktop PATCH=true IDENTITY=true OBSERVE=false ;;
desktop-26.04-prompt)
  URL=$MIRROR/26.04/ubuntu-26.04.1-desktop-amd64.iso
  SHA=601e30fbf5d97759367c632e2c33630665039b7e2158fd068403da3ccf1bda1f
  KIND=desktop PATCH=false IDENTITY=false OBSERVE=true ;;
*) echo "unknown case $CASE" >&2; exit 2 ;;
esac

say "## $CASE"
say ""
say "- ISO: \`$(basename "$URL")\`"
say "- GRUB patched for zero-touch: $PATCH · account in answers: $IDENTITY"

# ── Build the media with DSKY, as a user would ─────────────────────────────
export DSKY_LIBRARY="$W/lib"
(cd "$REPO" && CGO_ENABLED=0 go build -o "$W/dsky" ./cmd/dsky)
rm -rf "$W/ws"
"$W/dsky" init --org "DSKY CI" "$W/ws" >/dev/null

cat >"$W/ws/manifests/ci-ubuntu-iso.yaml" <<EOF
id: ci-ubuntu-iso
kind: os-image
format: iso
url: $URL
sha256: $SHA
EOF

{
  echo '#cloud-config'
  echo 'autoinstall:'
  echo '  version: 1'
  if [ "$IDENTITY" = true ]; then
    # Test-only account: user dsky, password "dsky".
    echo '  identity:'
    echo '    hostname: dsky-ci'
    echo '    username: dsky'
    echo "    password: \"$(openssl passwd -6 -salt dskyci dsky)\""
  fi
  echo '  storage:'
  echo '    layout:'
  echo '      name: direct'
  echo '  packages:'
  echo '    - hello'
  echo '  snaps:'
  echo '    - name: hello-world'
  echo '  late-commands:'
  echo '    - echo "provisioned by DSKY ({{.Org.Name}})" > /target/etc/dsky-provisioned'
  echo '  shutdown: reboot'
} >"$W/ws/templates/ci-autoinstall.yaml.tmpl"

cat >"$W/ws/recipes/ci-ubuntu.yaml" <<EOF
version: 1
id: ci-ubuntu
name: "CI: $CASE"
os:
  type: linux-iso
  source: ci-ubuntu-iso
target:
  min_stick: 8GiB
  boot: uefi-only
linux:
  autoinstall:
    user_data: templates/ci-autoinstall.yaml.tmpl
    kernel_patch: $PATCH
flash:
  verify: readback-sha256
EOF

# DRYRUN=1 stops here, after checking the workspace, without the multi-GB pull.
if [ "${DRYRUN:-}" = 1 ]; then
  "$W/dsky" -w "$W/ws" recipes list
  "$W/dsky" -w "$W/ws" sources list
  cat "$W/ws/templates/ci-autoinstall.yaml.tmpl"
  exit 0
fi
"$W/dsky" -w "$W/ws" sources pull ci-ubuntu-iso
"$W/dsky" -w "$W/ws" build ci-ubuntu | tee "$W/build.log"
IMG=$(awk '/^artifact:/ {print $2}' "$W/build.log")
[ -f "$IMG" ] || { say "- **FAIL** build produced no image"; exit 1; }
say "- DSKY built \`$(basename "$IMG")\` ($(( $(stat -c %s "$IMG") >> 20 )) MiB)"
# The pulled ISO is no longer needed; runners are short of disk.
find "$W/lib" -type f -size +1G ! -samefile "$IMG" -delete || true

# ── VM plumbing ─────────────────────────────────────────────────────────────
OVMF_CODE=${OVMF_CODE:-/usr/share/OVMF/OVMF_CODE_4M.fd}
OVMF_VARS=${OVMF_VARS:-/usr/share/OVMF/OVMF_VARS_4M.fd}
cp "$OVMF_VARS" "$W/vars.fd"
rm -f "$W/target.qcow2"
qemu-img create -q -f qcow2 "$W/target.qcow2" 40G

QPID=""
run_vm() { # phase, seconds-limit, extra qemu args...
  local phase=$1 limit=$2; shift 2
  local mon="$W/mon-$phase.sock"
  rm -f "$mon"
  qemu-system-x86_64 -enable-kvm -machine q35 -cpu host -smp 4 -m 8G \
    -drive if=pflash,format=raw,readonly=on,file="$OVMF_CODE" \
    -drive if=pflash,format=raw,file="$W/vars.fd" \
    -drive file="$W/target.qcow2",if=virtio,format=qcow2 \
    -netdev user,id=net0 -device virtio-net-pci,netdev=net0 \
    -device qemu-xhci -vga std -display none \
    -monitor unix:"$mon",server,nowait "$@" &
  QPID=$!
  local start=$SECONDS n=0
  while kill -0 "$QPID" 2>/dev/null; do
    sleep 30
    n=$((n + 1))
    echo "screendump $W/shots/$phase-$(printf %03d $n).ppm" | socat - UNIX-CONNECT:"$mon" >/dev/null 2>&1 || true
    if [ $((SECONDS - start)) -ge "$limit" ]; then
      echo "quit" | socat - UNIX-CONNECT:"$mon" >/dev/null 2>&1 || kill "$QPID" || true
      wait "$QPID" 2>/dev/null || true
      return 124
    fi
  done
  wait "$QPID" 2>/dev/null || true
  return 0
}

mount_target() {
  sudo modprobe nbd max_part=16
  sudo qemu-nbd --disconnect /dev/nbd0 >/dev/null 2>&1 || true
  sudo qemu-nbd --read-only --connect=/dev/nbd0 "$W/target.qcow2"
  sleep 3
  sudo partprobe /dev/nbd0 || true
  sleep 1
  lsblk -f /dev/nbd0 | tee -a "$W/lsblk.txt"
  ROOTDEV=$(lsblk -lnpo NAME,FSTYPE,SIZE -b /dev/nbd0 | awk '$2=="ext4" {print $3, $1}' | sort -n | tail -1 | awk '{print $2}')
  [ -n "$ROOTDEV" ] || return 1
  sudo mkdir -p "$W/mnt"
  sudo mount -o ro,noload "$ROOTDEV" "$W/mnt"
}
umount_target() {
  sudo umount "$W/mnt" 2>/dev/null || true
  sudo qemu-nbd --disconnect /dev/nbd0 >/dev/null 2>&1 || true
}

shots_to_png() {
  for f in "$W"/shots/*.ppm; do
    [ -e "$f" ] || continue
    convert "$f" "${f%.ppm}.png" && rm -f "$f"
  done
}
trap 'shots_to_png; umount_target' EXIT

# ── Install from the stick ─────────────────────────────────────────────────
# The image is attached as a USB stick and boots first. -no-reboot turns the
# installer's final reboot into QEMU exiting, which is how the end is seen.
LIMIT=$((60 * 60))
[ "$OBSERVE" = true ] && LIMIT=$((20 * 60))
set +e
t0=$SECONDS
run_vm install "$LIMIT" -no-reboot \
  -drive file="$IMG",format=raw,if=none,id=stick,readonly=on \
  -device usb-storage,drive=stick,bootindex=0
rc=$?
set -e
mins=$(( (SECONDS - t0) / 60 ))

if [ "$OBSERVE" = true ]; then
  say "- Observed for $mins min (exit $rc); see the install-* screenshots for what a person sees."
  exit 0
fi
if [ $rc -eq 124 ]; then
  say "- **FAIL** install still running after $mins min (last screenshots show where it stopped)"
  exit 1
fi
if [ $mins -lt 3 ]; then
  say "- **FAIL** the VM stopped after $mins min — too soon to have installed anything"
  exit 1
fi
say "- Installer finished and rebooted after $mins min"

# ── What landed on the disk ─────────────────────────────────────────────────
fail=0
check() { # description, command...
  local what=$1; shift
  if "$@" >/dev/null 2>&1; then say "- ok: $what"; else say "- **FAIL**: $what"; fail=1; fi
}
if ! mount_target; then
  say "- **FAIL** no ext4 root filesystem on the target disk"
  exit 1
fi
check "late-command ran (/etc/dsky-provisioned)" sudo test -s "$W/mnt/etc/dsky-provisioned"
check "answers were the ones DSKY wrote (autoinstall-user-data mentions hello-world)" \
  sudo grep -rq hello-world "$W/mnt/var/log/installer/"
check "apt package from packages: installed (hello)" \
  sudo grep -Pzq 'Package: hello\nStatus: install ok installed' "$W/mnt/var/lib/dpkg/status"
# Snaps listed in the answers are seeded during install and installed by
# snapd on first boot, so they are checked after it (below).
if [ "$IDENTITY" = true ]; then
  check "account from identity: exists (dsky)" sudo grep -q '^dsky:' "$W/mnt/etc/passwd"
fi
umount_target

# ── First boot of the installed system ─────────────────────────────────────
set +e
run_vm firstboot $((6 * 60))
set -e
if mount_target; then
  target=multi-user
  [ "$KIND" = desktop ] && target=graphical
  check "snap from snaps: installed on first boot (hello-world)" \
    sudo sh -c "ls $W/mnt/var/lib/snapd/snaps/hello-world_*.snap"
  check "installed system booted to $target.target" \
    sudo sh -c "journalctl -D '$W/mnt/var/log/journal' --no-pager 2>/dev/null | grep -qi 'Reached target.*$(echo ${target:0:1} | tr a-z A-Z)${target:1}'"
  umount_target
else
  say "- **FAIL** target disk unreadable after first boot"
  fail=1
fi
exit $fail
