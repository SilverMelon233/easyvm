package screen

import (
	"fmt"
	"image"
)

// ANDROID'S DESKTOP DOES NOT LOOK LIKE A LINUX PANEL, AND `IsLinuxDesktop` SAYS SO - CORRECTLY.
//
// MEASURED on a BlissOS guest this repository installed, on a frame that is unambiguously its
// desktop:
//
//	row     mean   spread   what is there
//	 936    22.3    203.0   wallpaper
//	 954    12.4    121.7   Taskbar's top edge
//	 972    23.2    232.7   Taskbar's icons
//	1026    12.8    121.3   Taskbar's right half
//
// and `IsLinuxDesktop` wants `margin > 40 && spread < 42 && sat > 0.05` against its own bottom 4.5%.
// Android's Taskbar is a 40 px translucent bar over a dark wallpaper: the contrast against the body
// is 9, not 40, and its spread is 121 because it has icons in it. A Linux panel - one flat colour
// along the whole width - has neither problem, which is why that test serves the five distributions
// it was measured on and cannot serve this one.
//
// SO THIS IS A SECOND MEASUREMENT RATHER THAN A LOOSER THRESHOLD. Loosening `spread < 42` to `200`
// would make a photograph's edge count as a panel; changing `margin > 40` to `> 5` would do the same
// to a gradient. What is measured here is something else entirely:
//
//	A DESKTOP HAS A BAND OF ICONS ALONG THE BOTTOM EDGE, AND NOTHING ELSE A GUEST SHOWS DOES.
//
// The boot splash is a centred logo on black; the home-app chooser is a dialog in the middle of an
// empty frame; a lock screen is one full-bleed wallpaper. None of them puts a row of high-contrast
// objects against the bottom edge, and all of them fail the two conditions below.
//
// THE CONDITIONS, BOTH NEEDED:
//
//	1  THE BOTTOM EDGE DIFFERS FROM THE BODY. Mean brightness of the lowest 12% against the 35-65%
//	   band. Android's Taskbar is DARKER than the wallpaper, so this is a difference and not a drop.
//	2  THE BOTTOM EDGE IS BUSY. A Linux panel is flat; an Android Taskbar holds six icons and a
//	   clock, so its mean absolute deviation is several times a panel's. This is what a wallpaper
//	   edge also has - which is why condition 1 has to hold as well, and why neither alone is enough.

// AndroidStats is that measurement, in numbers a failure message can print.
type AndroidStats struct {
	Width, Height int
	// BodyMean is the mean brightness of the frame's middle band.
	BodyMean float64
	// EdgeMean and EdgeSpread are the lowest 12% - where Taskbar draws.
	EdgeMean   float64
	EdgeSpread float64
	// EdgeContrast is the fraction of the bottom rows that are not the body's brightness.
	EdgeContrast float64
}

// Describe is one line a failure message can print, in the same shape the other detectors use.
func (s AndroidStats) Describe() string {
	return fmt.Sprintf("%dx%d: in the lowest rows %.0f%% are off the body's %.1f (need %.0f%%), "+
		"and adjacent rows move %.2f (need %.1f); the last row is %.1f",
		s.Width, s.Height, s.EdgeContrast*100, s.BodyMean,
		androidMinOffBody*100, s.EdgeSpread, androidMinStep, s.EdgeMean)
}

