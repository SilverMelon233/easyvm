#!/bin/sh
# BUILD THE DEBIAN ROOT FILESYSTEM A MICROVM BOOTS, OUT OF THE PUBLISHED IMAGE.
#
# `debootstrap` IS NOT AVAILABLE HERE and that is not a workaround, it is a reason: MEASURED,
# `apt-get update` inside the 2 GiB lab pod is OOM-killed before the package could be installed.
# Debian publishes the same content as a container image, whose layers ARE a root filesystem - which
# is the identical "populate a directory, then mke2fs -d" shape `alpine-rootfs.sh` uses.
#
# NO INSTALLER RUNS. A microvm is handed a kernel, an initramfs and a disk; there is no firmware to
# read a boot sector, so the disk has to arrive holding a system.
#
# THE FOUR THINGS THAT ARE NOT THE SAME AS UNTARRING IT:
#
#   1. /etc/fstab MUST NOT NAME A PARTITION THAT DOES NOT EXIST. The image this replaces carried
#
#        PARTUUID=0f6986d9-... /boot/efi vfat defaults,umask=077 0 2
#
#      and a microvm has no ESP: qemu hands the kernel over directly. systemd could not satisfy the
#      mount, local-fs.target failed, and the guest dropped to
#
#        You are in emergency mode...
#        Cannot open access to console, the root account is locked.
#
#      MEASURED on /disks/lab/d13/console.log - 710 lines, and `login:` appears ZERO times.
#
#   2. THE INITRAMFS MUST ACTUALLY CONTAIN A WORKING MODULE TREE. The image this replaces had
#      `virtio_blk.ko.xz` and `virtio_mmio.ko.xz` present but NO `modules.dep` and NO `/init` at all
#      - a hand-assembled half-file. MEASURED, it boots to
#
#        ALERT! /dev/vda does not exist. Dropping to a shell!
#
#      even though qemu DID inject `virtio_mmio.device=512@0xfeb00e00:12` on the command line, which
#      is visible in the same log. A module that cannot be resolved is a module that never loads.
#
#   3. THE KERNEL AND ITS MODULES MUST BE THE SAME BUILD. The modules come from the `linux-image`
#      package installed INTO the tree, never from the host, or `insmod` answers "version magic".
#
#   4. ROOT MUST BE ABLE TO LOG IN. `root::` in /etc/shadow is an empty password, which login accepts
#      only if PAM is told to; the autologin drop-in below is what makes the console reach a prompt
#      without asking.
set -eu

V="${DEBIAN_VERSION:-trixie}"
OUT="${1:-.}"
WORK="${OUT}/debian-build"

log() { printf '  %s\n' "$*"; }
mkdir -p "$WORK" "$OUT"

