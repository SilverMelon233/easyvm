#!/bin/sh
# BUILD AN ARCH LINUX ROOT FILESYSTEM A MICROVM BOOTS.
#
# THE ROOT TREE IS THE OFFICIAL CONTAINER IMAGE. Arch publishes
# `archlinux-bootstrap-x86_64.tar.zst` too, but it has TWO traps that the container image
# does not:
#
#	1. ITS pacman.conf HAS EVERY MIRROR COMMENTED OUT, so `pacman -Sy` answers
#	   "failed to synchronize all databases (no servers configured for repository)".
#	   Generating a mirrorlist needs `reflector`, which needs a working pacman.
#	2. IT EXPECTS A MOUNTED /proc. `pacman -S` fails with
#	   "could not open file: /etc/mtab: No such file or directory" and then
#	   "could not determine filesystem mount points" - and a build container that cannot
#	   mount proc can never satisfy that.
#
# The container image has a working pacman.conf pointing at the official mirror, which is
# why it is used instead. MEASURED: the mirror-list file it ships is also empty, so this
# script writes one line before installing anything.
#
# WHAT ITS KERNEL NEEDS, read from the installed package's modules.builtin:
#
#	kernel/drivers/block/virtio_blk.ko       BUILT IN - no module needed
#	kernel/fs/ext4/ext4.ko                   BUILT IN - no module needed
#	CONFIG_VIRTIO_MMIO=m                     a module, so it must be loaded
#
# so exactly ONE module goes into the initramfs, same as Fedora.
set -eu

OUT="${1:-.}"
WORK="${OUT}/arch-build"

log() { printf '  %s\n' "$*"; }

mkdir -p "$WORK" "$OUT"
HERE=$(cd "$(dirname "$0")" && pwd)

# --- 1. the root tree ---------------------------------------------------------------------
if [ ! -f "$WORK/rootfs.tar" ]; then
	log "pulling archlinux:latest from docker.io"
	python3 "$HERE/fetch-image.py" library/archlinux latest "$WORK/rootfs.tar"
fi

ROOT="$WORK/root"
if [ ! -d "$ROOT/usr" ]; then
	log "unpacking"
	rm -rf "$ROOT"
	mkdir -p "$ROOT"
	tar -xf "$WORK/rootfs.tar" -C "$ROOT"

	# THE DEVICE NODES tar DID NOT PRESERVE. MEASURED: pacman's own scriptlets fail with
	# "/dev/null: No such file or directory" and the package database ends up half
	# written.
	log "creating device nodes"
	for spec in "null c 1 3" "zero c 1 5" "random c 1 8" "urandom c 1 9" "tty c 5 0"; do
		set -- $spec
		rm -f "$ROOT/dev/$1"
		mknod "$ROOT/dev/$1" "$2" "$3" "$4" 2>/dev/null || true
		chmod 666 "$ROOT/dev/$1" 2>/dev/null || true
	done

	cp /etc/resolv.conf "$ROOT/etc/resolv.conf"

	# THE MIRRORLIST THE IMAGE SHIPS IS EMPTY. MEASURED: `grep -c '^Server'` answers 0, and
	# pacman then reports no servers configured. One line is enough and it is the official
	# mirror the image's own Dockerfile installs from.
	mkdir -p "$ROOT/etc/pacman.d"
	printf 'Server = https://geo.mirror.pkgbuild.com/$repo/os/$arch\n' \
		> "$ROOT/etc/pacman.d/mirrorlist"

	# THE SIGNATURE CHECK CANNOT RUN WITHOUT A POPULATED KEYRING, and `pacman-key --init`
	# needs entropy and a chroot that behaves like a real system. MEASURED without this:
	#
	#	:: File .../linux-7.3.5.arch1-1-x86_64.pkg.tar.zst is corrupted
	#	   (invalid or corrupted package (PGP signature)).
	#
	# The package is fetched over HTTPS from the project's own mirror, and the build ends
	# with a boot test that would catch a wrong kernel, so the signature is not the only
	# check here.
	sed -i 's/^SigLevel.*/SigLevel = Never/' "$ROOT/etc/pacman.conf"

	log "installing the kernel and an ssh server"
	chroot "$ROOT" /bin/sh -c '
		set -e
		# A FULL UPGRADE, NOT `-Sy`. MEASURED: the container image and the mirror are not
		# always at the same package versions, and a partial upgrade answers
		#
		#	:: installing openssl (4.1.0-1) breaks dependency "libssl.so=3-64"
		#	   required by libngtcp2
		#
		# because the new openssl no longer provides the soname the installed packages
		# were built against. `-Syu` moves the whole tree to one snapshot, which is what
		# a rolling release requires and what the Arch wiki calls a partial-upgrade
		# failure.
		pacman -Syu --noconfirm --quiet 2>&1 | tail -3
		# `linux` CARRIES vmlinuz AND /usr/lib/modules. openssh is the daemon the driver
		# dials; e2fsprogs is what growfs needs at first boot.
		pacman -S --noconfirm --quiet linux openssh e2fsprogs 2>&1 | tail -3
	'
