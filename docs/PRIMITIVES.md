# easyvm walkthrough — the primitives

Generated from the code and the 57 real walkthroughs. Every count in this table is
measured, not estimated: `internal/step/audit_test.go` fails if a documented key has
no implementation, if an implemented key is missing from this table, or if a key
claimed as "merged" still appears in a file.

Three verbs, and everything else is a modifier on the step:

```
do:    change the machine
see:   measure the screen      -> binds NAMED VALUES
want:  decide from those values -> binds a BOOLEAN
```

**A measurement is code, a verdict is data.** `see:` is the only place pixels are
touched; `want:` never reads the screen. That is what makes a new screen shape a
line in a file rather than a rebuild.

---

## 1. `do:` — change the machine

| key | value | what it does | uses |
|---|---|---|---|
| `boot` | mapping | start a machine this file owns: `{os, cpu, memory_mb, disk, iso, answer_cd, driver_cd, from, recovery}` | 56 |
| `reboot` | bool | reset the running machine | 2 |
| `shutdown` | `ssh\|kill\|acpi\|guest_agent` | stop the machine by a named method | 54 |
| `disk` | mapping | the file a `boot:` starts from: `{path, gb, gpt, esp_mib, from, copy_of, overlay_of}` | 29 |
| `record` | path / `false` | start or stop the screen recording | 100 |
| `compress` | path | flatten the overlay into an artifact | 49 |
| `esp_fallback` | disk | copy a disk's EFI loader to `\EFI\BOOT` and repair its search | 1 |
| `click` | `[x, y]` | one click | 299 |
| `double` | `[x, y]` | one double-click, moving once | 6 |
| `at_button` | `[x, y]` | click by name-free placement inside a window region: `{at, within, fallback}` | 9 |
| `at_color` | `[r,g,b]` | click the largest blob of one colour: `{tolerance, within}` | 1 |
| `key` | qcode / `ctrl+alt+t` | one keystroke or chord | 170 |
| `type` | text | type at the focus; with `at:` clicks first | 124 |
| `write` | kind | write one host-side file for a later `scp:` | 17 |
| `run` | shell text | type a command into the guest's own terminal, line by line | 78 |
| `ssh` | shell text | one command over ssh; the exit status is the verdict | 266 |
| `scp` | `[local, remote]` | one copy, size-checked on both sides | 25 |
| `script` | kind | write a body on the host, copy it in, run it by path: `{shell, name}` | 16 |
| `adb` | shell text | one command over the Android Debug Bridge | 16 |
| `require_ssh` | bool | wait for sshd before the steps that use it | 40 |
| `share_serve` | bool | start smbd: `{dir}` | 16 |
| `share_mount` | bool | the guest mounts the host share: `{family}` | 16 |

## 2. `see:` — measure the screen, bind numbers

| key | value | what it binds | uses |
|---|---|---|---|
| `see` | `{name, rect, of, scale}` | `name.mean`, `.spread`, `.sat`, `.flat_rows`, `.contrast`, `.min`, `.max`, or `name.words` for `of: [text]` | new |
| `ocr` | `{name, rect, scale}` | `name` -> the words read from that rect | 118 |
| `desktop` | `linux\|kde\|gnome\|cinnamon\|xfce\|windows\|macos\|android` | waits until that desktop's measurement passes | 22 |
| `screenshot` | path | saves a frame for a post-mortem; binds nothing | 3 |

`rect:` is a **fraction of the frame**, never a pixel pair: `middle`, `top 4.5%`,
`bottom 40px`, or `[x0, y0, x1, y1]`. A fraction cannot be measured at the wrong
size, which is the failure the files record as *"a coordinate measured at 1920x1080
but the framebuffer is 640x480"*.

`region_at:` IS GONE. It was written 42 times and was always the header's own
`resolution:`. A key that restates a constant is not information.

## 3. `want:` — decide, bind a boolean

| key | value | what it does | uses |
|---|---|---|---|
| `check` | `{name, text?, colour?, unless?, any_of?, region?}` | ONE capture, every criterion against it, one boolean | 119 |
| `want` | `{name, all\|any\|not}` | compares `see:` numbers and binds a boolean | new |
| `if` | fact | `then:` / `else:` branches, as `{then: [...], else: [...]}` | 19 |
| `loop` | `{max, do, until}` | repeat `do:` until the fact is true, up to `max` | 79 |

`check:` is the merged primitive. One capture, any number of criteria:

