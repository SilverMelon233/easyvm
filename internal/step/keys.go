// Package step defines the vocabulary a walkthrough YAML file may use.
//
// THIS FILE IS THE CONTRACT. The Python implementation this replaces accepted 85 distinct step
// keys, and every one of them was added because a specific CI run needed it. A key that is
// accepted here but does nothing is worse than a key that is rejected: the file loads, the step
// reports success, and the run proves nothing. So every key below is listed once, with the reason
// it exists, and the validator refuses anything not on the list.
package step

import "fmt"

// Key is one accepted field name in a step mapping.
type Key struct {
	Name string
	// Summary is what the key DOES, in the imperative, because a reader looking at a log needs to
	// know what a step claimed to do.
	Summary string
	// Kind says how the value is read and checked.
	Kind Kind
	// Since names the CI run or the measurement that put this key here, when there is one. A key
	// nobody can explain the origin of is a key nobody dares remove.
	Since string
}

// Kind is how a key's value is interpreted. It drives validation, not execution: the executor
// reads the same mapping, but a typo like `until: "Continue"` meaning a phrase versus
// `until: 30` meaning seconds has to be caught before a guest is started.
type Kind int

const (
	// KindString is free text.
	KindString Kind = iota
	// KindBool is true/false; YAML 1.1 quoting quirks make `on` a bool, which has bitten this
	// project before, so booleans are accepted only as real YAML booleans.
	KindBool
	// KindInt is a whole number of seconds, MiB, or a count.
	KindInt
	// KindFloat is seconds with a fractional part.
	KindFloat
	// KindPair is [x, y].
	KindPair
	// KindRect is [x0, y0, x1, y1].
	KindRect
	// KindWords is a phrase, or a list of words that must all be present.
	KindWords
	// KindWordSets is a list of phrases, any one of which satisfies the key.
	KindWordSets
	// KindMapping is a nested mapping (the `do:` list, `boot:` options).
	KindMapping
	// KindList is a sequence of mappings.
	KindList
	// KindScript is a multi-line body handed to a shell ON THE GUEST. Not our code: the disk
	// under test is what runs it, which is why it stays text.
	KindScript
	// KindAny is a key with MORE THAN ONE ACCEPTED SHAPE, and it exists because several of them
	// genuinely do: `boot: true` is a flag and `boot: {os: linux}` is the same key with options;
	// `disk: disk.qcow2` is a name and the mapping form was removed but old files still parse it.
	//
	// IT IS NOT A HOLE. A key lands here only after its own executor is read and found to take more
	// than one shape, and the shapes it takes are still checked by that executor when it runs. The
	// alternative - declaring one Kind and refusing the other form - would refuse files this
	// repository has been running for months.
	KindAny
)

func (k Kind) String() string {
	switch k {
	case KindString:
		return "string"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindPair:
		return "[x, y]"
	case KindRect:
		return "[x0, y0, x1, y1]"
	case KindWords:
		return "words"
	case KindWordSets:
		return "[words, ...]"
	case KindMapping:
		return "mapping"
	case KindList:
		return "[mapping, ...]"
	case KindScript:
		return "script"
	case KindAny:
		return "any"
	}
	return "?"
}