# --- 1. fetch the image's layers -------------------------------------------------------------
# THE REGISTRY RATHER THAN A GITHUB MIRROR: MEASURED, the debuerreotype branch serves the rootfs
# only inside an OCI blob (`trixie/oci/blobs/...`), while the registry hands back the compressed
# layer directly and is the same artifact `docker pull debian:trixie` would give.
if [ ! -f "$WORK/rootfs.tar" ]; then
	log "resolving debian:${V} on docker.io"
	token=$(curl -fsSL --max-time 60 \
		"https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/debian:pull" \
		| python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')

	digest=$(curl -fsSL --max-time 60 \
		-H "Authorization: Bearer $token" \
		-H 'Accept: application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json' \
		"https://registry-1.docker.io/v2/library/debian/manifests/${V}" \
		| python3 -c '
import json,sys
for m in json.load(sys.stdin).get("manifests", []):
    p = m.get("platform", {})
    if p.get("architecture") == "amd64" and p.get("os") == "linux":
        print(m["digest"]); break
')
	[ -n "$digest" ] || { log "no amd64 manifest for $V"; exit 1; }
	log "amd64 manifest $digest"

	# THE LAYER LIST, IN ORDER. Debian is a single layer, but the loop is written for the general
	# case: applying only the first layer of a multi-layer image produces a tree with no / etc.
	layers=$(curl -fsSL --max-time 60 \
		-H "Authorization: Bearer $token" \
		-H 'Accept: application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json' \
		"https://registry-1.docker.io/v2/library/debian/manifests/${digest}" \
		| python3 -c '
import json,sys
for l in json.load(sys.stdin)["layers"]:
    print(l["digest"])
')
	rm -f "$WORK"/layer-*
	i=0
	for d in $layers; do
		i=$((i+1))
		log "layer $i: $d"
		curl -fsSL --max-time 900 -H "Authorization: Bearer $token" \
			"https://registry-1.docker.io/v2/library/debian/blobs/${d}" -o "$WORK/layer-$i"
	done
	: > "$WORK/rootfs.tar"
	for f in "$WORK"/layer-*; do cat "$f" >> "$WORK/rootfs.tar"; done
fi
log "rootfs tar: $(( $(stat -c %s "$WORK/rootfs.tar") >> 20 )) MiB"

ROOT="$WORK/root"
if [ ! -d "$ROOT/etc" ]; then
	log "unpacking"
	rm -rf "$ROOT"; mkdir -p "$ROOT"
	python3 - "$WORK/rootfs.tar" "$ROOT" <<'PY'
import sys, tarfile
tar, dst = sys.argv[1], sys.argv[2]
# A REGISTRY LAYER IS A TAR, sometimes gzipped. `r:*` sniffs it; the magic bytes are why this is not
# an `xz` call - and `xz` is not in the image at all, measured.
with tarfile.open(tar, "r:*") as t:
    t.extractall(dst, filter="tar")
PY
fi
log "root tree: $(du -sm "$ROOT" | cut -f1) MiB"

# --- 2. an fstab a microvm can satisfy ------------------------------------------------------
log "writing an fstab with only the filesystem that exists"
cat > "$ROOT/etc/fstab" <<'EOF'
# A microvm has ONE filesystem. The kernel argument `root=/dev/vda` mounts it; saying so here is what
# keeps `systemctl` and the kernel in agreement.
/dev/vda  /  ext4  rw,errors=remount-ro  0  1
EOF

# --- 3. a console that reaches a prompt -----------------------------------------------------
log "unlocking root and enabling autologin on tty1 and ttyS0"
sed -i 's/^root:[^:]*:/root::/' "$ROOT/etc/shadow"
for unit in serial-getty@ttyS0 getty@tty1; do
	mkdir -p "$ROOT/etc/systemd/system/${unit}.service.d"
	cat > "$ROOT/etc/systemd/system/${unit}.service.d/autologin.conf" <<'EOF'
[Service]
# THE AUTOLOGIN IS THE ASSERTION, not a convenience. `login: root (automatic login)` can only appear
# after systemd reached multi-user and the store's own binaries ran, which is what a step checks.
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear %I $TERM
EOF
done

# --- 4. a kernel, its modules, and an initramfs that loads virtio ----------------------------
log "installing the kernel and initramfs-tools (about 90 MiB)"
# THE IMAGE ALREADY SHIPS A `debian.sources` FILE, and MEASURED, writing `sources.list` beside it
# makes apt complain that every target is configured twice - and then installs nothing, because the
# first error it hits is about the duplicated one. The published file is correct, so it is used and
# `sources.list` is left empty.
: > "$ROOT/etc/apt/sources.list"
if [ ! -f "$ROOT/etc/apt/sources.list.d/debian.sources" ]; then
	printf 'deb http://deb.debian.org/debian %s main\n' "$V" > "$ROOT/etc/apt/sources.list"
	printf 'deb http://deb.debian.org/debian %s-updates main\n' "$V" >> "$ROOT/etc/apt/sources.list"
	printf 'deb http://deb.debian.org/debian-security %s-security main\n' "$V" >> "$ROOT/etc/apt/sources.list"
fi
cp /etc/resolv.conf "$ROOT/etc/resolv.conf" 2>/dev/null || true

# `/tmp` MUST BE WORLD-WRITABLE OR apt CANNOT UNPACK ANYTHING. MEASURED: the layer ships `/tmp` as
# 0755 root:root, and `apt-get update` then fails with
#
#	Unable to mkstemp /tmp/apt.sig.XXXXXX - GetTempFile (13: Permission denied)
#
# because the signature check drops to the unprivileged `_apt` user. A real Debian install has 1777.
chmod 1777 "$ROOT/tmp"

mount --bind /dev  "$ROOT/dev"  2>/dev/null || true
mount --bind /proc "$ROOT/proc" 2>/dev/null || true
mount --bind /sys  "$ROOT/sys"  2>/dev/null || true

env ${https_proxy:+https_proxy="$https_proxy"} ${http_proxy:+http_proxy="$http_proxy"} \
	DEBIAN_FRONTEND=noninteractive \
	chroot "$ROOT" /bin/sh -c '
		set -e
		apt-get update -qq
		# THE VIRTIO MODULES ARE THE POINT. Without virtio_blk the guest boots to
		# "ALERT! /dev/vda does not exist" because nothing ever claims the MMIO device.
		apt-get install -y -qq --no-install-recommends \
			linux-image-amd64 initramfs-tools udev systemd-sysv \
			iproute2 iputils-ping ca-certificates tzdata openssh-server
		# `initramfs-tools` DOES NOT PULL virtio_mmio on its own: a normal machine boots from SATA.
		# All three are named so the dependency chain is explicit and `update-initramfs` records it.
		for m in virtio virtio_ring virtio_mmio virtio_pci virtio_blk ext4; do
			grep -qx "$m" /etc/initramfs-tools/modules 2>/dev/null || echo "$m" >> /etc/initramfs-tools/modules
		done
		update-initramfs -u -k all
		# THE PROOF THIS STEP WORKED, checked inside the tree rather than hoped for afterwards.
		command -v lsinitramfs >/dev/null && lsinitramfs /boot/initrd.img-* | grep -q virtio_blk \
			|| echo "WARNING: virtio_blk is not in the generated initramfs"
	'

umount "$ROOT/sys" 2>/dev/null || true
umount "$ROOT/proc" 2>/dev/null || true
umount "$ROOT/dev" 2>/dev/null || true

# --- 5. write the disk ----------------------------------------------------------------------
SIZE=$(( $(du -sm "$ROOT" | cut -f1) + 250 ))
log "writing a ${SIZE} MiB ext4 image"
rm -f "$WORK/rootfs.raw"
truncate -s "${SIZE}M" "$WORK/rootfs.raw"
mke2fs -q -F -t ext4 -d "$ROOT" "$WORK/rootfs.raw"
qemu-img convert -f raw -O qcow2 -c "$WORK/rootfs.raw" "$OUT/debian.qcow2"

# --- 6. the two files the boot step names ---------------------------------------------------
KVER=$(ls "$ROOT/lib/modules" | head -1)
log "kernel $KVER"
cp "$ROOT/boot/vmlinuz-$KVER" "$OUT/debian-vmlinuz"
cp "$ROOT/boot/initrd.img-$KVER" "$OUT/debian-initramfs"

log "done:"
ls -l "$OUT/debian-vmlinuz" "$OUT/debian-initramfs" "$OUT/debian.qcow2"