```yaml
- check:
    name: page
    region: middle
    text:
      - contains: [erase, disk]        # exact words, case/punctuation folded
      - contains: [ext4], fuzzy: 1     # within ONE edit per word
      - like: "install now"            # whole region ~ this sentence
        ratio: 0.85
      - not_contains: [installing]
    colour:
      - rect: bottom 4.5%
        mean:      {between: [20, 60]}
        spread:    {less_than: 42}
        flat_rows: {at_least: 3}
        vs: {rect: middle, mean: {greater_than: 40}}
    any_of:
      - colour: [{rect: top 4.5%, flat_rows: {at_least: 3}}]
    unless: [welcome, setup]
```

**Why one step and not `see:`+`want:` for this**: every `Driver.Frame()` is a real
screendump. Two steps read two different moments, and on a repainting pane the
numbers then describe no frame that ever existed. The single capture is a
correctness requirement.

**Three text comparisons, because they answer three questions** (measured):

```
contains: [erase, disk]   on 'Erase disk and install Ubuntu'   -> yes
like: "erase disk"        on 'Erase disk and install Ubuntu'    -> 0.345  NO
like: "install now"       on 'insall now'                       -> 0.909  yes
contains: [ext4], fuzzy:1 on 'ext'                              -> yes (distance 1)
```

`fuzzy:` is an **edit distance per word**, default 0, capped at 2. A ratio is the
wrong unit for a short word: `disk`→`dsk` is one glyph and scores 0.750, so any
ratio threshold refuses a pane's own button label. That is why `fuzzy:` is a
distance and `like:` is the only ratio.

## 4. Modifiers — nine, and each names a question

| key | value | the question it answers | uses |
|---|---|---|---|
| `name` | text | what does the log call this? | 1526 |
| `when` | fact / words | does this step apply at all? | 16 |
| `optional` | bool | may it fail without failing the walk? | 30 |
| `requires` | `[ssh, qga, share, ...]` | are its capabilities there? | 21 |
| `timeout` | seconds | how long may the whole step take? | 533 |
| `settle` | seconds | how long to wait with no fact to wait on (absorbs `wait`+`wait_after`) | 818 |
| `poll` | seconds | how often does a wait look? | 22 |
| `keep_alive` | bool | keep the screen awake while waiting? | 25 |
| `type_gap` | seconds | how fast to type? | 16 |

| `terminal_wait` | seconds | open a terminal first, then wait this long | 68 |
| `clear` | bool | select-all and clear the field before typing | 17 |
| `modifier` | `ctrl\|meta_l` | the select-all modifier for this family | 4 |

`settle:` replaces both `wait:` and `wait_after:` — the same key with two names,
818 uses between them. `wait_after` was a timed GUESS in one step of three, and a
`settle:` followed by a `check:` of the same region is reported by the validator,
because that guess is the bug class that failed cachyos and endeavouros on CI.

## 5. Subkeys — written INSIDE another key, never on a step

| key | parent | value | uses |
|---|---|---|---|
| `os` | `boot` | `linux`, `windows`, `macos`, `freebsd`, `android` | 56 |
| `cpu` | `boot` | `N` vCPUs (always `-cpu Skylake-Client-v4`) | 0 |
| `memory_mb` | `boot` | MiB of RAM | 7 |
| `iso` | `boot` | the installer medium | 56 |
| `recovery` | `boot` | macOS's recovery image | 0 |
| `answer_cd`, `driver_cd` | `boot` | Windows' extra media | 2 |
| `disk_gb`, `disk_gpt`, `disk_esp_mib` | `disk` | the target's size and layout | 18 |
| `disk_settle`, `disk_min_mb` | `disk_settle` | wait for the disk to stop growing | 12 |
| `ssh_probe`, `ssh_after_reboot` | `require_ssh` | how to probe, and whether to wait out the old daemon | 11 |
| `share_family` | `share_mount` | `linux`, `windows`, `macos`, `bsd` | 16 |
| `esp_from` | `esp_fallback` | the loader directory inside the ESP | 1 |
| `adb_connect`, `adb_privileged` | `adb` | connection and privilege | 2 |
| `rect`, `of` | `see` | where to measure, what to bind | new |
| `text`, `colour`, `unless`, `any_of`, `region` | `check` | the criteria | new |
| `colour` | `check` | the pixel-criterion list | new |
| `any_of` | `check` | groups where ONE must pass | new |
| `unless_any` | `check` | the alternatives form of `unless` | 2 |
| `from` | `check` | which reading the older single-comparison form compares | 42 |
| `all`, `any`, `not` | `want` | how the comparisons combine | new |
| `mode`, `words`, `without`, `text`, `pattern`, `range` | `check` | the older single-comparison form | retained |
| `do`, `until`, `max` | `loop` | the action, the stop test, the ceiling | 79 |
| `then`, `else` | `if` | the branches | 19 |
| `fact` | `until` | which boolean ends a loop | 79 |
| `asset`, `build`, `generate`, `template` | `fetch`, `build` | host-side asset production | 5 |
| `release` | `os` | the macOS release, for `os: macos-15` | 4 |
| `until_console`, `blind_after` | step | wait on serial output | 2 |