// Keys is every accepted step key. Grouped by what the step does to the machine, because that is
// the order a reader needs them in: a step opens a screen, acts on it, waits, and then says
// whether it worked. The groups are documentation; the map below is enforcement.
var Keys = map[string]Key{
	// --- identity and gating -----------------------------------------------------------
	"name": {Name: "name", Kind: KindString,
		Summary: "what the log calls this step; required"},
	"optional": {Name: "optional", Kind: KindBool,
		Summary: "a step that may fail without failing the stage - only for a check whose " +
			"NEGATIVE result is information, never for a verdict"},
	// `needs:` IS THE NAME AND `require:` WAS THE TRAP. `require:` named the FILES a stage needs on
	// disk; `requires:` names the CAPABILITIES the machine must answer. One letter apart, two
	// completely different assertions, and 43 uses between them - a reader had to count the `s`.
	// MEASURED: `require:` 22, `requires:` 21.
	"needs": {Name: "needs", Kind: KindList,
		Summary: "files that must be on disk before this stage runs; the first step of every stage " +
			"names its medium here so a missing input fails in the first second rather than forty " +
			"minutes in"},
	"requires": {Name: "requires", Kind: KindList,
		Summary: "capabilities that must answer before the step runs (ssh, qga, share, " +
			"network, overlay); missing is a StepError, never a quiet fallback"},
	// `when_not:` IS THE NEGATIVE GATE AND IT REPLACES `unless:`.
	//
	// `when:` AND `unless:` WERE ONE GATE WITH A NEGATION between them - "run if the screen says X"
	// and "run if it does NOT say X". Two names for one idea, 16 uses and 10, and a reader had to
	// remember which was which. `when:` / `when_not:` says it in the name.
	"when_not": {Name: "when_not", Kind: KindWords,
		Summary: "the negative gate: run this step only if the screen does NOT show these words"},
	"when_not_any": {Name: "when_not_any", Kind: KindWordSets,
		Summary: "the alternatives form of `when_not:`: the step runs only if the screen shows " +
			"NONE of these word-sets"},
	"skip_when_unfinished": {Name: "skip_when_unfinished", Kind: KindBool,
		Summary: "in a panes epilogue: run only if the walk finished"},

	// --- the machine's own lifecycle ----------------------------------------------------
	// `boot: true` IS A FLAG AND `boot: {os: linux}` IS THE SAME KEY WITH OPTIONS - both are in the
	// tree, so the Kind here is `any` and `doBoot` is what reads the two shapes.
	"boot": {Name: "boot", Kind: KindAny,
		Summary: "start a machine this file owns; its options include os, cpu, mem_mb, cpus, " +
			"share, iso, disk, tpm"},
	"os": {Name: "os", Kind: KindString,
		Summary: "which family the machine is: windows, macos, linux, android"},
	"iso": {Name: "iso", Kind: KindString,
		Summary: "an installer ISO. Windows and Linux boot it directly; macOS has none and is " +
			"refused by name if given one"},
	"recovery": {Name: "recovery", Kind: KindString,
		Summary: "macOS's recovery image, attached as removable media. It is the install medium " +
			"AND the source of the APFS driver OpenCore needs, so it is attached on every macOS " +
			"boot"},
	"answer_cd": {Name: "answer_cd", Kind: KindString,
		Summary: "a second CD-ROM carrying Windows' answer file. Setup finds it nowhere on the " +
			"install ISO"},
	"driver_cd": {Name: "driver_cd", Kind: KindString,
		Summary: "a third CD-ROM carrying the virtio drivers, for the installed system to pick up " +
			"network and ballooning"},
	"cpu": {Name: "cpu", Kind: KindString,
		Summary: "the guest's -cpu model when the disk's packages need a fixed one; empty is " +
			"-cpu host"},
	// --- how big the guest is ------------------------------------------------------------
	//
	// THESE ARE ON THE BOOT STEP RATHER THAN ONLY IN A `machine:` BLOCK, because the boot step is
	// what reaches qemu. A `machine:` block's own `memory_mb:` is copied onto the boot step when the
	// machine is turned back into steps, so both spellings end up here.
	//
	// THE SIZE WAS READ AND DROPPED FOR ONE RELEASE: `machineFromBlock` stored it on the spec,
	// nothing put it on a step, `doBoot` had nothing to pass, and every guest ran with the 4096 MiB
	// default whatever the file said - which is why ghostbsd's installer ran out of memdisk.
	"memory_mb": {Name: "memory_mb", Kind: KindInt,
		Summary: "the guest's RAM in MiB; 4096 when not said. A guest that installs from a " +
			"memdisk needs more than the default"},
	"cpus": {Name: "cpus", Kind: KindInt,
		Summary: "how many vCPUs the guest gets; 4 when not said"},
	// `mem_mb:` WAS THE OLDER SPELLING OF `memory_mb:` AND IS GONE.
	//
	// It was kept "so a file that has not been renamed runs", and no file ever used it: measured
	// zero uses across the 57 walkthroughs. A second spelling that nothing spells is not a
	// compatibility shim, it is a key a reader has to guess between - and `internal/step/audit_test`
	// now reports any key this package declares that no file uses.
	"kernel": {Name: "kernel", Kind: KindString,
		Summary: "with `microvm:`: the kernel qemu hands the guest, because a microvm has no " +
			"firmware to read a boot sector"},
	"initrd": {Name: "initrd", Kind: KindString,
		Summary: "with `microvm:`: the initramfs beside the kernel"},
	"microvm": {Name: "microvm", Kind: KindBool,
		Summary: "with `boot:`: the smallest machine that can boot a modern Linux kernel - no " +
			"firmware, no devices the guest does not need, a direct kernel hand-off. For a " +
			"walkthrough whose job is to prove the vocabulary drives a guest, not to install a " +
			"distribution through its GUI"},
	"reboot": {Name: "reboot", Kind: KindMapping,
		Summary: "reset the guest and wait for it to come back"},
	"shutdown": {Name: "shutdown", Kind: KindString,
		Summary: "a NAMED method: guest_agent, ssh, acpi, or kill; there is no silent fallback " +
			"between them"},
	"graceful": {Name: "graceful", Kind: KindBool,
		Summary: "ask the guest to shut down rather than killing it"},
	"record": {Name: "record", Kind: KindAny,
		Summary: "start recording to this path (a string) or stop with `false`; the step starts " +
			"its own sampler, because an encoder with no frames writes nothing"},
	"compress": {Name: "compress", Kind: KindString,
		Summary: "flatten the overlay into this artifact; the path is relative to the repository " +
			"root, not to the work directory"},
	"snapshot": {Name: "snapshot", Kind: KindString,
		Summary: "write the running machine's whole state - memory, devices and UEFI variables - to " +
			"this file. It is a migration stream, NOT `savevm`: a UEFI guest's variable store is a " +
			"pflash device and `savevm` refuses every writable device that cannot hold a snapshot"},
	"boot_from": {Name: "boot_from", Kind: KindString,
		Summary: "start the machine from a `snapshot:` file instead of running a boot loader. The " +
			"cpu, machine type and device set must match the ones the snapshot was taken with"},

	// --- the disk ----------------------------------------------------------------------
	// `disk: disk.qcow2` IS THE FORM EVERY FILE USES; the {from, copy} mapping is the older one and is
	// refused by name when `doDisk` sees it, which is a better error than a Kind mismatch here.
	//
	// THE SAME GOES FOR SIX MORE, AND EACH IS A KEY THAT GENUINELY CARRIES MORE THAN ONE SHAPE:
	//
	//	desktop       `linux/kde` is a name, `{os: linux, desktop: gnome}` is the same wait with options
	//	record        `install.mp4` starts one, `false` stops it - a bool is a STATEMENT here
	//	timeout       a number of seconds, or `'{{ boot_timeout }}'` resolved before this is reached
	//	script        a body string, or `{body, shell, name}` - both spellings are in the tree
	//	rect          a named rect (`middle`), a pixel list, or a list of fractions
	//	disk_esp_mib  a number, or a `{{ }}` template
	//
	// THEY WERE ALL DECLARED WITH ONE SHAPE, which is what happens when the Kind is documentation
	// rather than enforcement: nothing read it, so it drifted from what the executors accept. NOW IT IS
	// READ, so each was reconciled against the files rather than the other way round.
	"disk": {Name: "disk", Kind: KindAny,
		Summary: "prepare the file `boot:` starts from a source: {from, copy, overlay}"},
	"disk_from": {Name: "disk_from", Kind: KindString,
		Summary: "the source image a disk: step takes"},
	"disk_copy": {Name: "disk_copy", Kind: KindString,
		Summary: "move or copy the source: move, copy, reflink"},
	"disk_overlay": {Name: "disk_overlay", Kind: KindBool,
		Summary: "run on a throwaway diff over the read-only source"},
	"disk_gb": {Name: "disk_gb", Kind: KindAny,
		Summary: "size of a disk this step creates"},
	// THE PARTITION TABLE, FOR AN INSTALLER THAT EXPECTS ONE TO EXIST.
	//
	// Android-x86's first pane lists the partitions it FINDS; given a blank disk it hands the run to
	// cgdisk, whose seventeen line prompts take `260M` and `EF00` as TYPED TEXT and where one
	// misaligned key silently retypes the ESP as "Linux filesystem".
	"disk_gpt": {Name: "disk_gpt", Kind: KindBool,
		Summary: "partition the created disk: a 260 MiB ESP typed EF00 at LBA 2048, Linux on the rest"},
	"disk_esp_mib": {Name: "disk_esp_mib", Kind: KindAny,
		Summary: "size of that ESP; 260 is what the installer draws as 0.25GB"},
	"disk_settle": {Name: "disk_settle", Kind: KindFloat,
		Summary: "wait until the target qcow2 stops growing for this many quiet seconds"},
	"disk_min_mb": {Name: "disk_min_mb", Kind: KindInt,
		Summary: "with disk_settle: only start waiting once it holds this many allocated MiB"},

	// --- the screen: act ---------------------------------------------------------------
	// `click:` IS WHAT `at:` ALWAYS MEANT, AND THE OLD NAME WAS A PREPOSITION.
	//
	// A step that says `at: [1331, 785]` reads as "at this coordinate" and leaves the verb to be
	// inferred from the key next to it. It is a CLICK - one press at one pixel - and beside `double:`
	// the pair now says what they do:
	//
	//	- name: welcome
	//	  click: [1331, 785]      one press
	//	- name: open the pane
	//	  double: [960, 540]      two presses, with the gap that makes them a double-click
	//
	// NO COMPATIBILITY ENTRY FOR `at:`. The rule in this repository is that a name is renamed in the
	// same commit as its files, and a table of old spellings is a second vocabulary that drifts from
	// the first - the same argument that deleted the `python:` entries.
	"click": {Name: "click", Kind: KindPair,
		Summary: "click this [x, y]. Fixed coordinates, never OCR - a button's position is a " +
			"measured fact about a screen, and a search for it is a guess that moves"},
	"double": {Name: "double", Kind: KindPair,
		Summary: "double-click [x, y]; separate from `click` because the gap between the two " +
			"clicks is what makes it a double-click"},
	"at_button": {Name: "at_button", Kind: KindRect,
		Summary: "[x0, y0, x1, y1] - click the centre of the coloured fill inside this region. " +
			"The ONE lookup this repository allows, for a control whose position moves per pane " +
			"and whose fill is the only one of its kind in that strip"},
	"host_log": {Name: "host_log", Kind: KindString,
		Summary: "print the tail of a HOST-side log - a witness step, not a judge. `smbd` is the " +
			"one this build knows, and it says when there is no such log rather than pretending"},
	"at_fallback": {Name: "at_fallback", Kind: KindPair,
		Summary: "[x, y] - with `at_button`: the measured position to click when the fill is not " +
			"found. For a control that CAN be missing (an icon behind a wallpaper change) rather " +
			"than one that cannot (a footer button whose absence means a step above failed)"},
	// `at_color:` FINDS A CONTROL BY ITS COLOUR AND CLICKS ITS CENTRE, WHICH NEEDS NO GEOMETRY.
	//
	// IT REPLACED `at_window`, whose whole premise was that a control's offset from its window is
	// constant - true, and useless, because finding the window's edge was itself an unreliable
	// measurement. Four attempts at Disk Utility's Erase button (592,495 / 810,376 / 807,367 / 1289,15)
	// were four attempts at the SCAN, not at the button.
	//
	// A COLOUR IS A FACT THE COMPOSITOR DRAWS: macOS paints the green zoom control rgb(40,200,64) on
	// every run, at every window height, in every Space. `at_color: [40, 200, 64]` is the whole
	// instruction, and `color_tolerance:` says how far a pixel may sit from it for antialiasing.
	// `at_color: [40, 200, 64]` IS AN RGB TRIPLE, NOT A RECTANGLE - the Kind said `KindRect` and nothing
	// read it, so the documentation and the executor had disagreed since the key was written.
	"at_color": {Name: "at_color", Kind: KindAny,
		Summary: "[r, g, b] - click the centre of the largest blob of this colour. For a control " +
			"the window draws in a fixed colour, which is how a macOS window button is found without " +
			"knowing where the window is"},
	"color_tolerance": {Name: "color_tolerance", Kind: KindInt,
		Summary: "with `at_color`: how far a pixel may be from the target colour and still count, " +
			"for antialiasing. 45 matches macOS's window buttons"},
	"color_region": {Name: "color_region", Kind: KindRect,
		Summary: "with `at_color`: [x0, y0, x1, y1] to search inside, when the colour could appear " +
			"somewhere else on the screen too"},
	"at_bottom": {Name: "at_bottom", Kind: KindPair,
		Summary: "[x, dy] - click x at dy pixels above the window's bottom edge"},
	"key": {Name: "key", Kind: KindString,
		Summary: "a qemu qcode, or a + combo such as ctrl+alt+t"},
	"type": {Name: "type", Kind: KindString,
		Summary: "text to type; combined with `at` it clicks the field first and clears it"},
	"clear": {Name: "clear", Kind: KindBool,
		Summary: "clear the field before typing (select-all, then backspace)"},
	"modifier": {Name: "modifier", Kind: KindString,
		Summary: "the select-all modifier for this family: ctrl on Linux, meta_l on macOS"},
	"type_gap": {Name: "type_gap", Kind: KindFloat,
		Summary: "seconds between keystrokes; 0.02 suits a POSIX shell, 0.15 a Windows one"},
	"run": {Name: "run", Kind: KindScript,
		Summary: "a shell line typed into the guest's own terminal, one line at a time"},
	"terminal_wait": {Name: "terminal_wait", Kind: KindFloat,
		Summary: "seconds to wait for the terminal after `run` opens it"},
	"line_gap": {Name: "line_gap", Kind: KindFloat,
		Summary: "seconds between the lines of a multi-line `run`"},
	// THERE IS NO `repeat:`. IT WAS A DEAD KEY: fourteen of them across eight files, a vocabulary
	// entry describing "(count, settle) for a footer button pressed the same way N times", and NO
	// DISPATCHER BRANCH ANYWHERE THAT READ IT - so `repeat: [10, 4]` pressed nothing. The vocabulary
	// test that requires every key to have a branch did not catch it because `repeat` was never
	// claimed as executable, only declared.
	//
	// A LOOP IS `loop:`, and a fixed number of presses is a `do:` list. Both are visible in the file.
	"sweep": {Name: "sweep", Kind: KindMapping,
		Summary: "a band of clicks at one x, stopping when the sheet opens"},
	"press": {Name: "press", Kind: KindString,
		Summary: "press a named control rather than coordinates"},
	"change_settle": {Name: "change_settle", Kind: KindFloat,
		Summary: "with wait_for_change: how long the screen must stay unchanged"},
	"change_timeout": {Name: "change_timeout", Kind: KindFloat,
		Summary: "with wait_for_change: how long to wait for a change at all"},

	// --- the screen: read --------------------------------------------------------------
	"until_console": {Name: "until_console", Kind: KindString,
		Summary: "wait for text in the guest's SERIAL output - the firmware's own words, which " +
			"a screenshot cannot see. A Windows install ISO prints its boot prompt there and " +
			"waits five seconds for a key"},
	"blind_after": {Name: "blind_after", Kind: KindInt,
		Summary: "with until_console: go on after this many seconds whether or not the text " +
			"appeared, for media that boot with no prompt at all"},
	"until": {Name: "until", Kind: KindWords,
		Summary: "words that end a wait loop"},
	"until_dark": {Name: "until_dark", Kind: KindBool,
		Summary: "wait until the screen goes black - an installer's own start signal"},
	"wait_for_change": {Name: "wait_for_change", Kind: KindBool,
		Summary: "wait until the screen changes rather than for particular words"},
	"poll": {Name: "poll", Kind: KindFloat,
		Summary: "seconds between looks in an until: loop"},
	"timeout": {Name: "timeout", Kind: KindAny,
		Summary: "seconds the step may run before it is a failure"},
	// `keep_alive:` IS GONE. It was written `true` in all 25 places it appeared and never once
	// `false`, so it was not a switch - it was the behaviour, spelled as an option. A screen that
	// blanks during a boot is normal, and both waits that need to survive it now wake it
	// unconditionally: see `wakeTheScreen` in internal/run.
	"scale": {Name: "scale", Kind: KindInt,
		Summary: "enlargement for a cropped read; small text needs it"},
	"screenshot": {Name: "screenshot", Kind: KindString,
		Summary: "save the screen to this path, for a post-mortem nobody had to ask for"},
	"desktop": {Name: "desktop", Kind: KindAny,
		Summary: "wait until a desktop is on screen; options include family and timeout"},
	"ssh": {Name: "ssh", Kind: KindScript,
		Summary: "a command run over ssh IN the guest; the exit status is the verdict"},
	"require_ssh": {Name: "require_ssh", Kind: KindBool,
		Summary: "wait for sshd before the steps that use it"},
	"ssh_probe": {Name: "ssh_probe", Kind: KindString,
		Summary: "the command that wait runs; `true` is not a Windows command"},
	"ssh_after_reboot": {Name: "ssh_after_reboot", Kind: KindBool,
		Summary: "on a require_ssh: wait out the old daemon first, or the wait passes on the " +
			"process that is about to die"},
	"scp": {Name: "scp", Kind: KindList,
		Summary: "[local-file, remote-path] - copy a script in before running it"},
	"script": {Name: "script", Kind: KindAny,
		Summary: "a body written on the host, copied in, and run by path. The guest's " +
			"interpreter runs it, not ours"},
	"adb": {Name: "adb", Kind: KindScript,
		Summary: "a command run over adb IN the guest; Android's remote access is adbd and " +
			"there is no sshd until a stage installs one"},
	"adb_privileged": {Name: "adb_privileged", Kind: KindBool,
		Summary: "prove the adb shell can change system state - a write that reads back. NOT " +
			"`adb root`: this guest is a `user` build with ro.debuggable=0 and no `su`, and the " +
			"shell it already has owns appops, settings, cmd and setprop"},
	"adb_connect": {Name: "adb_connect", Kind: KindBool,
		Summary: "attach to the guest's adbd on its forwarded port and prove it answers"},
	"adb_serial": {Name: "adb_serial", Kind: KindString,
		Summary: "the serial to use"},

	// --- the host's share ---------------------------------------------------------------
	"share_serve": {Name: "share_serve", Kind: KindBool,
		Summary: "start the host's smbd for a directory - one act, not two"},
	"share_mount": {Name: "share_mount", Kind: KindBool,
		Summary: "the guest mounts what share_serve: is serving"},
	"share_dir": {Name: "share_dir", Kind: KindString,
		Summary: "the host directory to share; default ~/shared"},
	"share_family": {Name: "share_family", Kind: KindString,
		Summary: "which client the guest uses: macos, windows, linux, bsd"},

	// --- panes: the macOS shape ---------------------------------------------------------
	"when": {Name: "when", Kind: KindWords,
		Summary: "a pane: the words on screen that mean this step applies"},
	"do": {Name: "do", Kind: KindList,
		Summary: "a pane's actions, in order, run in one pass"},
	"release": {Name: "release", Kind: KindString,
		Summary: "which OS release is being driven; the Apple Account pane differs by release"},

	// --- host-side files, written or fetched before a copy -------------------------------
	//
	// THESE REPLACE `python:` STEPS, and the split is the same one the Python implementation had:
	// the file is WRITTEN or FETCHED here, the `scp:` after it moves it in, and the `ssh:` after
	// that runs it. A single step that wrote, copied and ran reported "the launcher install
	// exited 1" and named neither the file nor the action.
	"write": {Name: "write", Kind: KindString,
		Summary: "write one of the login/bootstrap files on the HOST, for the scp: after it. " +
			"Kinds: launcher, macos_agent_plist, macos_mount_script, macos_mount_plist, " +
			"windows_task"},
	// THE UEFI FALLBACK PATH, PUT ON A DISK THAT IS NOT RUNNING.
	//
	// AN INSTALLER THAT REGISTERS ITS LOADER WITH `efibootmgr` PUTS THE ENTRY IN THE NVRAM, NOT ON
	// THE DISK, and this repository makes a FRESH variable store for every boot - deliberately, so a
	// guest cannot inherit an entry naming a disk somebody else wrote. Firmware with an empty NVRAM
	// looks in exactly one place:
	//
	//	\EFI\BOOT\BOOTX64.EFI
	//
	// which is why a disk whose installer wrote /EFI/BLISSOS still boots to
	//
	//	BdsDxe: No bootable option or device was found.
	//
	// This step copies the loader tree to that path and rewrites the config's `search.fs_uuid`, a
	// UUID recorded BEFORE the installer formatted the partition - so it names a filesystem that no
	// longer exists.
	"esp_fallback": {Name: "esp_fallback", Kind: KindString,
		Summary: "copy a disk's EFI loader tree to \\EFI\\BOOT and repair its config search; the " +
			"value is the disk, or `disk` for the one this stage built"},
	"esp_from": {Name: "esp_from", Kind: KindString,
		Summary: "with esp_fallback: the loader directory inside the ESP, default /EFI/BLISSOS"},
	// --- the three-step model ---------------------------------------------------------------
	//
	// ONE OCR STEP, ONE CONDITION STEP, AND A LOOP THAT NAMES ALL THREE. The six keys they replace
	// (`until`, `until_any`, `expect`, `expect_any`, `unless`, `unless_any`) each meant "read the
	// screen and decide", and they could sit inside a click step - so a failure said `did not
	// advance` whether the click missed or the crop was wrong, and 47 of 70 readings covered the
	// whole frame because `region:` was optional.
	// `ocr:` IS GONE: `see: {name: X, of: [text]}` IS the same operation.
	//
	// MEASURED: `ocr:` 118 uses against `see:`'s handful, and the two produced IDENTICAL readings -
	// `seeText` and `doOCR` both crop a rect and run tesseract on it. Keeping both meant a reader had
	// to know that a word list came from one key and a number from another, when the only difference
	// is what the reading is USED for.
	//
	// IT IS ANSWERED BY THE ONE QUESTION THAT MATTERS: "is this a measurement?" A word list is a
	// measurement of what the screen says. So it is `see:`, and `of:` names which parts.

	// --- `see:` and `want:`: a picture is a VALUE --------------------------------------------
	//
	// THE PROBLEM THESE TWO SOLVE IS COUNTED, not a matter of taste. `internal/screen` is 1219 lines,
	// because every question about a picture is a Go function reached through a dispatch table -
	// `desktop: linux/cinnamon` and eight siblings. So a new screen shape is new Go, and new Go is an
	// image build. The reason it has to be Go at all is that the COMPARISON against the thresholds
	// lived beside the MEASUREMENT, so moving one threshold was a rebuild.
	//
	// MEASURED, from the 57 files and 1429 steps: 38 ocr steps of which 34 crop the whole frame and
	// 38 restate `region_at`, which the header already says; 61 steps exist only to be half of one
	// thought; 469 steps carry `wait_after`, and 28 of those are a timed guess immediately followed by
	// an actual measurement of the same screen - which is the bug class that failed cachyos and
	// endeavouros on CI.
	//
	// SO: A MEASUREMENT IS CODE, A VERDICT IS DATA.
	//
	//	- see:
	//	    name: bottom
	//	    rect: bottom 4.5%
	//	    of: [mean, spread, sat, flat_rows]
	//	- want:
	//	    name: desktop_up
	//	    any:
	//	      - bottom.spread: {less_than: 42}
	//	      - bottom.flat_rows: {at_least: 3}
	//
	// `see:` binds NUMBERS with names; `want:` binds a BOOLEAN from them. Together they are `ocr:` +
	// `check:` generalised from words-only to any measurement, and they make a desktop test a
	// sentence in the file rather than a function in the binary.
	//
	// THE VERDICT IS DATA AND IS PROVEN FOR THE THREE MEDIA ALREADY IN testdata: `measure_test.go`
	// evaluates this exact rule against mint, zorin and a BIOS menu, and reproduces the verdicts the
	// Go reaches - which is what makes `IsCinnamonDesktop` deletable rather than merely redundant.
	"see": {Name: "see", Kind: KindMapping,
		Summary: "MEASURE one rect and bind NUMBERS: {name, rect, of}. It decides nothing - a " +
			"`want:` step answers from the names. `of:` names what to measure: mean, spread, sat, " +
			"flat_rows, rows, text"},
	"want": {Name: "want", Kind: KindMapping,
		Summary: "DECIDE from named readings and bind a BOOLEAN: {name, all|any|not}. It compares " +
			"with less_than, greater_than, at_least, at_most, equals, or a difference from another " +
			"reading. It never reads the screen, so it costs nothing to re-ask"},
	// --- `check:`'s criteria, and the relations inside them -------------------------------
	//
	// THESE ARE SUBKEYS: they are written inside `check:` and never on a step. They are declared
	// here for the same reason every other key is - the validator refuses an unknown key, and a
	// subkey that is not declared would make a legitimate file fail to load. `audit_test.go` reports
	// any declared key that the table omits, which is what keeps this list and the document in step.
	//
	// THE THREE TEXT COMPARISONS ANSWER THREE QUESTIONS, and the split is measured rather than
	// tidy: `contains:` asks whether the region CONTAINS words, `like:`/`ratio:` asks whether it IS
	// a sentence, and `fuzzy:` is the tolerance `contains:` needs when a glyph is misread.
	"contains": {Name: "contains", Kind: KindList,
		Summary: "with `check: text:`: ALL these words, any order, case and punctuation folded"},
	"contains_any": {Name: "contains_any", Kind: KindWordSets,
		Summary: "with `check: text:`: ANY one of these word-sets"},
	"not_contains": {Name: "not_contains", Kind: KindList,
		Summary: "with `check: text:`: NONE of these words may be present"},
	"equals": {Name: "equals", Kind: KindString,
		Summary: "with `check: text:`: the whole region's text equals this, normalised"},
	"matches": {Name: "matches", Kind: KindString,
		Summary: "with `check: text:`: a regular expression over the region's text"},
	"like": {Name: "like", Kind: KindString,
		Summary: "with `check: text:`: the whole region is approximately THIS sentence - the " +
			"only place a ratio belongs, because it needs a long string to be reliable"},
	"fuzzy": {Name: "fuzzy", Kind: KindInt,
		Summary: "with `check: text: contains:`: an EDIT DISTANCE per word, 0 (exact) to 2. A " +
			"distance and not a ratio, because `disk`->`dsk` is one edit and scores 0.750 as a ratio"},
	"ratio": {Name: "ratio", Kind: KindFloat,
		Summary: "with `check: text: like:`: how close, 0 to 1. 0.85 separates one misread glyph " +
			"from a wrong crop"},
	"mean": {Name: "mean", Kind: KindMapping,
		Summary: "with `check: colour:`: mean brightness 0..255 over the rect, compared with " +
			"less_than, greater_than, at_least, at_most, between or equals"},
	"spread": {Name: "spread", Kind: KindMapping,
		Summary: "with `check: colour:`: the mean absolute deviation of brightness - what FLAT " +
			"means. A panel's border measures 0 to 2; a wallpaper 8 or more"},
	"sat": {Name: "sat", Kind: KindMapping,
		Summary: "with `check: colour:`: mean HSV saturation 0..1"},
	"flat_rows": {Name: "flat_rows", Kind: KindMapping,
		Summary: "with `check: colour:`: how many rows in the rect are ONE COLOUR across its full " +
			"width - the number that sees a panel whose colour matches its wallpaper"},
	"contrast": {Name: "contrast", Kind: KindMapping,
		Summary: "with `check: colour:`: max minus min brightness inside the rect"},
	"vs": {Name: "vs", Kind: KindMapping,
		Summary: "with `check: colour:`: compare this rect's number against ANOTHER rect's - " +
			"`{rect: middle, mean: {greater_than: 40}}` is the panel rule's brightness difference"},
	"min": {Name: "min", Kind: KindMapping,
		Summary: "with `check: colour:`: the darkest pixel in the rect"},
	"less_than": {Name: "less_than", Kind: KindFloat,
		Summary: "a comparison: < this"},
	"greater_than": {Name: "greater_than", Kind: KindFloat,
		Summary: "a comparison: > this"},
	"at_least": {Name: "at_least", Kind: KindFloat,
		Summary: "a comparison: >= this"},
	"at_most": {Name: "at_most", Kind: KindFloat,
		Summary: "a comparison: <= this"},
	"between": {Name: "between", Kind: KindPair,
		Summary: "a comparison: [low, high] inclusive"},
	"minus": {Name: "minus", Kind: KindString,
		Summary: "a comparison modifier: subtract another reading first, as " +
			"`{minus: body.mean, greater_than: 40}`"},

	"rect": {Name: "rect", Kind: KindAny,
		Summary: "with `see:` and `check:`: the region to measure, as a FRACTION of the frame " +
			"rather than a pixel pair, so it cannot be measured at the wrong size. Forms: " +
			"`middle`, `top 4.5%`, `bottom 40px`, or [x0, y0, x1, y1] as fractions"},
	"rect_at": {Name: "rect_at", Kind: KindPair,
		Summary: "with a PIXEL `rect:`: [width, height] the rectangle was measured at. A rectangle " +
			"is pixels on ONE screen, and naming that screen is what lets it be scaled for a guest " +
			"drawing at another size"},
	"of": {Name: "of", Kind: KindList,
		Summary: "with `see:`: which numbers to bind - mean, spread, sat, flat_rows, contrast, " +
			"min, max, or text"},
	"colour": {Name: "colour", Kind: KindList,
		Summary: "with `check:`: a list of pixel criteria, each with its own `rect:` and any of " +
			"mean, spread, sat, flat_rows, contrast, min, max, vs"},
	"any_of": {Name: "any_of", Kind: KindList,
		Summary: "with `check:`: groups of criteria where ONE must pass - the measured panel " +
			"rule is a disjunction of four shapes, which is what this key exists for"},
	"from": {Name: "from", Kind: KindString,
		Summary: "with `check:`: which named reading the older single-comparison form compares"},
	"all": {Name: "all", Kind: KindList,
		Summary: "with `want:`: every comparison must hold"},
	"any": {Name: "any", Kind: KindList,
		Summary: "with `want:`: at least one comparison must hold"},
	"not": {Name: "not", Kind: KindMapping,
		Summary: "with `want:`: the comparison must NOT hold"},

	// `settle:` IS `wait:` AND `wait_after:` WITH ONE NAME.
	//
	// MEASURED: `wait_after` appeared 627 times and `wait` 118 - the same key under two names, and
	// the pair was 36 to 1 because the 627 was a timed GUESS after an action. One in three steps
	// carried a guess, and 28 of those guesses were immediately followed by a real measurement of
	// the same screen: that is the bug class that failed cachyos and endeavouros on CI, where the
	// guess was right on the measuring machine and wrong on the runner.
	//
	// `settle:` is the honest name for what both did: "wait this long because there is no fact to
	// wait on". A step that CAN wait on a fact must - the validator reports a `settle:` followed by
	// a `check:` of the same region, because the fact should have been used.
	"settle": {Name: "settle", Kind: KindFloat,
		Summary: "seconds to wait where there is NO fact to wait on; the older `wait:` and " +
			"`wait_after:` both become this"},

	"check": {Name: "check", Kind: KindMapping,
		Summary: "compare a named reading and bind a BOOLEAN: {name, from, mode, ...}. `from:` is " +
			"what an `ocr:` step read; `mode:` says HOW to compare. It never reads the screen, so " +
			"it costs nothing to re-ask"},
	"mode": {Name: "mode", Kind: KindString,
		Summary: "with `check:`: how the reading is compared - says, says_any, says_without, " +
			"size_between, number_between, equals, contains or matches. The modes are the extension " +
			"point: a new kind of comparison is a new value, not a new top-level key"},
	"words": {Name: "words", Kind: KindList,
		Summary: "with `check: mode: says`/`says_any`/`says_without`: the word list, or a list of " +
			"word-sets"},
	"without": {Name: "without", Kind: KindWords,
		Summary: "with `check: mode: says_without`: words that must NOT be on screen - for a test " +
			"that has to say \"this page is gone\", which Go's regexp cannot express because RE2 has " +
			"no lookahead"},
	"range": {Name: "range", Kind: KindList,
		Summary: "with `check: mode: size_between`/`number_between`: [low, high]. GB for a size, " +
			"so a pane printing MB or TB is comparable"},
	"text": {Name: "text", Kind: KindString,
		Summary: "with `check: mode: equals`/`contains`: the text to compare against"},
	"pattern": {Name: "pattern", Kind: KindString,
		Summary: "with `check: mode: matches`: a regular expression over the normalised reading"},
	"as": {Name: "as", Kind: KindString,
		Summary: "bind a condition's result as a named boolean: `until_any: [...] as: up`. The " +
			"name can then be tested by `if:` and `until: {fact: ...}`. With `unless:` the " +
			"boolean is TRUE when the words were NOT there"},
	"if": {Name: "if", Kind: KindString,
		Summary: "run `then:` when this named boolean is true and `else:` when it is false. The " +
			"boolean comes from a `check:` step; a branch is a plain step list"},
	"then": {Name: "then", Kind: KindList,
		Summary: "with `if:`: the steps to run when the boolean is true"},
	"else": {Name: "else", Kind: KindList,
		Summary: "with `if:`: the steps to run when the boolean is false"},
	"fact": {Name: "fact", Kind: KindString,
		Summary: "with `until:` inside a loop: the boolean a `check:` step produced. `until:` " +
			"tests it and can do nothing else - the comparison belongs to `check:`"},
	"loop": {Name: "loop", Kind: KindMapping,
		Summary: "the parts written down: `max:`, `do:` (the action), `ocr:` (the reading -> text), " +
			"`check:` (the comparison -> boolean), `until: {fact: ...}` (tests the boolean)"},
	"max": {Name: "max", Kind: KindInt,
		Summary: "with `loop:`: the attempt ceiling. Required, because a loop with no ceiling " +
			"presses a key until the stage times out"},
	"repeat_until": {Name: "repeat_until", Kind: KindMapping,
		Summary: "run `actions:` and then read the screen, up to `attempts:` times, stopping " +
			"when `until_any:` matches. This is the loop the vocabulary was missing: the two " +
			"primitives it replaces (choose a disk by capacity, leave the Apple Account pane) " +
			"were loops and decisions hidden in Go, where a reader of the file cannot see them"},
	"wait_for": {Name: "wait_for", Kind: KindString,
		Summary: "wait for a control with no readable title; login_window is the password field, " +
			"twelve pixels tall, read from a crop at 6x"},
	"demo_script": {Name: "demo_script", Kind: KindString,
		Summary: "write a throwaway script, for the primitives walkthrough that exists to exercise " +
			"the vocabulary itself"},
	// THREE KINDS OF HOST-SIDE PREPARATION, AND `fetch:` WAS ALL THREE OF THEM.
	//
	// MEASURED FROM THE 23 USES IN THIS REPOSITORY, its eight values did three different jobs:
	//
	//	download a published file   linux_iso, windows_iso, virtio_win, recovery_macos, xcode
	//	build an artifact           opencore - reads apfs.efi out of the recovery image and
	//	                            assembles boot.img
	//	generate a file             answer_iso - renders a template and packs it into an ISO
	//
	// A NAME THAT SAYS "fetch" FOR ALL THREE HIDES THE BUILD AND THE GENERATE BEHIND A WORD THAT
	// PROMISES A DOWNLOAD, and a reader who saw `fetch: opencore` had no way to know it was reading
	// a 888 MiB recovery image.
	// `asset:`, `build:` AND `generate:` WERE ONE OPERATION UNDER THREE NAMES.
	//
	// All three dispatched to `doFetch` and differed only in which WORD the file chose: take a
	// published file, assemble one, render one. MEASURED: `asset:` 1 use, `build:` 2, `generate:` 1
	// - the distinction was never load-bearing, because the KIND is the value.
	"fetch": {Name: "fetch", Kind: KindAny,
		Summary: "produce one host-side input: an installer image, a guest agent, a firmware, an " +
			"answer CD. The value names WHAT, not how it is obtained"},
	"template": {Name: "template", Kind: KindString,
		Summary: "with `generate: answer_iso`: the answer file to substitute into. It is a path in " +
			"the repository because the placeholders are the file's vocabulary, not a download"},

	// THERE IS NO `python:` AND NO `python_screen:`. The Go implementation has no interpreter, so
	// a file that names one is refused as an unknown key - which is the same answer a typo gets, and
	// the right one: a key that cannot be executed must not be in the vocabulary at all. Keeping it
	// here so it could be refused BY NAME was the `Deprecated` table, and that is deleted.
}

