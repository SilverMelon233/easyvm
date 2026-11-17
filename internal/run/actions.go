package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/easydev-org/easyvm/internal/asset"
	"github.com/easydev-org/easyvm/internal/bootstrap"
	"github.com/easydev-org/easyvm/internal/screen"
	"github.com/easydev-org/easyvm/internal/walk"
)

// --- the machine ----------------------------------------------------------------------

func (e *Executor) doBoot(s walk.Step) error {
	m := e.Driver.Machine()
	if m == nil {
		return fail(s.Name, "this driver has no machine to start")
	}

	// A MACHINE THAT IS ALREADY RUNNING IS NOT AN ERROR, BECAUSE A YAML IS AN ACTION ON A MACHINE.
	//
	// THE FIRST MODEL WAS ONE FILE PER MACHINE: every walkthrough booted, did its steps and closed.
	// A SANDBOX IS THE OTHER WAY ROUND - `init.yaml` installs, `dev.yaml` adds a toolchain, `test.yaml`
	// runs the tests, ALL ON ONE MACHINE THAT WAS BOOTED ONCE.
	//
	// So a `boot:` step whose machine is already up is a NO-OP WITH A LOG LINE rather than a failure.
	// It is the same rule as a `mkdir -p`: the step says "this machine must be running", and it is.
	//
	// THE `os:` STILL HAS TO MATCH, when the step names one. A file that says `os: windows` against a
	// running Linux guest is a mistake, not a no-op, and it would otherwise be discovered by a click
	// landing on the wrong screen.
	if running, ok := m.(interface{ IsRunning() bool }); ok && running.IsRunning() {
		want := s.Get("os")
		if want == "" {
			want = s.Get("family")
		}
		if want != "" {
			if named, ok := m.(interface{ OS() string }); ok {
				if have := named.OS(); have != "" && have != want {
					return fail(s.Name, "the machine is already running %s, and this step asks "+
						"for %s. One daemon holds one machine; `destroy` it to change the guest", have, want)
				}
			}
		}
		e.Driver.Log(fmt.Sprintf("  already running: %s", s.Name))
		return nil
	}
	// `os:` REPLACED `family:`, and both are read so a file that has not been renamed still runs.
	osName := s.Get("os")
	if osName == "" {
		osName = s.Get("family")
	}
	if osName == "" {
		return fail(s.Name, "a boot step must name `os:` (windows, linux or macos)")
	}

	// `boot_from:` IS A RESUME, AND IT IS A SEPARATE KEY FROM `boot:` ON PURPOSE.
	//
	// A step that says `boot: true` asks for a boot loader to run; one that says `boot_from:` asks for
	// a machine whose memory is already decided. They produce the same running guest by different
	// means, and a file that meant one and got the other would take minutes to notice - the guest
	// boots happily from the wrong state and the failure appears as "my data is missing".
	resumeFrom := s.Get("boot_from")
	if resumeFrom != "" && !filepath.IsAbs(resumeFrom) {
		resumeFrom = filepath.Join(e.Work, resumeFrom)
	}
	if resumeFrom != "" {
		if _, err := os.Stat(resumeFrom); err != nil {
			return fail(s.Name, "no snapshot at %s: %v. A `boot_from:` names a file an earlier "+
				"`snapshot:` wrote", resumeFrom, err)
		}
	}
	spec := BootSpec{
		OS:           osName,
		Disk:         e.bootDisk(),
		ISO:          e.assetPath(s.Get("iso")),
		AnswerCD:     e.assetPath(s.Get("answer_cd")),
		DriverCD:     e.assetPath(s.Get("driver_cd")),
		Recovery:     e.assetPath(s.Get("recovery")),
		Incoming:     resumeFrom,
	}
	// THE SIZE COMES OFF THE STEP, WHICH IS WHERE `MachineSteps` PUT IT.
	//
	// WITHOUT THIS THE TWO KEYS WERE DECORATION: `machineFromBlock` read `memory_mb:` into the spec,
	// `MachineSteps` emitted the steps and dropped it, and qemu always got the 4096 MiB default. See
	// the long comment in machine.go for the guest that measured it.
	// BOTH SPELLINGS: `mem_mb` is what the vocabulary's own summary named, and a file written
	// against that sentence must not be a machine that silently keeps the default.
	mb, err := s.Float("memory_mb", 0)
	if err != nil || mb == 0 {
		mb, _ = s.Float("mem_mb", 0)
	}
	if mb > 0 {
		spec.MemMB = int(mb)
	}
	if n, err := s.Float("cpus", 0); err == nil && n > 0 {
		spec.CPUs = int(n)
	}
	// THE OPENCORE FILES ARE NEEDED BY EVERY MACOS BOOT, AND ONLY SOME OF THEM ATTACH THE
	// RECOVERY IMAGE. They are built from it - Apple's APFS driver comes out of that image - but
	// that is HOST work done by an earlier step, and the guest is not given the image on an
	// installed disk. Tying these three paths to `recovery:` conflated the two, so an installed
	// macOS disk would have booted with an empty pflash path.
	if osName == "macos" {
		// THE THREE PATHS COME FROM THE `fetch: opencore` STEP WHEN ONE RAN, and from the work
		// directory's layout otherwise. A build that produced them somewhere else must be able to
		// say where: guessing a path is how a stage boots a boot.img from a previous run.
		if boot, ok := e.assets[string(asset.OpenCore)]; ok {
			spec.BootImage = boot
			if extras, ok := e.assetExtras[string(asset.OpenCore)]; ok && len(extras) >= 2 {
				spec.OpenCoreROM, spec.OpenCoreVars = extras[0], extras[1]
			}
		} else {
			spec.BootImage = filepath.Join(e.Work, "boot.img")
			spec.OpenCoreROM = filepath.Join(e.Work, "macos.rom")
			spec.OpenCoreVars = filepath.Join(e.Work, "macos.vars")
		}
	}

	e.Driver.Log(fmt.Sprintf("  booting %s from %s%s", spec.OS, bootSourceName(spec),
		mediaNote(spec)))
	if err := m.Boot(spec); err != nil {
		return fail(s.Name, "boot: %v", err)
	}
	e.Driver.Log("  qemu started")
	return nil
}

// bootSourceName is what the log calls this: the fact a reader checks first.
func bootSourceName(spec BootSpec) string {
	switch {
	case spec.Recovery != "":
		return "the recovery image"
	case spec.ISO != "":
		return "the installer"
	}
	return "the disk"
}