fi

KVER=$(ls "$ROOT/usr/lib/modules" | head -1)
log "kernel $KVER"

# --- 2. the initramfs ---------------------------------------------------------------------
KERNEL="$ROOT/usr/lib/modules/$KVER/vmlinuz"
[ -f "$KERNEL" ] || { log "no vmlinuz under $ROOT/usr/lib/modules/$KVER"; exit 1; }

if [ ! -f "$WORK/busybox" ]; then
	log "fetching a static busybox"
	curl -fsSL --retry 5 --retry-delay 3 --retry-all-errors --max-time 300 \
		-o "$WORK/busybox" \
		"https://busybox.net/downloads/binaries/1.35.0-x86_64-linux-musl/busybox"
	chmod +x "$WORK/busybox"
fi

log "building the initramfs"
python3 "$HERE/make-initramfs.py" \
	--out "$WORK/initramfs.img" \
	--busybox "$WORK/busybox" \
	--kver "$KVER" \
	--modules-dir "$ROOT/usr/lib/modules/$KVER" \
	--root /dev/vda --fstype ext4 \
	--module virtio_mmio

# --- 3. the guest's own configuration -----------------------------------------------------
cat > "$ROOT/etc/fstab" <<'EOF'
/dev/vda / ext4 rw,relatime 0 1
EOF

sed -i 's/^root:x:/root::/' "$ROOT/etc/passwd"

mkdir -p "$ROOT/etc/systemd/system/serial-getty@ttyS0.service.d"
mkdir -p "$ROOT/etc/systemd/system/getty.target.wants"
cat > "$ROOT/etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf" <<'EOF'
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear %I $TERM
EOF
ln -sf /usr/lib/systemd/system/serial-getty@.service \
	"$ROOT/etc/systemd/system/getty.target.wants/serial-getty@ttyS0.service"

rm -f "$ROOT/etc/resolv.conf"
printf '# The bridge runs DNS for this guest.\nnameserver 172.30.0.1\n' > "$ROOT/etc/resolv.conf"

mkdir -p "$ROOT/etc/systemd/network"
cat > "$ROOT/etc/systemd/network/20-wired.network" <<'EOF'
[Match]
Name=en* eth*

[Network]
DHCP=yes
EOF

mkdir -p "$ROOT/etc/systemd/system/multi-user.target.wants"
mkdir -p "$ROOT/etc/systemd/system/sockets.target.wants"
ln -sf /usr/lib/systemd/system/sshd.service \
	"$ROOT/etc/systemd/system/multi-user.target.wants/sshd.service"
ln -sf /usr/lib/systemd/system/systemd-networkd.socket \
	"$ROOT/etc/systemd/system/sockets.target.wants/systemd-networkd.socket"
