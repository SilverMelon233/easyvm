{
  # THE NIXOS MICROVM, AS A FLAKE RATHER THAN A SCRIPT.
  #
  # Alpine and Debian are built here by hand (`alpine-rootfs.sh` downloads a rootfs, drops in an
  # initramfs, writes a qcow2). NixOS cannot be built that way because it is not distributed as a
  # disk image at all: it is a *description* of a system, and the only thing that can turn that
  # description into a kernel, an initramfs and a store disk is Nix. So this directory holds the
  # description and CI holds the builder.
  #
  # WHAT COMES OUT, AND WHY EXACTLY THREE FILES:
  #
  #   kernel     - the NixOS kernel; a microvm takes it with -kernel, no bootloader
  #   initrd     - systemd initrd, which mounts the store before handing over
  #   store.img  - the whole NixOS system as one read-only erofs image, mounted at /nix/store
  #
  # That is the same three-file contract `microvm.nix` itself boots with, and the same reason the
  # guest needs no root disk: `/` is a tmpfs and everything the system *is* lives in the store.
  #
  # ------------------------------ THE VIRTIO-MMIO QUESTION ------------------------------
  #
  # qemu's `microvm` machine has no PCI bus. Its block devices are `virtio-blk-device`, which is
  # virtio over MMIO, and a machine with no firmware cannot describe those devices in an ACPI table -
  # so qemu instead prints the addresses ON THE KERNEL COMMAND LINE:
  #
  #   virtio_mmio.device=512@0xfeb00e00:12
  #
  # A kernel only honours that line if it was built with CONFIG_VIRTIO_MMIO_CMDLINE_DEVICES. This is
  # the whole reason Void was dropped: MEASURED, `virtio_mmio.ko` in the Void kernel contains the
  # string `virtio_mmio.device=` zero times, so a Void microvm boots and never sees a disk.
  #
  # NIXPKGS ALREADY SETS IT, which is why this file does not patch the kernel. Verified, not assumed:
  # `pkgs/os-specific/linux/kernel/common-config.nix` line 1045, in the `virtualisation` block,
  #
  #   # Enable device detection on virtio-mmio hypervisors
  #   VIRTIO_MMIO_CMDLINE_DEVICES = yes;
  #
  # `yes` there is unconditional, not `option yes`, so it cannot be turned off downstream and it
  # cannot be missing from a stock NixOS kernel. Patching it in anyway would be worse than useless:
  # `boot.kernelPatches` changes the kernel derivation, so the build would stop hitting
  # cache.nixos.org and start compiling a kernel from source for no gain. Instead CI reads the
  # built kernel's `.config` and fails if the symbol is absent - see `verify` in the workflow. The
  # claim is checked, not repeated.
  #
  # ------------------------------ WHY NO PCIE, AND WHAT IT COSTS ------------------------------
  #
  # `microvm.nix` derives `requirePci` from `graphics.enable || shares != [] || pciInDevices ||
  # machine !~ ^microvm`. Nothing below sets any of those, so it picks `devType = "device"` and
  # emits `virtio-blk-device` - the MMIO transport, no PCIe bus, which is the requirement here.
  #
  # The cost is graphics: a framebuffer needs a GPU, a GPU needs PCIe, and with `pcie=off` there is
  # no `/dev/fb0`. That is not a gap to be worked around - it is why this guest is built to be read
  # on `ttyS0`. The Alpine and Debian images do carry a desktop (`microvm_display: desktop`); this
  # one deliberately does not, and the walkthrough that boots it asserts on the serial console.
  description = "NixOS guest for qemu's microvm machine type: kernel, initrd and erofs store";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    microvm = {
      url = "github:microvm-nix/microvm.nix";
      # ONE nixpkgs FOR BOTH, or the kernel and the store image are built from two different
      # revisions and the store's glibc does not match the kernel's modules.
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, microvm }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};

      guest = nixpkgs.lib.nixosSystem {
        inherit system;
        modules = [
          microvm.nixosModules.microvm
          ({ pkgs, ... }: {
            # ---------------------------- THE MACHINE ----------------------------
            networking.hostName = "nixos-microvm";

            # `machineOpts` REPLACES microvm.nix's computed defaults rather than merging with
            # them, so every option that matters is stated here. They are the same five the
            # easyvm driver passes (`internal/daemon/driver.go`), which is the point: the image is
            # built for the machine that will actually run it, not for microvm.nix's own runner.
            #
            #   accel = "kvm:tcg"  - kvm where there is one, tcg where there is not. A bare "kvm"
            #                        would make the artifact unusable on any runner without
            #                        /dev/kvm, and CI runners have no /dev/kvm.
            #   acpi = "off"       - there is no PCI bus for ACPI to enumerate, and the MMIO
            #                        devices arrive on the command line instead.
            #   pic  = "on"        - kept on for parity with the Alpine and Debian images; the
            #                        timer has to come from somewhere with no ACPI.
            #   pcie = "off"       - stated, not inferred. This is the constraint.
            microvm.qemu.machine = "microvm";
            microvm.qemu.machineOpts = {
              accel = "kvm:tcg";
              rtc = "on";
              pic = "on";
              acpi = "off";
              pcie = "off";
              usb = "off";
            };

            # 1024 AND NOT 2048: microvm.nix warns that qemu hangs when the microvm machine gets
            # exactly 2 GiB (microvm.nix#171). 1024 is also what the other three images use.
            microvm.mem = 1024;
            microvm.vcpu = 2;

            # ---------------------------- THE STORE DISK ----------------------------
            # erofs, not ext4: the store is read-only and never written, so a compressed read-only
            # filesystem is both smaller and faster to mount. Measured on the other images, a
            # NixOS closure as ext4 is several hundred MB larger for the same content.
            #
            # `mounts.nix` picks the device by LABEL when the type is erofs
            # (`/dev/disk/by-label/nix-store`), which is why the label `nix-store` set by
            # `store-disk.nix` matters: it is the only name stable across machine types.
            microvm.storeDiskType = "erofs";

            # NO `writableStoreOverlay`: a writable /nix/store would need a second disk, and the
            # point of this artifact is three files. The consequence is that the guest cannot
            # `nix-build` - it can boot, log in and run everything in the closure, which is what
            # the walkthrough checks.

            # ---------------------------- A CONSOLE ----------------------------
            boot.kernelParams = [ "console=ttyS0" ];

            # THE ROOT ACCOUNT IS THE ONLY ONE, and the password is empty because a read-only
            # store means there is no state worth protecting and no way to run `passwd` that
            # survives a reboot anyway.
            users.users.root = {
              password = "";
              shell = pkgs.bashInteractive;
            };
            services.getty.autologinUser = "root";

            # NIX ITSELF STAYS ENABLED, and its daemon does not start: `system.nix` in microvm.nix
            # sets `services.nix-daemon.enable` with `mkDefault` to
            # `writableStoreOverlay != null`, which is false here. Saying `nix.enable = false`
            # instead would remove the package the store image's own registration needs and buy
            # nothing, since the daemon was already off.

            # ---------------------------- WHAT PROVES IT IS ALIVE ----------------------------
            # A LINE ON THE SERIAL CONSOLE, PRINTED ONCE USERS HAVE STARTED. `echo` is a
            # coreutils binary inside the store, so this unit can only run if the store disk was
            # found, mounted, and the closure inside it is intact - which is exactly the claim
            # the artifact needs to support. Asserting on `login:` alone would not: agetty comes
            # from the initrd's own closure.
            systemd.services.microvm-proof = {
              description = "announce that the store mounted and userspace is up";
              wantedBy = [ "multi-user.target" ];
              after = [ "local-fs.target" ];
              before = [ "getty.target" ];
              serviceConfig = {
                Type = "oneshot";
                RemainAfterExit = true;
                ExecStart = "${pkgs.coreutils}/bin/echo NIXOS-MICROVM-READY";
                StandardOutput = "journal+console";
              };
            };

            system.stateVersion = "25.11";
          })
        ];
      };
    in {
      nixosConfigurations.nixos-microvm = guest;

      # THE THREE FILES, IN ONE DIRECTORY, WITH A STABLE NAME EACH.
      #
      # `runCommandLocal` rather than `symlinkJoin` so the artifact directory cannot grow an
      # accidental `/nix/store` dependency of its own: each entry is a symlink into the store, and
      # CI resolves them with `cp -L` before uploading.
      #
      # `microvm.kernel` IS A PACKAGE, NOT A FILE - its `out` is a directory holding `bzImage`
      # beside `System.map` and friends. `microvm.nix`'s own runner turns it into a path with
      # `kernelPath = "${kernel.out}/${linuxTarget}"`, and `linuxTarget` is `pkgs.linux.target`
      # ("bzImage" on x86_64). Linking the package directory instead would produce an artifact
      # named `kernel` that is a folder, which qemu rejects with "not a valid bzImage".
      #
      # ONE `packages.${system}` ATTRIBUTE WITH THREE KEYS, not three assignments to it: Nix reads
      # `packages.${system}.default = ...` followed by `packages.${system}.kernelConfig = ...` as
      # defining the dynamic attribute `packages.${system}` twice and refuses the whole file. Which
      # is what an evaluation said, not what the manual implies.
      packages.${system} = {
        default = pkgs.runCommandLocal "nixos-microvm-artifacts" { } ''
          mkdir -p $out
          ln -s ${guest.config.microvm.kernel.out}/${pkgs.linux.target} $out/kernel
          ln -s ${guest.config.microvm.initrdPath} $out/initrd
          ln -s ${guest.config.microvm.storeDisk} $out/store.img
        '';

        # THE KERNEL'S `.config`, AS ITS OWN OUTPUT, so CI can read it instead of trusting a
        # comment. `passthru.configfile` is the derivation nixpkgs builds from the merged Kconfig;
        # it is cheap and cacheable on its own, and it is the only honest way to answer "is
        # VIRTIO_MMIO_CMDLINE_DEVICES really y in the kernel we are about to ship".
        kernelConfig = guest.config.boot.kernelPackages.kernel.passthru.configfile;

        # THE EXACT COMMAND THAT BOOTS THIS ARTIFACT, generated from the configuration rather than
        # typed out beside it, so the two cannot drift.
        #
        # THE `-append` LINE IS THE PART THAT IS EASY TO GET WRONG, AND IT ALREADY WAS: the first
        # attempt booted the kernel without one and stopped at `Probing EDD (edd=off to disable)...
        # ok`, because a microvm has no bootloader to supply one and the kernel therefore had no
        # `init=` to run. It is not merely a console setting. `init=` names a path INSIDE the store
        # image, which the initrd finds by label and mounts before it execs anything:
        #
        #   init=/nix/store/...-nixos-system-nixos-microvm-26.11.../init
        #
        # So the arguments are read from `microvm.kernelParams` - the very list microvm.nix's own
        # runner joins onto its command line - and prefixed with the console options that runner
        # puts in front of them.
        #
        # `accel=kvm:tcg` means this also boots, slowly, on a machine without /dev/kvm; `-cpu host`
        # is dropped to `-cpu max` there by qemu itself, so the same line is honest on both.
        bootCommand = pkgs.writeText "boot-command.txt" ''
          # Boot the NixOS microvm from three files. Nothing else is needed: `/` is a tmpfs, and the
          # store image is the entire operating system.
          qemu-system-x86_64 \
            -M microvm,accel=kvm:tcg,rtc=on,pic=on,acpi=off,pcie=off,usb=off \
            -cpu host -m 1024 -smp 2 \
            -kernel kernel -initrd initrd \
            -drive file=store.img,if=none,id=store,format=raw,read-only=on \
            -device virtio-blk-device,drive=store \
            -device virtio-rng-device \
            -nodefaults -no-reboot -nographic \
            -append "earlyprintk=ttyS0 console=ttyS0 reboot=t panic=-1 ${nixpkgs.lib.concatStringsSep " " guest.config.microvm.kernelParams}"

          # `-nographic` ALREADY PUTS THE SERIAL CONSOLE ON YOUR TERMINAL. Do not add `-serial
          # file:...` beside it: -nographic redirects the port and wins, so the file stays empty and
          # the output appears on the console instead. MEASURED. If a log file is wanted, replace
          # `-nographic` with `-display none -serial file:LOG`.
          #
          # WHAT A CORRECT BOOT LOOKS LIKE: these three lines, in this order.
          #
          #   Found device /dev/disk/by-label/nix-store
          #   [    5.416244] echo[475]: NIXOS-MICROVM-READY
          #   nixos-microvm login: root (automatic login)
          #
          # The middle one is printed by a binary INSIDE the store image, so it cannot appear unless
          # the disk was found and mounted. `login: root` alone would not prove that: agetty lives in
          # the initrd, which is a separate file.
          # THE VIRTIO-MMIO ADDRESSES ARE NOT IN THAT LINE ON PURPOSE. qemu's microvm machine appends
          # them itself, derived from the devices it actually created:
          #
          #   virtio_mmio.device=512@0xfeb00e00:12
          #
          # Writing them by hand is how the Void attempt failed twice - the address depends on
          # device order, so a stale constant points at nothing and the kernel silently finds no disk.
        '';
      };
    };
}
