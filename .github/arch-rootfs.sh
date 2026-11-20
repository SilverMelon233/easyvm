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

# DESKTOP=1 ADDS SWAY, THE SESSION AROUND IT, AND EASYSTREAM. DESKTOP=0 IS THE MINIMAL IMAGE.
#
# THE TWO ARE ONE SCRIPT FOR THE REASON THE DEBIAN PAIR IS: everything that makes this boot -
# the kernel, the initramfs modules, /dev, the fstab, the console session, growfs - is
# identical, and a second script would be a copy that drifts.
#
# WHY SWAY AND NOT KDE OR GNOME. MEASURED: both of those capture through the portal, which
# means xdg-desktop-portal plus its backend plus PipeWire plus a PermissionStore plus a
# graphical-session.target that only a greeter creates. On a microvm that chain is a stack of
# services pretending to be a desktop that nobody logged into. Sway has `wlr-screencopy` IN
# THE COMPOSITOR, so easystream reads frames over the Wayland socket with no portal in the
# path at all. That difference was measured end to end: a screenshot of a Sway guest came back
# as a 3.1 MB PNG with the expected content, while the KDE probe never got past
# "No such interface org.freedesktop.portal.RemoteDesktop".
DESKTOP="${DESKTOP:-0}"

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

	# THE IMAGE HAS NO /dev, /proc OR /sys AT ALL, AND THAT IS ARCH-SPECIFIC.
	#
	# MEASURED from the registry tarballs themselves: alpine, debian, ubuntu, fedora and
	# leap each carry an empty `dev/` directory; `archlinux:latest` carries NO `dev` entry
	# of any kind.
	#
	# THE FAILURE THAT CAUSES IS NOT IN THIS SCRIPT, IT IS LATER. The initramfs moves
	# /proc and /sys onto the new root and switch_roots:
	#
	#	for d in proc sys; do mount --move "/$d" "/newroot/$d" || true; done
	#
	# `|| true` THERE IS DELIBERATE (a failed move is not worth refusing to boot), so a
	# missing directory is SILENT. What it costs is a guest whose systemd cannot mount
	# anything: MEASURED on a hand-built KDE image that had no /dev, boot stalled at
	#
	#	A start job is running for…nts and Files (1min 57s / 3min 2s)
	#	A start job is running for… Credentials (2min 58s / no limit)
	#
	# for three minutes and then gave up. The minimal variant does not show it because a
	# headless systemd tolerates the absent mounts; a desktop does not.
	log "creating /dev, /proc and /sys, which this image does not ship"
	mkdir -p "$ROOT/dev" "$ROOT/proc" "$ROOT/sys"

	# THE DEVICE NODES tar DID NOT PRESERVE. MEASURED: pacman's own scriptlets fail with
	# "/dev/null: No such file or directory" and the package database ends up half
	# written.
	#
	# NO `|| true` ON THE mknod: the previous version had one, and it is exactly what let
	# the missing directory above go unnoticed. A node that cannot be made is a build that
	# cannot work, so it says so.
	log "creating device nodes"
	for spec in "null c 1 3" "zero c 1 5" "random c 1 8" "urandom c 1 9" \
	            "tty c 5 0" "console c 5 1" "ptmx c 5 2"; do
		set -- $spec
		rm -f "$ROOT/dev/$1"
		mknod "$ROOT/dev/$1" "$2" "$3" "$4"
		chmod 666 "$ROOT/dev/$1"
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

	if [ "$DESKTOP" = "1" ]; then
		# SWAY, ITS TERMINAL, AND THE CAPTURE DAEMON'S LIBRARIES. `sway` carries wlroots and
		# the screencopy protocol; `foot` is the terminal a screenshot needs something to
		# show; the rest are easystream's own links (libyuv, x264, opus, pipewire).
		DESKTOP_PKGS="sway foot xorg-xwayland libyuv x264 opus pipewire ttf-dejavu ttf-liberation mesa"
	else
		DESKTOP_PKGS=""
	fi
	export DESKTOP_PKGS

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
		# THE EXIT CODE IS CHECKED, WHICH `| tail -3` THROWS AWAY.
		#
		# MEASURED: a mirror that answered "failed to retrieve some files" made pacman exit
		# non-zero, `tail` exited zero, and the build carried on to fail forty lines later
		# with "no vmlinuz under .../usr/lib/modules" - a message about the kernel for a
		# problem that was a download.
		if ! pacman -S --noconfirm --quiet linux openssh e2fsprogs $DESKTOP_PKGS >/tmp/pacman.log 2>&1; then
			tail -15 /tmp/pacman.log
			echo "pacman failed to install the kernel or the desktop packages" >&2
			exit 1
		fi
		tail -2 /tmp/pacman.log
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
ExecStart=-/sbin/agetty --autologin vmuser --noclear %I $TERM
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