// mediaNote lists the media, because a boot line that names only the primary medium hides the other
// two a Windows install needs.
func mediaNote(spec BootSpec) string {
	var parts []string
	for _, m := range []struct{ path, why string }{
		{spec.ISO, "installer"},
		{spec.AnswerCD, "answer"},
		{spec.DriverCD, "drivers"},
		{spec.Recovery, "recovery"},
	} {
		if m.path != "" {
			parts = append(parts, m.why+"="+filepath.Base(m.path))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " + " + strings.Join(parts, " + ")
}

// assetPath resolves a medium a `fetch:` produced.
//
// `answer_cd: answer_iso` MEANS "THE FILE THE answer_iso FETCH WROTE", not a path. That keeps the
// name of a produced file in one place - the asset layer - instead of in a YAML file that would
// then disagree with it. Anything else is used as a path, so a file run by hand against a medium
// it prepared itself still works.
func (e *Executor) assetPath(v string) string {
	if v == "" {
		return ""
	}
	if p, ok := e.assets[v]; ok {
		return p
	}
	// A NAME THAT IS NOT AN ASSET ROLE IS A PATH, AND A RELATIVE ONE IS RELATIVE TO THE WORK
	// DIRECTORY - NOT TO THE PROCESS'S CURRENT DIRECTORY.
	//
	// MEASURED on run 43879668884, whose install leg had the ISO right there:
	//
	//	=== the media this stage boots from is already on disk
	//	  required: freebsd.iso
	//	error: boot the installer: boot: no the installer medium at freebsd.iso:
	//	       stat freebsd.iso: no such file or directory
	//
	// `require:` joins `e.Work` and finds the file; `boot:` passed the bare name to qemu, which
	// resolved it against the PROCESS'S directory - `/__w/easyvm/easyvm`, where no ISO lives. Two
	// steps, two answers about one file, and the one that failed named the file rather than the
	// directory it looked in.
	//
	// SO EVERY RELATIVE PATH IS JOINED HERE, once, for every role a boot step can name.
	if !filepath.IsAbs(v) {
		return filepath.Join(e.Work, v)
	}
	return v
}

// bootDisk is the target, from the `disk:` step when it ran and from the flag otherwise.
func (e *Executor) bootDisk() string {
	if d, ok := e.Driver.Machine().(interface{ Disk() string }); ok {
		if p := d.Disk(); p != "" {
			return p
		}
	}
	return e.BootDisk
}

func (e *Executor) doShutdown(s walk.Step) error {
	m := e.Driver.Machine()
	if m == nil {
		return fail(s.Name, "this driver has no machine to stop")
	}
	method := s.Get("shutdown")
	if method == "" {
		return fail(s.Name, "`shutdown:` must name a method: guest_agent, ssh, acpi or kill")
	}
	e.Driver.Log(fmt.Sprintf("  stopping with method %q", method))
	// THE STEP'S OWN `timeout:` IS THE GRACE THE GUEST GETS, and passing it is the whole point: a
	// stage that just wrote a desktop needs longer than the five-minute default, and a kill after a
	// fixed five minutes leaves a filesystem that needs a replay.
	//
	// `seconds` ALREADY RETURNS A `time.Duration`, so multiplying by `time.Second` again squared the
	// unit. MEASURED with the arithmetic this line used to do:
	//
	//	timeout: 300  ->  1347804h7m  =  153.9 YEARS
	//
	// and `waitForExit` was given that as its deadline, so a `shutdown: ssh` step waited for a guest
	// that had already been asked to power off and would never exhaust the grace and reach the kill.
	// TWO SUCH STAGES SAT IN "stop the machine" FOR FORTY MINUTES AND WERE STILL RUNNING when this was
	// found - macos-ssh run 43944852857 and windows-vm-configure run 43962063140 - and neither could
	// ever report. A `shutdown: acpi` stage looked healthy only because qemu exits on its own.
	//
	// THE ZERO FALLBACK STILL MEANS "THE DRIVER'S DEFAULT": `graceOrDefault` reads `> 0`.
	grace := seconds(s, "timeout", 0)
	if err := m.Stop(method, grace); err != nil {
		return fail(s.Name, "%v", err)
	}
	return nil
}

func (e *Executor) doReboot(s walk.Step) error {
	m := e.Driver.Machine()
	if m == nil {
		return fail(s.Name, "this driver has no machine to reboot")
	}
	if err := m.Reboot(); err != nil {
		return fail(s.Name, "%v", err)
	}
	e.Driver.Log("  the machine is back")
	return nil
}

// --- the disk -------------------------------------------------------------------------

// doDisk names the file `boot:` will start.
//
// `disk:` IS A PATH, NOT A MAPPING. Every real file writes `disk: disk.qcow2`, and the richer
// {from, copy} form was never used outside a comment. Accepting both would be a second shape for
// one fact, which is the drift this port exists to remove - so a mapping is refused by name rather
// than silently half-read.
//
// THE OVERLAY IS `boot:`'s JOB, and that is from the Python implementation: a file used to need a
// separate `disk_overlay:` step because letting `boot:` make the overlay had no way to say so. Now
// it does, so this step only says which file.
func (e *Executor) doDisk(s walk.Step) error {
	// A STEP WITH `disk_gb:` CREATES A DISK; one with `disk:` NAMES AN EXISTING ONE.
	//
	// THEY ARE DIFFERENT ACTIONS AND THE PRESENCE OF THE KEY SAYS WHICH. A `disk:` step prepares a
	// copy of a disk that came from somewhere; `disk_gb:` makes the empty 128 GB disk an INSTALL
	// writes to, which is the first thing stage one needs and nothing else does.
	if s.Has("disk_gb") {
		return e.createDisk(s)
	}
	v, ok := s.Spec["disk"]
	if !ok {
		return fail(s.Name, "`disk:` must name a file")
	}
	path, isPath := v.(string)
	if !isPath {
		if _, isMap := v.(map[string]any); isMap {
			return fail(s.Name, "`disk:` names a FILE, not a mapping: write `disk: disk.qcow2`. "+
				"The {from, copy} form was never used by any file in this repository")
		}
		return fail(s.Name, "`disk:` must be a file name, got %T", v)
	}
	if path == "" {
		return fail(s.Name, "`disk:` is empty")
	}
	// A RELATIVE PATH IS LOOKED FOR IN BOTH PLACES, and the work directory wins.
	//
	// A file says `disk: disk.qcow2`, meaning "the prepared copy I am about to run on". In a CI job
	// that copy is in the work directory the stage created; when the file is run by hand it is
	// often beside the YAML. Trying `work` first and falling back to the file's own directory
	// serves both without the file having to know which one it is in.
	if !filepath.IsAbs(path) {
		inWork := filepath.Join(e.Work, path)
		beside := filepath.Join(filepath.Dir(e.File.Path), path)
		switch {
		case fileExists(inWork):
			path = inWork
		case fileExists(beside):
			path = beside
		default:
			return fail(s.Name, "no disk at %s or %s (work=%q, file=%q); a `disk:` step names "+
				"the file `boot:` will start, and this stage was given neither",
				inWork, beside, e.Work, e.File.Path)
		}
	}
	e.Driver.Log(fmt.Sprintf("  disk: %s", path))
	if _, err := os.Stat(path); err != nil {
		return fail(s.Name, "no disk at %s: %v", path, err)
	}
	if setter, ok := e.Driver.Machine().(interface{ SetDisk(string) }); ok {
		setter.SetDisk(path)
	}
	return nil
}

// createDisk makes an empty qcow2 for an install to write to.
//
// THE SIZE IS FIXED AT 128G BY EVERY FILE THAT USES THIS, and it is not a tunable: macOS's setup
// assistant picks its target by CAPACITY, and every coordinate in macos/15/steps was measured on the
// disk the workflow actually bakes. A different size moves a window that was measured once.
func (e *Executor) createDisk(s walk.Step) error {
	// A COUNT, NOT A DURATION. `seconds()` RETURNS A time.Duration, and reading `disk_gb` through
	// it produced a size of 128 * 10^9 - which qemu-img refused with
	//
	//     Invalid image size specified. Must be between 0 and 9223372036854775807.
	//
	// a message about the NUMBER that names neither the key nor the unit. `s.Float` reads the number
	// the file wrote.
	gbFloat, err := s.Float("disk_gb", 128)
	if err != nil || gbFloat <= 0 {
		return fail(s.Name, "`disk_gb:` must be a positive number of gigabytes, got %v",
			s.Spec["disk_gb"])
	}
	gb := int(gbFloat)
	path := s.Get("disk")
	if path == "" {
		return fail(s.Name, "a `disk_gb:` step must also name the file it creates: `disk: disk.qcow2`")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.Work, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fail(s.Name, "%v", err)
	}
	// A PREVIOUS FILE IS REPLACED, NOT REUSED. Starting from whatever a failed run left is how an
	// install writes into half a system and reports success - and the step's whole job is "a disk
	// that does not exist yet".
	if fileExists(path) {
		e.Driver.Log(fmt.Sprintf("  removing the disk a previous run left at %s", path))
		if err := os.Remove(path); err != nil {
			return fail(s.Name, "%v", err)
		}
	}

	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		return fail(s.Name, "qemu-img is not installed: %v", err)
	}
	e.Driver.Log(fmt.Sprintf("  creating %s, %d GiB", path, gb))
	// `-f qcow2` AND NO SPARSE SURPRISE: a qcow2 grows as it is written, so a 128G file is a few
	// hundred KiB until an installer puts something in it.
	cmd := exec.Command(qemuImg, "create", "-f", "qcow2", path, fmt.Sprintf("%dG", gb))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fail(s.Name, "qemu-img create: %v (%s)", err, firstLineOf(string(out)))
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fail(s.Name, "qemu-img reported success and %s is not there", path)
	}
	e.Driver.Log(fmt.Sprintf("  %s: %d KiB (a qcow2 grows as it is written)", filepath.Base(path),
		fi.Size()>>10))

	// A GPT IS WRITTEN HERE RATHER THAN BY THE INSTALLER, AND THAT IS A MEASURED REQUIREMENT.
	//
	// Android-x86's first pane lists the partitions IT FINDS. Given a blank disk it offers
	// "Create/Modify partitions" and the walkthrough has to drive cgdisk - seventeen line prompts in
	// a TUI that accepts `260M`, `EF00` and a name as TYPED LINES, where one misaligned key silently
	// changes the ESP's type to "Linux filesystem" and the install then has no ESP at all.
	//
	// Given the table below it shows `vda1 0.25GB` already selected, and ten key presses finish the
	// install. The ESP must also be 1 MiB aligned and typed EF00 - measured, twice: a table whose
	// first partition starts at LBA 34 makes the installer report "No ESP is selected", and one
	// whose ESP type GUID is not C12A7328-F81F-11D2-BA4B-00A0C93EC93B makes it answer "This is not
	// an EFI System Partition, please check again".
	if s.Has("disk_gpt") {
		espMiB, _ := s.Float("disk_esp_mib", 260)
		if err := writeGPT(path, gb, int(espMiB)); err != nil {
			return fail(s.Name, "%v", err)
		}
		e.Driver.Log(fmt.Sprintf("  GPT: ESP %d MiB at LBA 2048 (type EF00), the rest Linux",
			int(espMiB)))
	}

	if setter, ok := e.Driver.Machine().(interface{ SetDisk(string) }); ok {
		setter.SetDisk(path)
	}
	return nil
}

