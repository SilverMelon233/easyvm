# The migration, verified

Eighteen walkthroughs became `.walk` files. Sixteen of them have been RUN to a finish - not parsed,
not converted back, not loaded: `easyvm run` drove a real guest through every step and the daemon
reported `status: finished step N/N` with `answered N step(s)`. The other two have documented causes:
one is a network abort that moves between steps, and one needs an input this runner cannot produce.

## The sixteen that pass

Every row below is a CI run of the branch, in the image that run built, with the log line quoted.

| walkthrough | steps | run | evidence |
|---|---|---|---|
| linux/ubuntu install | 44 | 44085510921 | `status: finished step 44/44` · `answered 44 step(s)` |
| linux/ubuntu ssh | 21 | 44118113239 | `status: finished step 21/21` · `answered 21 step(s)` |
| linux/ubuntu vm-configure | 31 | 44122235903 | `status: finished step 31/31` · `answered 31 step(s)` |
| bsd/freebsd install | 64 | 44097139713 | `status: finished step 64/64` · `answered 64 step(s)` |
| bsd/freebsd ssh | 18 | 44121817115 | `status: finished step 18/18` · `answered 18 step(s)` |
| bsd/freebsd vm-configure | 23 | 44123507364 | `status: finished step 23/23` · `answered 23 step(s)` |
| macos/15 setup | 35 | 44105336457 | `status: finished step 35/35` · `answered 35 step(s)` |
| macos/15 ssh | 32 | 44124857572 | `status: finished step 32/32` · `answered 32 step(s)` |
| macos/15 lifecycle | 7 | 44144030102 | `status: finished step 7/7` · `answered 7 step(s)` |
| macos/15 firmware | 2 | 44142319557 | `status: finished step 2/2` · `answered 2 step(s)` |
| macos/15 primitives | 19 | 44157758150 | `status: finished step 19/19` · `answered 19 step(s)` |
| macos/15 vm-configure | 32 | 44164173754 | `status: finished step 32/32` · `answered 32 step(s)` · `TIMEZONE_OK` · `BOTH_HALVES_OK` |
| macos/15 dev | 18 | 44165292877 | `status: finished step 18/18` · `answered 18 step(s)` · `found /Users/vmuser/xcode-expand/Xcode.app` |
| windows/11 install | 16 | 44117639979 | `status: finished step 16/16` · `answered 16 step(s)` |
| windows/11 ssh | 31 | 44123555530 | `status: finished step 31/31` · `answered 31 step(s)` |
| windows/11 vm-configure | 30 | 44127574005 | `status: finished step 30/30` · `answered 30 step(s)` |

The three install legs produced systems: ubuntu 5.0 GiB, freebsd 2.3 GiB, macos 27.8 GiB, windows
9.6 GiB. The ssh legs answered on port 2222 (`ssh answers on port 2222 after 0s..34s`). The
vm-configure legs proved the host share both ways (`a file written on the host is readable in the
guest - the share is real`).

## The two that do not, and what the failure is

### macos/15 sequoia — its input cannot be produced on this runner (ACCEPTED NON-PASS)

Sequoia drives the FIRST-BOOT Setup Assistant, so it needs a disk that is installed but has not
finished its first boot. Both candidates fail, and the log says why:

```
boot_disk=macos15      read screen: 1 word(s): support.apple.com/mac/startup   <- OpenCore: no bootable system
boot_disk=macos15-ssh  sign in to apple account - when: 0 word(s):
                       skipped: the screen does not say "sign in to your apple account"
```

`macos15` will not start; `macos15-ssh` has already finished the wizard, so it lands outside it.

UPSTREAM'S `macos.yml` DISPATCHES ONLY `setup`, `ssh`, `vm-configure` AND `dev` - `sequoia.yaml` has
never been run by anyone, in any branch. Its wizard logic IS covered and passing in
`macos/15/steps/ssh.walk` (`answer the wizard until the desktop appears`, 32/32), which handles all
eighteen panes.

THIS IS RECORDED AS AN ACCEPTED NON-PASS rather than fixed: the file needs an input this run
environment cannot produce, and the behaviour it tests is already exercised by a file that passes.

### windows/11 dev — an install download aborts, and WHICH one changes between runs

```
mine      44132311724  failed step  8/22 "install the dotnet sdk"  the script exited 2147500036
upstream  133739714140 failed step  8/22 "install the dotnet sdk"  the script exited 2147500036
mine      44162112013  failed step 10/22 "install python"          the script exited 2147500036
                       ...and in the SAME run, two steps earlier:
                       install the dotnet sdk -> 0
```

THE SAME FILE, THE SAME RUNNER, THE SAME SCRIPT - and `install the dotnet sdk` returned 0 in the
second run while `install python` failed in exactly the way dotnet had before. `2147500036` is
`0x80004004`, "operation aborted", raised while downloading an installer from the vendor. The
walkthrough pulls three large files (Xcode 15 GiB, the .NET SDK, the Python package) from the public
internet, and any one of them being cut off fails a 22-step leg.

That is a property of the network this runs on, not of the file: the evidence is that the failing
step MOVED.

## What the migration itself needed

Converting the files was mechanical and is proved by round trip
(`TestEveryRealWalkthroughRoundTripsThroughWalk`: 57 walkthroughs, documents equal). RUNNING them found
six defects that no conversion test can see:

1. **`loop:` handed a `see:` mapping to the `ocr:` parser** - every loop in every file failed.
2. **`see:` and `ocr:` are two vocabularies**, not two spellings; canonicalising the key lost `of:`.
3. **`readWords` divided a pixel rect by the FRAME** instead of by `rect_at:`, so a 1920x1080 rect
   asked for a crop outside a 640x480 recovery frame. Fixed three times because each check was made
   one layer below the one that was broken.
4. **`TestALoopAssemblesItsPartsWithTheirKeys` asserted on its own copy of the shape** - it passed
   while every loop was broken. It now calls `loopParts`.
5. **`adb_connect` was missing from the list of waiting keys**, so the package's tests spent 320 of
   their 331 seconds waiting for an adbd that no fake driver has.
6. **`requires: [qga]` is a passive key** - it records a capability, it does not probe one, so a step
   that looked guarded could time out on the thing it claimed to check.

And four that were properties of walkthroughs nobody had ever run: `write: demo_script` is not a kind
this build writes; a recording shorter than the sampler's tick captures nothing (`ssh: sleep 20` is
what fixed it - a host-side `settle:` cannot); `share_mount:` does not mount, so `~/shared` never
exists; and `shutdown: guest_agent` after a reboot asks an agent the reboot did not restart.