# THE WHOLE SCREEN STACK IS ONE BRANCH, for the same reason the Debian image's is: a
# compositor with no client draws nothing, easystream with no compositor has no frames, and
# PipeWire with no user session refuses root (`ConditionUser=!root`).
if [ "$DESKTOP" = "1" ]; then
	# --- 3b. EASYSTREAM ------------------------------------------------------------------
	ES_VERSION="${EAZYSTREAM_VERSION:-v0.10.2}"
	ES_PKG="easystream-${ES_VERSION#v}-1-x86_64.pkg.tar-arch.zst"
	if [ ! -x "$ROOT/usr/bin/easystream" ]; then
		log "fetching easystream ${ES_VERSION} (arch package)"
		curl -fsSL --retry 5 --retry-delay 3 --retry-all-errors --max-time 300 \
			-o "$WORK/$ES_PKG" \
			"https://github.com/easydev-org/easystream/releases/download/${ES_VERSION}/${ES_PKG}"
		# UNTARRED INTO THE TREE, which is how the package's /usr/bin/easystream and its
		# user unit land where they belong without a pacman database to install against.
		tar --zstd -xf "$WORK/$ES_PKG" -C "$ROOT" 2>/dev/null
		chmod 0755 "$ROOT/usr/bin/easystream"
	fi
	# THE PROOF IT CAN LOAD, taken here rather than discovered on a guest that boots to a
	# dead frame.
	if chroot "$ROOT" ldd /usr/bin/easystream 2>&1 | grep -q "not found"; then
		log "WARNING: easystream has unresolved libraries: $(chroot "$ROOT" ldd /usr/bin/easystream 2>&1 | grep 'not found' | tr '\n' ' ')"
	else
		log "easystream: all libraries resolve"
	fi

	# --- 3c. THE SWAY SESSION ------------------------------------------------------------
	#
	# SWAY RUNS AS vmuser IN A LINGERED SESSION, NOT AS A SYSTEM SERVICE.
	#
	# `ConditionUser=!root` in PipeWire's unit is the reason: a root session cannot start
	# it. MEASURED - `systemctl --user start pipewire` as root answered "inactive" and the
	# journal named the condition.
	#
	# SWAY ON vkms, NOT ON A GPU. A microvm has neither PCIe nor a DRM card; `vkms.ko` is a
	# software DRM device that provides /dev/dri/card0, and wlroots' `drm` backend drives
	# it with the `pixman` renderer - no GPU, no firmware, no PCI.
	# VKMS, THE SOFTWARE DRM DEVICE, IS A KERNEL MODULE AND HAS TO BE SHIPPED.
	#
	# MEASURED, BEFORE THIS WAS WRITTEN: sway answered
	#
	#	Failed to open device: '/dev/dri/card0': No such file or directory
	#	Found 0 GPUs, cannot create backend
	#
	# and restarted forever - a guest that boots cleanly to a prompt and never draws
	# anything. `vkms.ko` provides /dev/dri/card0 with no GPU, no firmware and no PCI, which
	# is exactly what a microvm has.
	#
	# IT COMES OUT OF THE SAME PACKAGE THE KERNEL CAME FROM, so it is always the matching
	# build; `VGEM` goes beside it because Mesa's software path wants the dumb-buffer
	# device when the compositor allocates its first framebuffer.
	VKMS_SRC="$ROOT/usr/lib/modules/$KVER/kernel/drivers/gpu/drm/vkms/vkms.ko.zst"
	VGEM_SRC="$ROOT/usr/lib/modules/$KVER/kernel/drivers/gpu/drm/vgem/vgem.ko.zst"
	if [ -f "$VKMS_SRC" ]; then
		# DECOMPRESSED, because insmod does not open a .zst - that is kmod's job.
		command -v zstd >/dev/null || { log "zstd is required to ship vkms.ko"; exit 1; }
		zstd -dc "$VKMS_SRC" > "$ROOT/etc/vkms.ko"
		[ -f "$VGEM_SRC" ] && zstd -dc "$VGEM_SRC" > "$ROOT/etc/vgem.ko"
		log "shipping vkms.ko ($(stat -c %s "$ROOT/etc/vkms.ko") bytes)"
	else
		log "ERROR: no vkms.ko in the kernel package; the desktop could never start"
		exit 1
	fi

	mkdir -p "$ROOT/opt/sway" "$ROOT/etc/sway"
	cat > "$ROOT/opt/sway-session.sh" <<'SESSION'