// Validate checks a step mapping against the vocabulary.
//
// A KEY THAT IS UNKNOWN FAILS THE FILE, and that is the whole point of this list: the Python
// implementation kept 85 keys alive by accepting whatever the executor happened to read, so a
// misspelled key was a step that did nothing and reported success. Two such key names are in this
// project's history already (`family:` then `os:`, `eject` spelled two ways).
// THERE IS NO DEPRECATED LIST. A NAME IS EITHER IN THE VOCABULARY OR IT IS REFUSED.
//
// THERE WAS ONE, and it existed so a renamed key could be refused with a sentence rather than a parse
// error. IT WAS REMOVED BECAUSE THE FILES ARE THE ONLY CALLER. Every walkthrough in this repository
// is renamed in the same commit as the vocabulary, so a stale name is not a user to be guided - it is
// a file that was not updated, and "unknown key" is the correct answer for it. A compatibility table
// with no external consumer is a second place the vocabulary is written down, and the two drift.
// THE BLIND-SLEEP RULE WAS RIGHT FOR ONE COMMIT AND IS NOW WRONG, AND SAYING SO IS THE POINT.
//
// `settle:` used to be `time.Sleep` and did not poll `ShouldStop`, so a long pause beside an action
// was a wall: `easyvm stop` sets a flag and nothing reads it until the pause ends. This validator
// therefore refused anything over ten seconds, and it did its job by failing all 24 linux files at
// once - because the migration in the previous commit had converted every `wait_after:` to `settle:`,
// including the twenty-second ones.
//
// THE FIX WAS IN `settle` RATHER THAN IN THE FILES: it polls in five-second steps now, exactly as
// `doWait` does, so ANY length is interruptible and there is nothing left to refuse. A validator that
// kept the rule would be forcing files to split a pause in two to satisfy a limit whose reason no
// longer exists - which is how a validator becomes a tax.
//
// actionKeys are the keys that make a step DO something, so a `settle:` beside one is a pause and not
// a wait.
var actionKeys = []string{"click", "double", "at_button", "at_color", "key", "type", "run", "ssh",
	"scp", "script", "adb", "write", "share_serve", "share_mount", "boot", "reboot", "shutdown",
	"disk", "record", "compress", "esp_fallback", "screenshot", "ocr", "see", "check", "want", "loop",
	"if", "desktop", "require_ssh"}

// Validate refuses a key the vocabulary does not have, and a blind pause long enough that stopping
// the machine would not work.
func Validate(stepName string, mapping map[string]any) error {
	for name, value := range mapping {
		key, ok := Keys[name]
		if !ok {
			return fmt.Errorf("step %q has unknown key %q; an unknown key used to mean a step that did nothing",
				stepName, name)
		}
		// THE NAME IS WHAT THE LOG SAYS, SO IT IS A STRING AND NOTHING ELSE.
		if name == "name" {
			continue
		}
		if err := checkKind(key, value); err != nil {
			return fmt.Errorf("step %q: %w", stepName, err)
		}
	}
	return nil
}