// --- the screen -----------------------------------------------------------------------

func (e *Executor) doDesktop(s walk.Step) error {
	// `desktop: X` NAMES THE FAMILY ITSELF, AND READING ONLY `os:` MADE EVERY ONE OF THEM WINDOWS.
	//
	// Measured on ubuntu's install leg, whose fourth step is `desktop: linux`:
	//
	//	wait for the live desktop: no windows desktop within 15m0s; drop=+17.8 ... on a 1920x1080 frame
	//
	// IT WAITED FIFTEEN MINUTES FOR A WINDOWS DESKTOP ON A UBUNTU GUEST, because `desktop_family` and
	// `os:` were both empty and the fallback is `windows`. The value of the key is the family, and
	// every file here writes `desktop: windows|macos|linux` - so reading it is the whole job.
	family := s.Get("desktop")
	if family == "" {
		family = s.Get("desktop_family")
	}
	if family == "" {
		family = s.Get("os")
	}
	if family == "" {
		family = "windows"
	}
	timeout := seconds(s, "timeout", 900)
	poll := seconds(s, "poll", 5)

	e.Driver.Log(fmt.Sprintf("  waiting up to %s for a %s desktop", timeout, family))
	start := time.Now()
	deadline := start.Add(timeout)
	var lastStats screen.Stats
	for time.Now().Before(deadline) {
		f, err := e.Driver.Frame()
		if err != nil {
			e.Driver.Log(fmt.Sprintf("  could not read the screen: %v", err))
			time.Sleep(poll)
			continue
		}
		img, err := screen.Load(f.Path())
		if err != nil {
			// A FRAME THAT CANNOT BE READ IS NOT A FAILURE OF THE GUEST. qemu writes it
			// asynchronously, so the newest one can be half-written; the next look will do.
			time.Sleep(poll)
			continue
		}
		var ok bool
		switch family {
		case "windows":
			ok, lastStats = screen.IsWindowsDesktop(img)
		case "macos":
			// A MACOS DESKTOP IS DECIDED BY THE DOCK AND THE BODY, not by words: there is no text
			// on it to read. `internal/screen.IsMacOSDesktop` has been implemented and tested since
			// the port began; the dispatcher simply had no branch for it, so `desktop: macos` in
			// macos/15/steps/ssh.yaml would have failed on the first frame.
			var mac screen.MacOSStats
			ok, mac = screen.IsMacOSDesktop(img)
			lastStats = mac.Stats()
		case "linux":
			// A LINUX DESKTOP IS A PANEL: a strip of colour along an edge that the installer under
			// it does not draw. The same measurement serves every distribution here because they all
			// run a panel - GNOME, KDE, XFCE, MATE, Cinnamon - and the installers they boot do not.
			ok, lastStats = screen.IsLinuxDesktop(img)

		// THE DISTRIBUTION'S OWN TEST, NAMED `linux/<name>`, FOR A MEDIUM WHOSE FIRST SCREEN IS NOT
		// A DESKTOP.
		//
		// Kubuntu 26.04 boots to a KDE launcher drawn on a full-bleed gradient: no panel, so
		// `IsLinuxDesktop` answers false with `Drop=0.0`, and a `desktop: linux` step would spend its
		// fifteen minutes on a machine that is sitting on its installer. MEASURED on that image:
		//
		//	the launcher                 sat=96.9
		//	every Calamares page         sat= 4.1 .. 6.2
		//
		// `screen.IsKubuntuLauncher` is that measurement, and naming it in the file as
		// `desktop: linux/kubuntu` is what makes the choice visible where the walk is read rather
		// than implicit in a family-wide threshold.
		// THE DESKTOP ENVIRONMENT IS THE UNIT, NOT THE DISTRIBUTION.
		//
		// Two media that run the same environment draw the same desktop, and one measurement serves
		// both; two media that run different environments do not, and a single `linux` threshold
		// answers for neither. So the key names the ENVIRONMENT - `linux/kde`, `linux/gnome`,
		// `linux/cinnamon`, `linux/xfce` - and the distribution reaches it by the desktop its ISO
		// carries:
		//
		//	kde        kubuntu, cachyos, endeavouros, manjaro, opensuse-leap-16.1
		//	gnome      ubuntu, popos, fedora-workstation, nixos, zorin
		//	cinnamon   mint
		//	xfce       mx
		//
		// `linux/kubuntu` REMAINS, because that medium's first screen is not a desktop at all - it is
		// a launcher on a full-bleed gradient, and it has its own measurement. The name is kept
		// rather than renamed because the file that uses it says which medium it is about.
		case "linux/kubuntu":
			ok, lastStats = screen.IsKubuntuLauncher(img)

		// KDE, WHOSE PANEL IS NOT ONE HEIGHT. MEASURED on two live media:
		//
		//	manjaro   panel 48 rows   IsLinuxDesktop=true   drop=128.4
		//	cachyos   panel  8 rows   IsLinuxDesktop=false  drop=  0.0   edge=16.9
		//
		// `IsKDEDesktop` tries the band average first and the row-step second, so one branch answers
		// for both without either file depending on the other's panel height.
		case "linux/kde":
			ok, lastStats = screen.IsKDEDesktop(img)

		case "linux/gnome":
			ok, lastStats = screen.IsGNOMEDesktop(img)

		case "linux/cinnamon":
			ok, lastStats = screen.IsCinnamonDesktop(img)

		case "linux/xfce":
			ok, lastStats = screen.IsXFCEDesktop(img)

		case "android":
			// ANDROID IS NOT A PANEL TEST, AND SAYING SO IS MORE USEFUL THAN A DETECTOR THAT ALMOST
			// WORKS.
			//
			// `IsLinuxDesktop` ANSWERS FALSE ON ANDROID'S DESKTOP, and that is correct of it: it
			// wants `margin > 40 && spread < 42` in the lowest 4.5%, and Taskbar is a 40 px
			// translucent bar over a dark wallpaper - contrast 9, spread 121, saturation 0.
			//
			// A DETECTOR WAS WRITTEN FOR ANDROID'S SHAPE AND IT WAS WITHDRAWN, BECAUSE IT DEPENDS ON
			// THE FRAMEBUFFER'S SIZE. Measured on one frame, at two sizes:
			//
			//	1920x1080   desktop step 1.17 -> true    splash step 0.61 -> false
			//	 640x360    desktop step 2.36 -> true    splash step 1.71 -> TRUE   <- wrong
			//
			// A verdict that changes when the same picture is scaled is not a verdict, and both
			// thresholds that were tried moved the failure rather than removing it.
			//
			// THE QUESTION HAS A BETTER ANSWER. adbd reports what the window manager thinks is in
			// front, which is the fact a person reading the frame is trying to infer:
			//
			//	adb: dumpsys window | grep mCurrentFocus | grep com.farmerbb.taskbar
			//
			// and `android/blissos16/steps/vm-configure.yaml` asks it three ways - `mCurrentFocus`,
			// `ResumedActivity`, and a window count - each of which a boot splash, the home-app
			// chooser and a lock screen all fail.
			return fail(s.Name, "`desktop: android` is not implemented, and the pixel test it used "+
				"to run was withdrawn because its verdict changed with the framebuffer's size. Ask "+
				"the system instead: `adb: dumpsys window | grep mCurrentFocus | grep "+
				"com.farmerbb.taskbar`, or `dumpsys activity activities | grep ResumedActivity`")
		default:
			return fail(s.Name, "the %q desktop test is not implemented in this build; the four "+
				"are windows, macos, linux and android", family)
		}
		if ok {
			// A DESKTOP AT THE WRONG SIZE IS NOT A DESKTOP, BECAUSE EVERY COORDINATE IN THE FILE WAS
			// MEASURED AT ONE SIZE.
			//
			// MEASURED ON zorin-install, WHICH PASSED THE GATE AND THEN PRODUCED A 0 MiB DISK:
			//
			//	a desktop is on screen (10s); drop=+20.6 (need 15) ratio=0.0x sat=+0.00
			//	on a 800x600 frame
			//
			// THE GUEST WAS AT 800x600 WHILE THE FILE CLICKS (1223, 586) AND (1361, 764) - measured on a
			// 1920x1080 screen. At 800x600 those land on the wallpaper, and the whole installer walk
			// pressed nothing: the disk was never written, and the stage "passed" its steps and
			// published 0 MiB.
			//
			// SO THE GATE ASKS FOR THE SIZE TOO. A guest that has not switched modes is a guest whose
			// coordinates mean something else, and saying so HERE names the cause on the step that can
			// see it rather than on the compress four hundred steps later.
			//
			// THE SIZE IS THE FILE'S OWN `resolution:`, which is what the coordinates were measured
			// against; 1920x1080 when the file does not say.
			want := resolutionOf(e.File)
			w, h := f.Size()
			if w > 0 && (w != want[0] || h != want[1]) {
				// A GUEST THAT NEVER SWITCHES IS NOT A GUEST TO WAIT FOR FOREVER, and the first
				// version of this check did exactly that. Measured on the images themselves:
				//
				//	opensuse-tumbleweed-install   a desktop ... on a 1920x1080 frame   (46s)
				//	zorin-install                 a desktop ... on a  800x600 frame   (10s)
				//
				// SAME qemu ARGUMENTS, DIFFERENT ANSWER, because `-device VGA,xres=,yres=` is what the
				// ADAPTER offers and the guest's own session decides. A distribution whose live session
				// has no mode for it stays where it started, and no amount of waiting changes that.
				//
				// SO THE CHECK IS A DEADLINE RATHER THAN A LOOP. A guest that is going to switch does it
				// while the desktop is still drawing - tens of seconds - and one that has not by now is
				// not going to. Waiting the whole desktop timeout would turn a mis-sized guest into a
				// ninety-minute stage, which is strictly worse than the 0 MiB disk it replaces.
				if time.Since(start) > resSwitchBudget {
					e.Driver.Log(fmt.Sprintf("  a desktop is on screen at %dx%d after %s, and this "+
						"file's coordinates were measured at %dx%d; this guest is not switching, so "+
						"every click in it will land somewhere else - continuing rather than waiting "+
						"for a mode this session does not have",
						w, h, time.Since(start).Round(time.Second), want[0], want[1]))
					return nil
				}
				e.Driver.Log(fmt.Sprintf("  a desktop is on screen at %dx%d, and this file's "+
					"coordinates were measured at %dx%d; waiting for the guest to switch modes",
					w, h, want[0], want[1]))
				time.Sleep(poll)
				continue
			}
			e.Driver.Log(fmt.Sprintf("  a desktop is on screen (%s); %s",
				time.Since(start).Round(time.Second), lastStats.Explain()))
			return nil
		}
		// A GUEST THAT BLANKS ITS OWN SCREEN WAKES ON INPUT, and a blank screen during a boot is
		// normal rather than a failure. THIS IS UNCONDITIONAL NOW, because it used to be behind
		// `keep_alive:` and that key was written `true` in all 25 places it appeared - a switch
		// nobody ever set to false is not a switch, it is the behaviour.
		wakeTheScreen(e)
		time.Sleep(poll)
	}
	return failWithScreen(s.Name, e.Driver,
		"no %s desktop within %s; %s", family, timeout, lastStats.Explain())
}