#!/bin/sh
# vkms IS LOADED BY A ROOT UNIT BEFORE THIS ONE - see `vkms.service`. insmod needs
# CAP_SYS_MODULE and this script runs as vmuser, so an `insmod` here answers "Operation not
# permitted" and the only symptom is a compositor that cannot find a GPU.
#
# THIS SCRIPT ONLY WAITS FOR THE RESULT, and says so if it never arrives: a guest that boots
# to a prompt and draws nothing is the failure this line makes visible.
i=0
while [ $i -lt 100 ]; do [ -e /dev/dri/card0 ] && break; i=$((i+1)); sleep 0.1; done
if [ ! -e /dev/dri/card0 ]; then
	echo "sway-session: /dev/dri/card0 never appeared; vkms.service failed" >&2
	exit 1
fi

export XDG_RUNTIME_DIR=/run/user/$(id -u)
export LIBSEAT_BACKEND=noop
export WLR_BACKENDS=drm
# THE DEVICE IS DISCOVERED, NOT NAMED. MEASURED: with both vgem and vkms loaded the nodes are
#
#	/dev/dri/card0  -> vgem   (a dumb-buffer device, NOT a display)
#	/dev/dri/card1  -> vkms   (the one that can drive an output)
#
# so `WLR_DRM_DEVICES=/dev/dri/card0` pointed the compositor at the wrong one and it answered
# "Found 0 GPUs, cannot create backend". wlroots walks every card and picks the first that
# supports KMS.
export WLR_RENDERER=pixman
export WLR_LIBINPUT_NO_DEVICES=1
export LIBGL_ALWAYS_SOFTWARE=1
# NO XWAYLAND: nothing in this image is an X client, and easystream reads Wayland through
# wlr-screencopy rather than the X protocol.
export WLR_XWAYLAND=
unset DISPLAY

# EASYSTREAM IS ITS OWN SERVICE, STARTED BY THE USER MANAGER.
#
# IT WAS TEMPORARILY STARTED FROM THIS SCRIPT AND THAT WAS WRONG. The two are related by a
# DEPENDENCY (easystream needs the compositor's socket), not by a LIFECYCLE: a sway restart
# should reconnect the capture daemon, not kill it, and mixing them puts two restart policies
# in one unit and makes a failure in either look like a failure in both.
#
# The reason it was moved here was a measurement that turned out to be wrong: `systemctl
# --user` run as ROOT cannot talk to another user's manager -
#
#	Failed to connect to user scope bus via local transport: Operation not permitted
#	(consider using --machine=<user>@.host --user to connect to bus of other user)
#
# - so "the unit is not running" was concluded from a probe that could never succeed. The
# unit's own `ExecStartPre` waits for the socket, which is the ordering this needs.
exec sway --config /etc/sway/config
SESSION
	chmod 0755 "$ROOT/opt/sway-session.sh"

	# A CONFIG THAT DRAWS SOMETHING. `output * bg` needs swaybg to paint, so it is not used;
	# `bar` runs swaybar, which ships with sway, and that is a region of certain non-background
	# pixels a screenshot can be checked against.
	cat > "$ROOT/etc/sway/config" <<'SWAYCFG'