// IsAndroidDesktop reports whether a frame is Android's desktop, and returns the numbers either way.
//
// THE MEASUREMENT IS A ROW PROFILE, NOT A BAND AVERAGE, AND THAT IS THE WHOLE POINT.
//
// The first version of this took the mean of the lowest 12% and compared it with the body - and it
// answered "contrast 0.7" on a frame that plainly has a Taskbar. The reason is in the profile:
//
//	row     mean    what is there
//	 936    22.3    wallpaper
//	 954    12.4    Taskbar's top edge, DARK
//	 972    23.2    Taskbar's icons, BRIGHT
//	1026    12.8    Taskbar's right half, DARK
//
// The bar is 96 px tall and its own contents range from 12 to 25, so an average over the band lands
// back on the wallpaper's 22 and the difference vanishes. A DESKTOP'S SIGNATURE IS THE VARIATION,
// not the level: rows near the bottom that are far from the body's brightness, and adjacent rows
// that differ from each other.
func IsAndroidDesktop(img image.Image) (bool, AndroidStats) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	st := AndroidStats{Width: w, Height: h}
	if w == 0 || h == 0 {
		return false, st
	}

	const samples = 160
	body, _ := bandStats(img, w, h, 0.35, 0.65)
	st.BodyMean = sum(body) / 3.0

	// THE LOWEST 15%, ROW BY ROW. The Taskbar measured above spans y 954-1050 of 1080, which is
	// 8.9%; 15% gives room for a taller bar and for the gesture line beneath it.
	y0 := int(float64(h) * 0.85)
	var rows []float64
	for y := y0; y < h; y++ {
		var total float64
		for i := 0; i < samples; i++ {
			x := b.Min.X + i*w/samples
			r, g, bl, _ := img.At(x, b.Min.Y+y).RGBA()
			total += (float64(r>>8) + float64(g>>8) + float64(bl>>8)) / 3.0
		}
		rows = append(rows, total/float64(samples))
	}
	if len(rows) < 2 {
		return false, st
	}

	// HOW MANY ROWS ARE NOT THE BODY'S BRIGHTNESS. A wallpaper's own rows are all within a few
	// levels of it; Taskbar's edges are 10 below and its icons a few above.
	offBody := 0
	for _, v := range rows {
		if d := v - st.BodyMean; d > androidRowDelta || d < -androidRowDelta {
			offBody++
		}
	}
	st.EdgeContrast = float64(offBody) / float64(len(rows))

	// AND HOW MUCH THE PROFILE MOVES BETWEEN ADJACENT ROWS, AVERAGED OVER THE WHOLE BAND.
	//
	// TWO OTHER MEASUREMENTS WERE TRIED AND BOTH FAILED ON REAL FRAMES:
	//
	//	the mean over every row            0.08 on a synthetic bar and 1.17 on the real desktop,
	//	                                   because a bar is ~100 rows and its icons move the row mean
	//	                                   on a dozen - dividing by 160 buries them
	//	the LARGEST step anywhere          35-39 on EVERY frame measured, desktop or not, because the
	//	                                   last rows of every guest screen are the framebuffer's own
	//	                                   black edge - a cliff no threshold can tell from a bar
	//
	// SO SPREAD IS THE MEAN, AND IT IS NOT ASKED TO DISTINGUISH ON ITS OWN. What separates a desktop
	// here is the two conditions TOGETHER: a large fraction of rows off the body's brightness (a bar
	// and its icons) AND rows that move between neighbours (icons, not a flat strip). The boot splash
	// has neither; the chooser has neither.
	var moved float64
	for i := 1; i < len(rows); i++ {
		d := rows[i] - rows[i-1]
		if d < 0 {
			d = -d
		}
		moved += d
	}
	st.EdgeSpread = moved / float64(len(rows)-1)
	st.EdgeMean = rows[len(rows)-1]

	//	offBody >= 0.05   at least one row in twenty is not the wallpaper's brightness
	//	step    >= 1.0    and the profile steps rather than drifts
	return st.EdgeContrast >= androidMinOffBody && st.EdgeSpread >= androidMinStep, st
}

// The thresholds, named so `Describe` and any test print the numbers the check uses.
const (
	// androidRowDelta is how far a row has to be from the body before it counts as "not wallpaper".
	// Measured: the Taskbar's edges are 9.9 below it.
	androidRowDelta = 4.0
	// androidMinOffBody is the fraction of the bottom rows that must be off-body. The measured
	// desktop gives 0.35; a uniform frame gives 0.
	androidMinOffBody = 0.05
	// androidMinStep is the mean absolute difference between adjacent rows in the bottom band.
	// Measured: the real desktop gives 1.17; a uniform frame and a smooth gradient give under 1.
	androidMinStep = 1.0
)
