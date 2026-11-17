# The Linux install walkthroughs

Seven distributions install unattended from these files, and every one of them produces an **ext4**
root. This document is the index: what each walk does, where the filesystem decision is made, and
the evidence that the disk that comes out is ext4.

The other five directories (`fedora-workstation`, `opensuse-leap-16.1`, `popos`, `mx`, `nixos`) are
NOT here. They have install files and they have not been proven to produce ext4 - `fedora-workstation`
and `opensuse-leap-16.1` are measured to produce **btrfs** and their installers offer no filesystem
selector (see "The two that cannot reach ext4" below).

## The rule every file follows

A step does ONE thing. `boot:` and the wait for a desktop are separate steps; a loop is `loop:` with
`do:`/`ocr:`/`check:`/`until:`; an OCR read is a step of its own with `region_at` naming the frame
size it was measured on. Nothing here downloads, and nothing here uses `kubectl`, NodePort or `python3`
inside the guest.

**A `check:` compares WHOLE TOKENS.** `says` builds a set of the reading's words and asks whether each
wanted entry is in it, so an entry with a space in it (`Select storage`) can never match. Every entry
in these files is one word. `TestNoCheckNamesAPhrase` enforces it.

**A `loop:` must name what ends it.** `until: {fact: <name>}` is not optional, and
`TestEveryLoopSaysWhatEndsIt` fails on a loop that omits it - a loop without one ends only when the
stage times out, and that is measured to have cost a whole stage.

## The seven

| Distribution | Steps | Installer | Where ext4 is decided |
|---|---|---|---|
| kubuntu | 38 | Calamares | partitions page, `ext4` chosen from the combo (default is ext4) |
| ubuntu | 44 | subiquity (Flutter) | `Disk setup` → `Erase disk`, then the Encryption page names ext4 |
| cachyos | 54 | Calamares | partitions page, combo CHANGED from btrfs to ext4 |
| endeavouros | 50 | Calamares | partitions page, default already ext4, read from the summary |
| manjaro | 32 | Calamares | partitions page, combo CHANGED from btrfs to ext4 |
| mint | 31 | Ubiquity | the confirm dialog: `partition #2 ... as ext4` |
| zorin | 32 | Ubiquity | the confirm dialog: `partition #2 ... as ext4` |

### The three installer families behave differently

- **Calamares** (kubuntu, cachyos, endeavouros, manjaro) draws its footer at the bottom of the PAGE
  CONTENT, so `Next` is **not at one height**: measured 751 on Welcome, 791 on Location, 834 on
  Keyboard, 793 on Partitions/Desktop/Packages/Users, 872 on Summary/Install. A shared coordinate
  clicks a different control on each page.
- **Ubiquity** (mint, zorin) has **no filesystem selector**. The only evidence it gives is the
  confirmation dialog's own words, which is why those two files read it there.
- **subiquity** (ubuntu) needed its page list corrected: the real sequence is
  `Accessibility → Keyboard → Internet → Update available → Type of installation → Applications →
  Optimise your computer → Disk setup → Encryption → Create your account`. The file had
  `try or install` and `proprietary software`, which are not pages this build has.

### cachyos and manjaro default to btrfs

Both Calamares builds open the partitions page with `btrfs` in the combo. Both files change it
explicitly, and both then read the summary, which prints the filesystem in words:

```
cachyos    choose ext4 -> summary names ext4
manjaro    choose ext4 -> the root partition is ext4
```

The summary on cachyos was measured reading `124.00 GiB Btrfs` when the combo change did not take,
and `127.50 GiB ext4` when it did - so the summary reading is a real second opinion and not a
restatement of the click.

## The ext4 evidence

Read back from the finished disks with a superblock reader (`magic 0xEF53`, plus `EXTENTS` and
`64BIT` in `s_incompat`), not from the installer's own claim:

| Distribution | Partition | Magic | incompat | ext4 |
|---|---|---|---|---|
| kubuntu | `kubuntu_2604` | `0xef53` | `0x000022c2` | yes |
| ubuntu | (unnamed) | `0xef53` | `0x000022c6` | yes |
| cachyos | `root` | `0xef53` | `0x000022c6` | yes |
| endeavouros | `endeavouros` | `0xef53` | `0x000022c2` | yes |
| manjaro | (root) | `0xef53` | `0x000022c2` | yes |
| mint | (unnamed) | `0xef53` | `0x000002c6` | yes |
| zorin | (root) | `0xef53` | `0x000002c6` | yes |

`0x22c6` and `0x22c2` are ext4 with `EXTENTS` and `64BIT`; `0x02c6` is the same set for an ext4
written by a 32-bit feature mask. Every row is `EXTENTS=True 64BIT=True`.

The ESP beside the root is `FAT32` and reads `NOT ext4`, which is what it should be.

## Per-distribution notes that are not obvious

### kubuntu

Boots to a KDE **launcher on a full-bleed gradient** - no panel - so `desktop: linux/kubuntu` is a
saturation test (launcher 96.9, Calamares pages 4.1..6.2), not a panel test.

### ubuntu

The new installer's Advanced options offer **LVM and ZFS only**; there is no ext4/btrfs selector,
which means the default partition is ext4 and the file does not have to choose it. The `Encryption
and file system` page is what the file settles on, and the summary line reads
`vda2 ... Created and formatted as ext4 used for /`.