// --- host commands --------------------------------------------------------------------

// doScript writes a body on the host, copies it in and runs it by path.
//
// WHY BY PATH RATHER THAN AS A QUOTED ONE-LINER: the body is the guest's own shell, and a command
// that has to survive python -> ssh -> cmd loses its quotes. That is measured - the Python
// implementation's own comment says the Build Tools `--override` arrived with none of them.
func (e *Executor) doScript(s walk.Step) error {
	client := e.Driver.Guest()
	if client == nil {
		return fail(s.Name, "no ssh connection; a `require_ssh:` step must come first")
	}
	shell := s.Get("script_shell")
	if shell == "" {
		shell = "powershell"
	}
	name := scriptName(s)
	local := filepath.Join(e.Work, name)
	if err := os.MkdirAll(e.Work, 0o755); err != nil {
		return fail(s.Name, "%v", err)
	}
	body := s.Get("script")
	if shell == "cmd" {
		// cmd.exe reads a .bat byte for byte, and a stray \n is a command name.
		body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	}
	if err := os.WriteFile(local, []byte(body), 0o644); err != nil {
		return fail(s.Name, "writing %s: %v", name, err)
	}

	remote := "C:/Users/" + e.File.SSHUser + "/" + name
	if shell == "sh" {
		remote = "/tmp/" + name
	}
	if err := scpFile(client, local, remote); err != nil {
		return fail(s.Name, "copying %s in: %v", name, err)
	}

	var command string
	switch shell {
	case "cmd":
		command = `cmd /c "` + strings.ReplaceAll(remote, "/", `\`) + `"`
	case "powershell", "pwsh":
		command = `powershell -NoProfile -ExecutionPolicy Bypass -File "` + remote + `"`
	default:
		// `bash`, not `sh`: these bodies use `set -uo pipefail` and `[[ ]]`, which POSIX sh does
		// not have and which silently ignores a failing stage of a pipeline.
		command = "bash " + remote
	}

	res, err := client.Run(command, seconds(s, "timeout", 1800))
	e.Driver.Log(fmt.Sprintf("  script %s (%s) -> %d", name, shell, res.Status))
	for _, line := range headAndTail(res.Combined(), 18, 6) {
		e.Driver.Log("    " + line)
	}
	if err != nil {
		return fail(s.Name, "%v", err)
	}
	if res.Status != 0 {
		return fail(s.Name, "the script exited %d", res.Status)
	}
	return nil
}

func (e *Executor) doSCP(s walk.Step) error {
	client := e.Driver.Guest()
	if client == nil {
		return fail(s.Name, "no ssh connection; a `require_ssh:` step must come first")
	}
	raw, ok := s.Spec["scp"].([]any)
	if !ok || len(raw) != 2 {
		return fail(s.Name, "`scp:` must be [local-file, remote-path]")
	}
	local, _ := raw[0].(string)
	remote, _ := raw[1].(string)
	if local == "" || remote == "" {
		return fail(s.Name, "`scp:` must name both ends")
	}
	// A RELATIVE LOCAL PATH IS LOOKED FOR WHERE `write:` PUT IT, WHICH IS THE WORK DIRECTORY.
	//
	// Measured on manjaro's vm leg:
	//
	//	copy the launcher in: scp bootstrap-launcher.sh -> /tmp/bootstrap-launcher.sh:
	//	open bootstrap-launcher.sh: no such file or directory
	//
	// `write: launcher` WROTE IT and `scp:` could not find it, because the writer joins its name onto
	// the work directory and the reader used the process's own working directory. The two are
	// different places and the file was in the first one.
	//
	// THE WORK DIRECTORY WINS, and the caller's directory is the fallback - the same rule `disk:`
	// already follows, for the same reason: a file run by hand has its inputs beside it and a file run
	// by the daemon has them in `work`.
	if !filepath.IsAbs(local) {
		inWork := filepath.Join(e.Work, local)
		if fileExists(inWork) {
			local = inWork
		}
	}
	// A MISSING FILE NAMES EVERY PLACE IT WAS LOOKED FOR. The message above said only
	// `open bootstrap-launcher.sh: no such file or directory`, which reads as "the copy failed"
	// rather than "the step that should have produced it did not, or produced it elsewhere".
	if _, statErr := os.Stat(local); statErr != nil {
		return fail(s.Name, "no file to copy at %s (work=%q): %v. A `scp:` names a file that an "+
			"earlier `write:` or `fetch:` produced; if that step ran, look at where IT wrote",
			local, e.Work, statErr)
	}
	if err := scpFile(client, local, remote); err != nil {
		return fail(s.Name, "scp %s -> %s: %v", filepath.Base(local), remote, err)
	}
	e.Driver.Log(fmt.Sprintf("  scp: %s -> %s", filepath.Base(local), remote))
	return nil
}

// --- the share ------------------------------------------------------------------------

func (e *Executor) doShareServe(s walk.Step) error {
	dir := s.Get("share_dir")
	if dir == "" {
		dir = "~/shared"
	}
	dir = strings.ReplaceAll(dir, "~", os.Getenv("HOME"))
	e.Driver.Log(fmt.Sprintf("  share: serving %s", dir))
	if sv, ok := e.Driver.(interface{ ServeShare(string) error }); ok {
		if err := sv.ServeShare(dir); err != nil {
			return fail(s.Name, "%v", err)
		}
		return nil
	}
	return notImplemented(s, "share_serve")
}

// shareDirOrEmpty is the directory being served, for a proof that must look on the host side.
func (e *Executor) shareDirOrEmpty() string {
	if d, ok := e.Driver.(interface{ ShareDir() string }); ok {
		return d.ShareDir()
	}
	return ""
}

// shareURLOf asks the driver where the share is served. It is "" when nothing is shared, which the
// caller has already refused.
func shareURLOf(e *Executor) string {
	if sv, ok := e.Driver.(interface{ ShareURL() string }); ok {
		return sv.ShareURL()
	}
	return ""
}

// doShareMount proves the guest can read the shared directory over HTTP.
//
// IT USED TO MOUNT AN SMB SHARE, AND EVERY GUEST NEEDED A DIFFERENT CLIENT: `mount -t cifs` on
// Linux, Apple's mount_smbfs on macOS, `net use` on Windows - and on FreeBSD NOTHING IN THE BASE
// SYSTEM COULD CONNECT AT ALL, because netsmb speaks SMB1 on NetBIOS 139 while the server offers
// SMB3 on 445.
//
// THE TRANSPORT IS NOW THE DAEMON'S OWN HTTP LISTENER, so this step asks the guest to FETCH a file
// the host just wrote, with whatever that platform ships:
//
//	linux/bsd/macos   curl
//	windows           Invoke-WebRequest
//
// THE SENTINEL IS WRITTEN ON THE HOST AND READ IN THE GUEST, because the guest's own round trip
// proves nothing: measured on manjaro, where the mount had never happened and `~/shared` was an
// ordinary directory - a file written there and read back came back unchanged, and the check
// passed on a share that did not exist.
func (e *Executor) doShareMount(s walk.Step) error {
	family := s.Get("share_family")
	if family == "" {
		family = "windows"
	}

	dir := e.shareDirOrEmpty()
	if dir == "" {
		return fail(s.Name, "no directory is being shared; a `share_serve:` step must come first")
	}

	client := e.Driver.Guest()
	if client == nil {
		return fail(s.Name, "no ssh connection to fetch the proof with")
	}

	sentinel := fmt.Sprintf(".easyvm-share-proof-%d", time.Now().UnixNano())
	hostPath := filepath.Join(dir, sentinel)
	body := "from-the-host"
	if err := os.WriteFile(hostPath, []byte(body+"\n"), 0o644); err != nil {
		return fail(s.Name, "writing the proof file on the host: %v", err)
	}
	defer os.Remove(hostPath)

	url := shareURLOf(e) + sentinel
	e.Driver.Log(fmt.Sprintf("  share: the %s guest fetches %s", family, url))

	// THE FETCH IS THE GUEST'S OWN COMMAND, IN THE GUEST'S OWN CLIENT.
	//
	// MEASURED ON windows-vm-configure, in the SMB era: the check ran `cat`, which Windows does not
	// have, and reported "'cat' is not recognized as an internal or external command" - a working
	// share failing on the checker's own vocabulary. The same mistake is not repeated here.
	var cmd string
	if family == "windows" {
		cmd = `powershell -NoProfile -Command "(Invoke-WebRequest -UseBasicParsing -TimeoutSec 20 ` +
			"'" + url + "'" + `).Content"`
	} else if family == "bsd" {
		// `fetch` IS IN THE FREEBSD BASE SYSTEM and `curl` is not guaranteed; both are tried so the
		// check does not depend on which the image happens to carry.
		cmd = "sh -c 'curl -fsS --max-time 20 " + url + " 2>/dev/null || fetch -qo - " + url + "'"
	} else {
		cmd = "sh -c 'curl -fsS --max-time 20 " + url + " || wget -qO- --timeout=20 " + url + "'"
	}

	res, err := client.Run(cmd, 90*time.Second)
	got := strings.TrimSpace(res.Stdout)
	if err != nil || got != body {
		return fail(s.Name, "the guest cannot fetch a file this host wrote into %s. The daemon "+
			"serves it at %s; a guest that wrote to ~/shared instead would have written into its "+
			"OWN filesystem. (%v, read %q)", dir, url, err, firstLineOf(res.Combined()))
	}
	e.Driver.Log("  share: a file written on the host is readable in the guest - the share is real")
	return nil
}

// --- recording and publishing ---------------------------------------------------------

// doRecord starts or stops a recording.
//
// `record:` IS BOTH, because a file says `record: out/x.mp4` to start and `record: false` to stop.
// The stop half STOPS THE SAMPLER FIRST, so the last second is on disk before the encoder reads -
// stopping the encoder while the sampler still writes gives a video that ends one frame early, and
// on a fast shutdown that is the frame with the work in it.
func (e *Executor) doRecord(s walk.Step) error {
	value := s.Get("record")
	if value == "false" || value == "" {
		e.stopSampler()
		if e.rec == nil {
			return fail(s.Name, "`record: false` but nothing was recording")
		}
		// THE NAME COMES FROM THE START STEP, WHICH IS A DIFFERENT `walk.Step`.
		//
		// `s.Get("_video")` READ THE STOP STEP'S OWN SPEC and was therefore ALWAYS EMPTY - so every
		// recording was written to the fallback `install.mp4` whatever `record:` had said at the start.
		// A file with two `record:` steps produced a video named for neither.
		// THE NAME IS JOINED ONTO THE WORK DIRECTORY, WHICH IS WHAT THE FILES SAY.
		//
		// `record: zorin-install.mp4` IS A NAME, NOT A PATH: the artifact step uploads `work/*.mp4`, and
		// the daemon's cwd is not the work directory. MEASURED ON zorin-install:
		//
		//	=== stop recording
		//	  recording: 2643 frame(s) -> zorin-install.mp4
		//	  path: work/*.mp4
		//	  ##[warning]No files were found with the provided path: work/*.mp4
		//
		// THE ENCODE SUCCEEDED AND THE FILE WAS IN THE WRONG PLACE - 2643 frames, written to the
		// process's own directory instead of `work/`, so the artifact step found nothing and the run
		// reported a successful recording that does not exist.
		video := e.recVideo
		if video == "" {
			video = "install.mp4"
		}
		if !filepath.IsAbs(video) {
			video = filepath.Join(e.Work, video)
		}
		if err := e.rec.encode(video, e.Driver.Log); err != nil {
			return fail(s.Name, "%v", err)
		}
		e.rec = nil
		e.recVideo = ""
		return nil
	}
	if err := e.sampler(filepath.Join(e.Work, "shots")); err != nil {
		return fail(s.Name, "%v", err)
	}
	// REMEMBERED, because the `record: false` step is a DIFFERENT Step object and cannot see this one's
	// Spec - and because `finishRecording` needs it when the walk fails before that step runs.
	e.recVideo = value
	e.Driver.Log(fmt.Sprintf("  recording: %s", value))
	return nil
}

// doCompress flattens the disk this stage ran on into the artifact the workflow publishes.
//
// THE PATH IS RELATIVE TO THE REPOSITORY, NOT TO THE WORK DIRECTORY. A workflow writes
// `out/windows11-ssh.qcow2` from where the job started; joining that onto `work` produced
// `out/ssh-work/out/windows11-ssh.qcow2` and the publish step then uploaded nothing. Measured on the
// first real run of this step in the Python implementation.
func (e *Executor) doCompress(s walk.Step) error {
	dest := s.Get("compress")
	if dest == "" {
		return fail(s.Name, "`compress:` must name the artifact path")
	}
	source := e.Driver.Machine()
	if source == nil {
		return fail(s.Name, "no machine is known; a `boot:` step must come first")
	}
	disk := ""
	if d, ok := source.(interface{ Disk() string }); ok {
		disk = d.Disk()
	}
	if disk == "" {
		return fail(s.Name, "no disk is known; a `disk:` step must come first")
	}

	// AN EMPTY DISK IS NOT A PRODUCT, AND PUBLISHING ONE IS THE WORST KIND OF SUCCESS.
	//
	// MEASURED ON ghostbsd: the walk ran all 25 steps, reported `finished`, and left
	//
	//	/disks/os/ghostbsd/install.qcow2   198656 bytes
	//
	// - the size `qemu-img create` writes for a disk nobody has touched. Its `installation runs` step
	// is `wait: 1800`, A FIXED THIRTY MINUTES WITH NO CHECK THAT ANYTHING IS HAPPENING, so a guest whose
	// installer never started still "completes".
	//
	// THE FLOOR IS THE CHECK THAT CANNOT BE FAKED. 512 MiB is well above a fresh qcow2's metadata and
	// well below any real install - the smallest system here is ~1.2 GiB - so it separates "an
	// installer wrote a system" from "nothing wrote anything" with no judgement in between.
	//
	// IT IS `disk_min_mb:` WHEN A FILE SAYS SO, because a file that produces a deliberately small
	// artifact should be able to say what small means for it.
	floor, _ := s.Float("disk_min_mb", 512)
	if size, how := installedBytes(disk); size >= 0 && floor > 0 {
		if mb := float64(size) / (1 << 20); mb < floor {
			return fail(s.Name, "the disk is %.0f MiB, below the %.0f MiB floor for an installed "+
				"system (%s). This is a qcow2 header with nothing written to it - the steps above "+
				"ran without installing anything, and publishing it would put an empty disk behind "+
				"a tag the next stage pulls", mb, floor, how)
		}
	}

	// `qemu-img convert` READS THE WHOLE BACKING CHAIN AND WRITES ONE FILE, so it is the flatten.
	// There is no second call for an overlay: what this step publishes is either the disk the run
	// wrote (an install) or the diff on top of the disk it was given, and convert reads both.
	if err := compressDisk(disk, dest, e.Driver.Log); err != nil {
		return fail(s.Name, "%v", err)
	}

	// AND THE DIFF IS RELEASED AFTER IT HAS BEEN READ. A run that stops here and never releases
	// leaves a file whose base has never been re-checked, and the check is the guarantee.
	if rel, ok := source.(interface{ ReleaseDiff() error }); ok {
		if err := rel.ReleaseDiff(); err != nil {
			return fail(s.Name, "%v", err)
		}
	}
	return nil
}

// notImplementedCount reports how many steps a file has that this build cannot run, so `run` can
// refuse before starting a machine rather than after.
func notImplementedCount(steps []walk.Step, can map[string]bool) (int, string) {
	count := 0
	first := ""
	passive := map[string]bool{"name": true, "optional": true, "timeout": true, "requires": true,
		"wait_after": true, "poll": true, "os": true, "family": true}
	for _, s := range steps {
		blocking := false
		for k := range s.Spec {
			if passive[k] || can[k] {
				continue
			}
			blocking = true
			break
		}
		if blocking {
			count++
			if first == "" {
				first = s.Name
			}
		}
	}
	return count, first
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// --- host-side files ------------------------------------------------------------------

// doWrite writes one of the bootstrap files on the host, for the `scp:` after it.
//
// IT DOES NOT COPY AND DOES NOT RUN. That split is from the Python implementation and it was
// earned: a single step that wrote, copied and ran reported `the launcher install exited 1` and
// named neither the file nor the action, so a failure could only be diagnosed by reading the script.
//
// `family:` IS TAKEN FROM THE STEP WHEN IT NAMES ONE, and from the walkthrough otherwise - the same
// rule for the same reason as everywhere else: a fact about this disk belongs in the file that runs
// on it.
func (e *Executor) doWrite(s walk.Step) error {
	kind := s.Get("write")
	if kind == "" {
		return fail(s.Name, "`write:` must name a file kind")
	}
	family := s.Get("family")
	if family == "" {
		family = s.Get("os")
	}
	if family == "" && e.File != nil {
		// The walkthrough's own os, from its first boot step, if it names one.
		for _, other := range e.File.Steps {
			if v := other.Get("os"); v != "" {
				family = v
				break
			}
		}
	}

	opts := bootstrap.Options{
		Family: bootstrap.Family(family),
		Host:   "172.30.0.1",
		Share:  "shared",
		User:   "guest",
	}
	if e.File != nil {
		if v := e.File.Vars["share_user"]; v != nil {
			opts.User = fmt.Sprint(v)
		}
		if v := e.File.Vars["share"]; v != nil {
			opts.Share = fmt.Sprint(v)
		}
	}

	var body, name string
	switch kind {
	case "launcher":
		body, name = bootstrap.Launcher(opts), bootstrap.LauncherName(opts.Family)
	case "macos_agent_plist":
		body, name = bootstrap.MacOSAgentPlist("$HOME/bootstrap-launcher.sh"),
			"org.vm.bootstrap.plist"
	case "windows_task":
		body, name = bootstrap.WindowsTaskScript(
			"C:/Users/"+e.userForPaths()+"/bootstrap-launcher.ps1"), "bootstrap-install.ps1"
	default:
		return fail(s.Name, "`write: %s` is not a file this build writes; the kinds are "+
			"launcher, macos_agent_plist, windows_task", kind)
	}

	path := filepath.Join(e.Work, name)
	if err := os.MkdirAll(e.Work, 0o755); err != nil {
		return fail(s.Name, "%v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fail(s.Name, "writing %s: %v", name, err)
	}
	// THE PATH IS LOGGED because the `scp:` that follows names it, and a mismatch between the two
	// is the failure this split exists to make visible.
	e.Driver.Log(fmt.Sprintf("  wrote %s (%d bytes); the scp: step after this copies it in",
		path, len(body)))
	return nil
}

func (e *Executor) userForPaths() string {
	if e.File != nil && e.File.SSHUser != "" {
		return e.File.SSHUser
	}
	return "vmuser"
}

// doFetch produces a host-side asset.
//
// THE VALUES ARE THE FILE'S OWN VARIABLES, because an answer file's placeholders are exactly the
// facts the walkthrough already names: the account, its password, the computer name. A `fetch:`
// that took them from the environment instead would be a second place those facts live, and the
// disk would be installed with one account and driven with another.
func (e *Executor) doFetch(s walk.Step) error {
	// ONE KIND, THREE KEY NAMES. Which key the file used says what is being done - taking a
	// published file, assembling one, or rendering one - and this reads it without caring which.
	kind := s.Get("asset")
	if kind == "" {
		kind = s.Get("build")
	}
	if kind == "" {
		kind = s.Get("generate")
	}
	if kind == "" {
		return fail(s.Name, "the step must name what it prepares: `asset:`, `build:` or `generate:`")
	}
	family := s.Get("family")
	if family == "" && e.File != nil {
		for _, other := range e.File.Steps {
			if v := other.Get("os"); v != "" {
				family = v
				break
			}
		}
	}

	spec := asset.Spec{
		Kind:     asset.Kind(kind),
		Family:   family,
		Work:     e.Work,
		Template: s.Get("template"),
		Release:  s.Get("release"),
		// THE RECOVERY IMAGE IS AN INPUT TO THE OPENCORE BUILD, not a device this step gives a
		// guest: Apple's APFS driver comes out of that image, and OpenCore cannot read an APFS
		// partition without it. The two are separate facts, which is why this is a path here and a
		// `recovery:` on a boot step.
		Recovery: e.assetPath(s.Get("recovery")),
	}
	if spec.Release == "" && e.File != nil {
		if v, ok := e.File.Vars["release"]; ok {
			spec.Release = str(v)
		}
	}
	// THE ANSWER FILE'S PLACEHOLDERS. `{{ user }}` and `{{ password }}` are resolved by the
	// walkthrough loader, so a file writes them the same way it writes every other fact; these
	// are the WINDOWS names the template uses.
	if e.File != nil {
		vars := e.File.Vars
		spec.Values = map[string]string{
			"__ADMIN_USER__":          str(vars["user"]),
			"__ADMIN_PASSWORD__":      str(vars["password"]),
			"__COMPUTER_NAME__":       str(vars["computer_name"]),
			"__WINDOWS_IMAGE_INDEX__": str(vars["windows_image_index"]),
		}
	}

	res, err := asset.Fetch(spec)
	if err != nil {
		return fail(s.Name, "%v", err)
	}
	e.Driver.Log(fmt.Sprintf("  ready: %s", res.Main))
	for _, extra := range res.Extra {
		e.Driver.Log(fmt.Sprintf("  also: %s", extra))
	}
	// THE STEPS AFTER THIS ONE NAME THIS FILE, so it is remembered rather than guessed: a `boot:`
	// with `answer_cd: auto` or a later step asking "where is the answer CD" gets the truth.
	if e.assets == nil {
		e.assets = map[string]string{}
	}
	if e.assetExtras == nil {
		e.assetExtras = map[string][]string{}
	}
	e.assets[kind] = res.Main
	if len(res.Extra) > 0 {
		e.assetExtras[kind] = res.Extra
	}
	// THE ANSWER CD IS BUILT FROM THE VENDOR ISO, and the boot step needs that ISO attached as the
	// driver CD - "the same drivers again, for the installed system". Recording it here means the
	// file is named in ONE place (the asset layer) rather than spelled into the YAML.
	if kind == string(asset.AnswerISO) {
		for _, extra := range res.Extra {
			if strings.HasSuffix(extra, "virtio-win.iso") {
				e.assets[string(asset.VirtioWin)] = extra
			}
		}
	}
	return nil
}

// str renders a resolved variable for a placeholder. An absent one is the empty string, and a
// placeholder left empty is caught by answer.Build rather than here - the message belongs where the
// file is read, not where its value is missing.
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// --- macOS's own steps ----------------------------------------------------------------

// doWaitFor waits for a control with no readable title.
//
// THE LOGIN WINDOW'S ONLY TEXT IS THE PASSWORD FIELD'S PLACEHOLDER, twelve pixels tall in a 165x40
// box. A whole-frame read returns NOTHING on a finished Setup Assistant, which is why a walk whose
// gate lists fifteen pane titles waits out its entire budget on a screen that is already done.
func (e *Executor) doWaitFor(s walk.Step) error {
	what := s.Get("wait_for")
	if what != "login_window" {
		return fail(s.Name, "`wait_for:` knows login_window only; got %q", what)
	}
	timeout := seconds(s, "timeout", 240)
	poll := seconds(s, "poll", 3)

	e.Driver.Log(fmt.Sprintf("  waiting up to %s for the login window", timeout))
	start := time.Now()
	for time.Since(start) < timeout {
		f, err := e.Driver.Frame()
		if err != nil {
			time.Sleep(poll)
			continue
		}
		img, err := screen.Load(f.Path())
		if err != nil {
			time.Sleep(poll)
			continue
		}
		if screen.HasLoginWindow(img) {
			e.Driver.Log(fmt.Sprintf("  the login window is up (%s)",
				time.Since(start).Round(time.Second)))
			return nil
		}
		time.Sleep(poll)
	}
	// THE CEILING IS NOT A FAILURE *WHEN THE FILE SAYS optional*. `wait_for` returning an error
	// either way, with the tolerance baked in, made the file's own `optional:` a decoration - and
	// the two disagreed about who decided. The decision is the file's, so this reports and lets
	// the walk decide.
	return fmt.Errorf("%s: no login window within %s", s.Name, timeout)
}

// doScreenshot saves the screen, for a step that asks for the picture rather than a failure taking
// one.
func (e *Executor) doScreenshot(s walk.Step) error {
	dest := s.Get("screenshot")
	if dest == "" {
		return fail(s.Name, "`screenshot:` must name a path")
	}
	f, err := e.Driver.Frame()
	if err != nil {
		return fail(s.Name, "%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fail(s.Name, "%v", err)
	}
	src, err := os.ReadFile(f.Path())
	if err != nil {
		return fail(s.Name, "%v", err)
	}
	if err := os.WriteFile(dest, src, 0o644); err != nil {
		return fail(s.Name, "%v", err)
	}
	e.Driver.Log(fmt.Sprintf("  screenshot: %s", dest))
	return nil
}

// doDemoScript writes a throwaway script, for the primitives walkthrough.
func (e *Executor) doDemoScript(s walk.Step) error {
	if s.Get("demo_script") == "" {
		return fail(s.Name, "`demo_script:` must name the file to write")
	}
	return notImplemented(s, "demo_script")
}

// --- the compound action ------------------------------------------------------------------
//
// `do:` IS A LIST OF ACTIONS THAT BELONG TO ONE STEP, and it exists for a step whose NAME is the
// thing being done - "dismiss the overlays macOS puts up on a first login" is three clicks four
// seconds apart, and splitting it into three named steps would name the clicks rather than the deed.
//
// IT IS NOT A LANE OR A PANE. Those are the four-key state machines (`when`/`once`/`end`/`panes:`) for
// a GUI that has to be answered until it stops asking; this is one step, run once, whose actions
// happen in order.
//
// MEASURED ON macos-ssh, step 10 of 33:
//
//	error:   dismiss the overlays macOS puts up on a first login: no implementation for this step's keys
//
// The key was declared in the vocabulary and had no dispatcher branch - the exact failure the
// vocabulary exists to refuse, arriving from the one direction it does not cover: a key that IS
// declared, IS valid YAML, and does nothing.
func (e *Executor) doActions(s walk.Step) error {
	raw, ok := s.Spec["do"].([]any)
	if !ok || len(raw) == 0 {
		return fail(s.Name, "`do:` must be a list of actions")
	}
	for i, item := range raw {
		spec, ok := item.(map[string]any)
		if !ok {
			return fail(s.Name, "`do:` item %d is not a mapping", i+1)
		}
		// EACH ACTION IS A STEP WITH THE PARENT'S NAME AND ITS OWN KEYS, so `at:`, `settle:`, `key:`,
		// `type:`, `until:` and the rest all work without a second dispatcher.
		sub := walk.Step{
			Index: s.Index,
			Name:  fmt.Sprintf("%s [%d]", s.Name, i+1),
			Spec:  make(map[string]any, len(spec)+1),
		}
		for k, v := range spec {
			sub.Spec[k] = v
		}
		if err := e.runOne(sub); err != nil {
			return err
		}
	}
	return e.settle(s)
}

// --- snapshots ---------------------------------------------------------------------
//
// `snapshot:` WRITES THE MACHINE'S WHOLE STATE TO A FILE, so a later run can start from it instead of
// booting. It is the one primitive that makes a sandbox's "pause" mean something: the guest's memory,
// its devices and its UEFI variables all go into one stream.
//
// WHY IT IS A MIGRATION AND NOT `savevm` - the guest refuses that, and the reason is structural:
//
//	savevm s1  ->  Error: Device 'pflash1' is writable but does not support snapshots
//
// Every guest here boots UEFI, and UEFI's variable store is a pflash device - a firmware chip, not a
// qcow2 - so `savevm`, which writes the state INTO a block device, has nowhere to put it. A migration
// stream is a file of its own and asks nothing of the devices.
//
// THE STEP TAKES A PATH AND NOTHING ELSE, because everything else about a resume belongs to the boot
// that reads it: the cpu, the machine type and the device set must match, and this build gives every
// guest the same ones. A `snapshot:` that also recorded the machine would be a second description of
// the machine, and the two would disagree.
func (e *Executor) doSnapshot(s walk.Step) error {
	dest := s.Get("snapshot")
	if strings.TrimSpace(dest) == "" {
		return fail(s.Name, "`snapshot:` must name the file to write the machine state to")
	}
	// A RELATIVE PATH IS RESOLVED AGAINST THE WORK DIRECTORY, the same rule `scp:` and `disk:`
	// follow: a file that a later `run --boot-from` names lives where the daemon put it.
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(e.Work, dest)
	}
	sv, ok := e.Driver.(interface{ Snapshot(string) error })
	if !ok {
		return notImplemented(s, "snapshot")
	}
	if err := sv.Snapshot(dest); err != nil {
		return fail(s.Name, "%v", err)
	}
	return nil
}

// resSwitchBudget is how long a desktop at the wrong size is given to switch to the right one.
//
// IT IS NOT THE `timeout:` OF THE STEP, DELIBERATELY. The step's timeout is how long the guest may
// take to draw a desktop AT ALL - ninety minutes for a live medium that unpacks itself from a
// multi-gigabyte ISO. The mode switch happens once the panel is up, and a session that has drawn its
// panel and stayed at 800x600 has no other mode to offer. Two minutes is generous for the ones that
// do switch and bounded for the ones that never will.
const resSwitchBudget = 2 * time.Minute

// resolutionOf is the guest screen size a file's coordinates were measured at.
//
// IT IS THE FILE'S OWN `resolution:`, and 1920x1080 when the file does not say - which is the size
// every walkthrough here was measured on. A guest that has not switched to it is a guest whose
// coordinates mean something else entirely.
func resolutionOf(f *walk.File) [2]int {
	if f != nil && f.Resolution[0] > 0 && f.Resolution[1] > 0 {
		return f.Resolution
	}
	return [2]int{1920, 1080}
}

// wakeTheScreen nudges the guest with a click so a blanked display comes back.
//
// IT MOVES EACH TIME, because a click at exactly the same place twice is a double-click to some
// toolkits and would open whatever is under it. The x walks a 40-pixel band, which is enough to be a
// different position and small enough to stay on the wallpaper of every desktop here.
//
// THE COST IS ONE CLICK PER POLL, and it is paid by both waits that use it: a `desktop:` polling
// every five seconds and a `disk_settle:` every twenty. That is deliberate - this is what makes an
// unattended wait survive a screen saver, and `keep_alive:` used to make it OPTIONAL while every one
// of its 25 uses said yes.
func wakeTheScreen(e *Executor) {
	x := 400 + int(time.Now().UnixNano()/int64(time.Second))%40
	_ = e.Driver.Click(x, 300)
}