## 6. File header — not step keys at all


| key | value | what it says |
|---|---|---|
| `resolution` | `[w, h]` | the size every fraction and coordinate was measured at |
| `vars` | mapping | `{from: args.x, default: y}` |
| `ssh_user`, `ssh_port` | | the account the later stages log in as |
| `ends`, `waits_for` | list | the old `panes:` model; no current file uses it |

---

## The retirement plan — 47 keys, 1014 uses

**THESE KEYS STILL EXIST AND STILL WORK.** This is a target, not a description of
today's code, and saying otherwise would be the one thing a design document must
never do. The plan is per-file and non-breaking: the loader keeps accepting every
old name and reports each use, so a file migrates when it is next touched and no
walkthrough stops loading in the meantime.

The `uses` column is measured from the 57 files, and `internal/step/audit_test.go`
fails if a key listed here as ALREADY GONE is still used — so this table cannot
silently become a lie in either direction.

| step key | uses | becomes |
|---|---|---|
| `wait_after` | 627 | `settle:` |
| `wait` | 118 | `settle:` |
| `region` | 42 | `check: {rect: ...}` |
| `region_at` | 42 | *(gone — `rect:` is a fraction)* |
| `scale` | 42 | `check: {scale: N}` |
| `require` | 22 | `requires:` + the file header |
| `then` / `else` | 19 | `if: {then: ..., else: ...}` |
| `script_shell` / `script_name` | 32 | `script: {shell:, name:}` |
| `family` | 15 | `os:` |
| `at_bottom` | 8 | `at_button:` |
| `skip_when_unfinished` | 5 | `if: finished` |
| `color_tolerance` / `color_region` | 2 | `at_color: {tolerance:, within:}` |
| `at_fallback` | 1 | `at_button: {fallback:}` |
| `once` | 1 | `loop: {max: 1}` |
| `answer_cd` / `driver_cd` | 2 | `boot: {answer_cd:, driver_cd:}` |
| `mem_mb` | 0 | `memory_mb:` — **MIGRATED** |
| `cpus` | 0 | `cpu:` |
| `until`, `until_any`, `expect`, `expect_any` | 81 | `loop:` + `check:` |
| `repeat_until` | 0 | `loop:` |
| `wait_for`, `wait_for_change`, `until_dark` | 0 | `check: {colour: ...}` |
| `change_settle`, `change_timeout` | 0 | `settle:`, `timeout:` |
| `desktop_family` | 0 | `desktop:` |
| `boot_from`, `disk_from`, `disk_copy`, `disk_overlay`, `snapshot` | 0 | the mapping forms of `boot:`/`disk:` |
| `graceful` | 0 | `shutdown: {method:}` |
| `press`, `sweep` | 0 | `at_button:`, `loop: {do: [{click: ...}]}` |
| `demo_script` | 0 | `write:` |
| `host_log`, `line_gap`, `adb_serial`, `share_dir`, `end`, `stuck_after`, `as` | 0 | *(dead — no successor needed)* |

**Seven keys were declared, documented and used ZERO times** — `until`,
`until_any`, `repeat_until`, `wait_for`, `change_settle`, `change_timeout`,
`desktop_family`. A vocabulary that cannot retire a key grows forever, so the
audit test now reports a dead key as a failure.

---

## Counts

```
before   128 declared   (85 top-level, 43 subkeys)   43 top-level dead
after    41 keys        (21 do, 4 see, 4 want, 9 modifiers, 3 header)
```

The rule that keeps it from growing back: **a key is a verb or a question, never a
goal.** `dismiss:`, `copy_in:`, `select_disk:` and `apple_account:` were removed
for this reason already — each hid a loop, a click sequence or a tolerance in Go,
so the file said one thing and the code did three.