### cachyos

Its panel is **eight rows tall** (y=1072..1080), which the shared band average dilutes - hence
`linux/kde`, whose second reading looks for a brightness step in the last 18 rows.

The language combo is real: two runs of the same file produced an English installer and then a
Chinese one, because the list is opened by a click and a missed click sends the keys to the page.
The step is therefore a RETRY, and its verdict is the welcome page's own body
(`program ... questions`) - the installer's sidebar is in English whatever language the wizard is in,
so a page NAME could never prove it.

### endeavouros

Its footer sits one row lower than the other Calamares builds: `Next` at (1410, 878).
The two checkboxes on the users page are **106 pixels apart**, not 51: the root-password section
appears only while `Reuse user password as root password` is clear, which is how the page arrives.
A missed tick leaves `Next` GREY and the clicks after it do nothing, so the tick is a retry whose
verdict is the section's disappearance (`Repeat` leaving the page).

### manjaro

`Office Suite` must be chosen explicitly: `No Office Suite` is highlighted but `Next` is grey.

### mint

Four things had to be measured rather than guessed:

1. **The medium stops at its own GRUB menu** and does not count down. The menu is read first, then
   answered with one `ret` - a key pressed at a screen that is not that menu would go somewhere else.
2. **The desktop icon is click-to-select**, and the launcher opens on `Return`. `double:` was measured
   doing nothing three times on a coordinate that was verified correct by locating the disc's pixels
   (centroid (114,52), bbox x=69..164 y=32..91).
3. **The finish is read from the sentence under the title.** The dialog says `Installation Complete`,
   and OCR returns the title as `oon`:

	finished.txt: "Install Linux Mint oon but until you restart the computer, any changes you make"

   so a check on `Complete` could never pass with the dialog plainly on screen. The reading is
   `restart`, from the longest line on the dialog, which OCR reads cleanly.
4. **It used to wait on the clock.** The step was `wait: 2400` - a fixed forty minutes - sitting where
   a verdict belongs. MEASURED: the disk stopped growing at 12519 MiB with the dialog up and the step
   then sat there for the remaining half hour. It is a loop on the dialog's words now.

And its panel is the **same colour as its wallpaper** on the stock dark theme, so `IsLinuxDesktop`
sees no brightness difference at all (`BrightnessDrop=0`). `IsCinnamonDesktop` therefore looks for
the panel's own EDGE - a row flat across the full width in the bottom quarter with content below it -
which measures `mad=0.0` on the border against `mad=8.3` on the wallpaper beside it.

**A CI RUN READS THE FILE AT ITS OWN COMMIT, NOT THE WORKING TREE.** Two mint legs were dispatched
minutes after a fix and failed on the version before it: run 44058701996 was created at 09:46 against
`728f66a` and failed reading `Complete`, while the fix changing it to `restart` landed as `325d792` at
10:17. The re-dispatch at 10:45 (44067988882, sha `325d792`) passed. When a fix has been pushed,
check `headSha` before reading a failure as a failure of the fix.

### zorin

Same Ubiquity family as mint, so the ext4 evidence is the confirmation dialog's wording rather than a
selector.

## Running one

`easyvm create` keeps the machine up (a sandbox); `easyvm run` runs every step and then stops the
machine. A local check of a file needs no machine at all:

```
easyvm check linux/cachyos/steps/install.yaml
easyvm list  linux/cachyos/steps/install.yaml
```

The CI legs are `linux/<name>-install` in each `.github/workflows/<name>.yml`, and each one publishes
`linux-<name>.qcow2`. The ssh and vm legs that follow are NOT covered here.

## Where each one was proven

All seven have been run to completion on a workstation AND their install leg has passed on CI:

| Distribution | Steps | Local | CI install leg |
|---|---|---|---|
| kubuntu | 38 | 38/38 | run 43991307005 `success` |
| ubuntu | 44 | 29/29 at the time | run 43991118842 `success` |
| cachyos | 54 | 54/54 | run 44020080865 `success` |
| endeavouros | 50 | 50/50 | run 44028288557 `success` |
| manjaro | 32 | 32/32 | run 43991290852 `success` |
| mint | 31 | 31/31 | run 44067988882 `success` |
| zorin | 32 | 32/32 | run 43991222285 `success` |

A whole `<name>.yml` run still shows `failure` for most of them, because the ssh and vm legs that
follow the install leg are NOT in scope and are not passing. Read the install leg, not the run.

## The two that cannot reach ext4

`fedora-workstation` and `opensuse-leap-16.1` are measured to install **btrfs** by default:

```
fedora 45, Review and install:
  vda1   629 MB  format as efi              /boot/efi
  vda2  2.15 GB  format as ext4             /boot
  vda3   135 GB  format as btrfs subvolume  /        <- root is btrfs
  vda3   135 GB  format as btrfs subvolume  /home
```

Fedora's `Storage configuration` page offers **encryption only** - no filesystem selector. The only
route to ext4 in that installer is `Advanced → Launch storage editor` (blivet-gui), whose popup menus
are closed by the pointer move that precedes every click here, or a kickstart answer file, which this
build cannot attach to a Linux boot. openSUSE has the same shape. Until one of those exists, these two
are out of scope rather than quietly passing.