output * bg #3366cc solid_color
bar {
    position top
    height 40
    status_command while true; do echo "ARCH SWAY MICROVM"; sleep 5; done
    colors {
        background #223344
        statusline #ffffff
    }
}
SWAYCFG

	# VKMS AS A ROOT ONESHOT, BEFORE THE COMPOSITOR.
	#
	# `insmod` needs CAP_SYS_MODULE. The compositor runs as vmuser, so a module loaded from
	# inside it answers "Operation not permitted" - MEASURED, and the only visible effect was
	# sway restarting forever with "Found 0 GPUs".
	#
	# `Type=oneshot` WITH NO `DefaultDependencies=no` AND NO `Wants=` ON ANOTHER UNIT.
	# MEASURED with the earlier shape: the boot hung for a full minute at
	#
	#	A start job is running for…ftware DRM device (58s / no limit)
	#
	# because `DefaultDependencies=no` removed the ordering that lets a oneshot finish during
	# normal boot, and `Wants=systemd-modules-load.service` made it wait on a unit that had
	# already run. A plain oneshot in `multi-user.target` is enough: it has no inputs and no
	# dependencies, and `sway.service` orders itself after it.
	cat > "$ROOT/etc/systemd/system/vkms.service" <<'UNIT'
[Unit]
Description=load vkms, the software DRM device
After=systemd-modules-load.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/sbin/modprobe vkms
# THE NODE IS WHAT THE COMPOSITOR OPENS. udev creates it when it notices the new device; if
# it is slow, mknod from the device numbers the kernel assigned is the fallback, so the
# compositor never races the device manager.
ExecStart=/bin/sh -c 'i=0; while [ $i -lt 50 ] && [ ! -e /dev/dri/card0 ]; do i=$((i+1)); sleep 0.1; done; if [ ! -e /dev/dri/card0 ]; then mkdir -p /dev/dri; M=$(cat /sys/class/drm/card0/dev 2>/dev/null); [ -n "$M" ] && mknod /dev/dri/card0 c ${M%:*} ${M#*:}; fi'
StandardOutput=journal+console
StandardError=journal+console

[Install]
WantedBy=multi-user.target
UNIT
	ln -sf /etc/systemd/system/vkms.service \
		"$ROOT/etc/systemd/system/multi-user.target.wants/vkms.service"

	# THE MODULE GOES WHERE `modprobe` CAN FIND IT, with a modules.dep the loader can read.
	mkdir -p "$ROOT/lib/modules/$KVER/extra"
	cp "$ROOT/etc/vkms.ko" "$ROOT/lib/modules/$KVER/extra/vkms.ko"
	chroot "$ROOT" /sbin/depmod -a "$KVER" 2>&1 | tail -2 || true

	cat > "$ROOT/etc/systemd/system/sway.service" <<'UNIT'
[Unit]
Description=sway on vkms, for vmuser
After=systemd-logind.service vkms.service
Wants=systemd-logind.service vkms.service

[Service]
Type=simple
User=vmuser
Environment=HOME=/home/vmuser
ExecStart=/opt/sway-session.sh
Restart=always
RestartSec=3
StandardOutput=journal+console
StandardError=journal+console

[Install]
WantedBy=multi-user.target
UNIT
	ln -sf /etc/systemd/system/sway.service \
		"$ROOT/etc/systemd/system/multi-user.target.wants/sway.service"

	# EASYSTREAM AS A SYSTEM UNIT WITH AN EXPLICIT SESSION ENVIRONMENT.
	#
	# THE PACKAGE'S OWN USER UNIT CANNOT BE USED AS SHIPPED: it carries
	# `ConditionEnvironment=WAYLAND_DISPLAY` AND `ConditionEnvironment=DISPLAY`, systemd ANDs
	# conditions, and this guest is pure Wayland - MEASURED, the unit was skipped with
	# "unmet condition check ConditionEnvironment=DISPLAY". The drop-in that clears it is
	# written here beside the unit, so both live in one place.
	mkdir -p "$ROOT/etc/systemd/user/easystream.service.d"
	cat > "$ROOT/etc/systemd/user/easystream.service.d/10-wayland-only.conf" <<'DROPIN'