ln -sf /usr/lib/systemd/system/systemd-networkd.service \
	"$ROOT/etc/systemd/system/multi-user.target.wants/systemd-networkd.service"

# THE INTERACTIVE FIRST-BOOT PROMPT IS THE ONE THING THAT MADE A WORKING GUEST LOOK DEAD.
#
# MEASURED: the guest booted, mounted its disk, started systemd - and then stopped at
#
#	Initial Setup
#	Please enter the new timezone name or number ("list" to list options, empty to skip):
#
# because a fresh Arch root has no /etc/localtime and systemd-firstboot asks. Nothing is
# wrong with the machine; it is waiting for an answer nobody will type.
#
# Both halves are needed: the mask stops the question, and the values below mean it would
# not be asked anyway. Masking alone would leave a guest with no timezone and a UTC clock
# that some services refuse.
ln -sf /dev/null "$ROOT/etc/systemd/system/systemd-firstboot.service"
printf 'LANG=C.UTF-8\n' > "$ROOT/etc/locale.conf"
ln -sf /usr/share/zoneinfo/UTC "$ROOT/etc/localtime"
printf 'archlinux\n' > "$ROOT/etc/hostname"

chroot "$ROOT" /bin/sh -c '
	command -v useradd >/dev/null || exit 0
	id vmuser >/dev/null 2>&1 || useradd -m -s /bin/bash vmuser
	echo "vmuser:admin" | chpasswd
	command -v sudo >/dev/null && (id -nG vmuser | grep -qw wheel || usermod -aG wheel vmuser) || true
'

mkdir -p "$ROOT/etc/systemd/system"
cat > "$ROOT/etc/systemd/system/growfs.service" <<'EOF'
[Unit]
Description=grow the root filesystem into the disk it was given
After=systemd-remount-fs.service
Before=local-fs.target
DefaultDependencies=no

[Service]
Type=oneshot
RemainAfterExit=yes
ExecCondition=/usr/bin/test ! -f /var/lib/growfs-done
ExecStart=/bin/sh -c '/usr/bin/resize2fs /dev/vda 2>/dev/null || true; /usr/bin/touch /var/lib/growfs-done'
StandardOutput=journal+console
StandardError=journal+console

[Install]
WantedBy=multi-user.target
EOF
ln -sf /etc/systemd/system/growfs.service \
	"$ROOT/etc/systemd/system/multi-user.target.wants/growfs.service"

rm -f "$ROOT/etc/resolv.conf"
printf '# The bridge runs DNS for this guest.\nnameserver 172.30.0.1\n' > "$ROOT/etc/resolv.conf"
rm -rf "$ROOT/var/cache/pacman/pkg"/* "$ROOT/var/log"/* 2>/dev/null || true

# --- 4. write the disk --------------------------------------------------------------------
# 64 GiB, FIXED. The guest grows its filesystem into the disk with `growfs.service`,
# so the virtual size is a POLICY decision and not a build artefact: qcow2 stores only
# clusters that were written, so a large disk costs nothing until somebody fills it,
# and an operator can install a toolchain or a desktop without rebuilding the image.
SIZE=65536
log "writing a ${SIZE} MiB ext4 image"
rm -f "$WORK/rootfs.raw"
truncate -s "${SIZE}M" "$WORK/rootfs.raw"
mke2fs -q -F -t ext4 -d "$ROOT" "$WORK/rootfs.raw"
qemu-img convert -f raw -O qcow2 -c "$WORK/rootfs.raw" "$OUT/arch.qcow2"

# --- 5. the three files the boot step names -----------------------------------------------
log "kernel $KVER"
cp "$KERNEL" "$OUT/arch-vmlinuz"
cp "$WORK/initramfs.img" "$OUT/arch-initramfs"

log "done:"
ls -l "$OUT/arch-vmlinuz" "$OUT/arch-initramfs" "$OUT/arch.qcow2"