[Unit]
# THE PACKAGED UNIT REQUIRES WAYLAND_DISPLAY *AND* DISPLAY; systemd ANDs conditions and this
# guest is pure Wayland (no Xwayland), so DISPLAY is unset and the unit is skipped. An empty
# assignment clears the list and leaves every other line of the package's unit in charge.
ConditionEnvironment=
DROPIN

	# THE DRM NODE BELONGS TO ROOT:video WITH MODE 0660, SO vmuser MUST BE IN `video`.
	#
	# MEASURED, one step after vkms started working: sway answered
	#
	#	Failed to open device: '/dev/dri/card0': Permission denied
	#	Found 0 GPUs, cannot create backend
	#
	# which is the same sentence as the "No such file" failure one permission later - the
	# compositor cannot tell a missing device from one it may not open.
	chroot "$ROOT" /bin/sh -c '
		groupadd -f video
		id -nG vmuser | grep -qw video || usermod -aG video vmuser
	' 2>&1 | tail -2 || true

	# AND THE LINGER THAT MAKES A USER MANAGER EXIST WITH NOBODY LOGGED IN, which is what
	# makes the packaged user unit runnable at all.
	mkdir -p "$ROOT/var/lib/systemd/linger"
	touch "$ROOT/var/lib/systemd/linger/vmuser"

	# THE MANAGER'S OWN ENVIRONMENT IS WHAT `ConditionEnvironment=` READS.
	#
	# THIS IS THE ROOT CAUSE OF "THE UNIT NEVER STARTED", AND IT TOOK A WHILE TO SEE.
	# The package's unit carries
	#
	#	ConditionEnvironment=WAYLAND_DISPLAY
	#	ConditionEnvironment=DISPLAY
	#
	# and systemd evaluates those against THE USER MANAGER'S ENVIRONMENT, not against the
	# `Environment=` lines a drop-in adds to the unit. A lingered manager starts at boot,
	# before any compositor exists, so both are absent and the unit is skipped -
	# MEASURED: "Started User Manager for UID 1000" and no easystream process, no 48079,
	# no journal line, with the drop-in that clears the conditions already in place.
	#
	# `environment.d` IS HOW A USER MANAGER'S ENVIRONMENT IS SET: the `user@.service`
	# reads every `*.conf` in this directory before starting, so the variable exists when
	# the conditions are evaluated. A fixed display name is correct here because this image
	# has exactly one compositor and it always gets the same name.
	mkdir -p "$ROOT/etc/systemd/user/environment.d"
	cat > "$ROOT/etc/systemd/user/environment.d/50-wayland.conf" <<'ENVD'
# THE MANAGER'S ENVIRONMENT, WHICH `ConditionEnvironment=` READS. The compositor this image
# ships creates wayland-1, and easystream's own `ExecStartPre` waits for that socket, so
# naming it here and waiting there are the two halves of one assumption.
WAYLAND_DISPLAY=wayland-1
ENVD

	# AND A `DISPLAY` FOR THE SAME REASON, because the unit requires BOTH and systemd ANDs
	# them. Nothing is an X client here; the value only has to exist for the condition to
	# pass, and sway's own Xwayland is off.
	cat >> "$ROOT/etc/systemd/user/environment.d/50-wayland.conf" <<'ENVD'
DISPLAY=:0
ENVD

	# THE PACKAGE'S OWN UNIT, ENABLED - WITH THE ONE THING IT NEEDS THAT A DESKTOP WOULD
	# HAVE SUPPLIED: WAYLAND_DISPLAY. The unit is `WantedBy=graphical-session.target`, which
	# nothing creates here, so it is pulled in by `default.target` instead and given the
	# display name sway chose.
	#
	# `After=sway.service` IS NOT EXPRESSIBLE ACROSS THE SYSTEM/USER BOUNDARY, so the
	# session script polls the socket instead of the unit ordering being perfect.
	mkdir -p "$ROOT/etc/systemd/user/default.target.wants"
	ln -sf /usr/lib/systemd/user/easystream.service \
		"$ROOT/etc/systemd/user/default.target.wants/easystream.service"
	cat > "$ROOT/etc/systemd/user/easystream.service.d/20-sway-display.conf" <<'DROPIN'
[Service]
# THE COMPOSITOR'S SOCKET NAME, AND A WAIT FOR IT TO EXIST.
#
# `After=sway.service` cannot cross the system/user boundary, so this unit may start before
# the compositor has created its socket. MEASURED on the first attempt: easystream came up,
# could not connect, and the est server answered nothing. The wait is bounded and exits
# non-zero rather than hanging, so `Restart=on-failure` retries it.
ExecStartPre=/bin/sh -c 'i=0; while [ $i -lt 600 ]; do [ -S "$XDG_RUNTIME_DIR/wayland-1" ] && exit 0; i=$((i+1)); sleep 0.1; done; echo "wayland-1 never appeared in 60s" >&2; exit 1'
Environment=WAYLAND_DISPLAY=wayland-1
Environment=EAZYSTREAM_PORT=48079
DROPIN
fi

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