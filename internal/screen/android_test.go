package screen

import (
	"image"
	"image/color"
	"testing"
)

// A DESKTOP'S SIGNATURE IS A ROW PROFILE THAT STEPS, NOT A BAND THAT IS DARKER.
//
// These cases are the shapes the real frames had, drawn rather than shipped, because the numbers
// are what matter and a 1920x1080 PNG in the repository is a megabyte for one assertion.
func frame(w, h int, draw func(x, y int) color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, draw(x, y))
		}
	}
	return img
}

func TestAUniformFrameIsNotADesktop(t *testing.T) {
	// A boot splash and a lock screen are one colour top to bottom.
	img := frame(1920, 1080, func(x, y int) color.RGBA {
		return color.RGBA{R: 8, G: 4, B: 16, A: 255}
	})
	if ok, st := IsAndroidDesktop(img); ok {
		t.Fatalf("a uniform frame read as a desktop: %s", st.Describe())
	}
}

func TestAGradientIsNotADesktop(t *testing.T) {
	// AND NEITHER IS A GRADIENT, which is what the looser thresholds would have accepted: it drifts
	// smoothly, so its rows are far from the body but adjacent rows barely move.
	img := frame(1920, 1080, func(x, y int) color.RGBA {
		v := uint8(10 + y*40/1080)
		return color.RGBA{R: v, G: v, B: v, A: 255}
	})
	if ok, st := IsAndroidDesktop(img); ok {
		t.Fatalf("a smooth gradient read as a desktop: %s", st.Describe())
	}
}

func TestABarWithIconsIsADesktop(t *testing.T) {
	// THE MEASURED SHAPE, DRAWN: a dark bar along the bottom with brighter glyphs in it, over
	// wallpaper. The real frame's profile is
	//
	//	row 936   22.3   wallpaper
	//	row 954   12.4   the bar's top edge
	//	row 972   23.2   the bar's icons
	//	row 1026  12.8   the bar's right half
	//
	// AN EVEN ALTERNATION ALONG EACH ROW DOES NOT REPRODUCE IT - measured twice, at step 0.14 and
	// 0.08 - because only the row's MEAN is compared, and a checkerboard has the same mean on every
	// row. The glyphs have to cover a different FRACTION of different rows, which is what a taller-
	// than-wide icon does.
	const wallpaper, bar, glyph = 22, 12, 25
	img := frame(1920, 1080, func(x, y int) color.RGBA {
		v := uint8(wallpaper)
		switch {
		case y >= 950 && y < 1040:
			// THE BAR, WITH A ROW OF ICONS INSIDE IT. Each icon spans 40 columns and its ink covers
			// a fraction of the row that changes as the row crosses the glyph's own height.
			v = bar
			if y >= 970 && y < 1010 {
				col := x / 40
				frac := (y - 970) // a triangle: wider ink in the middle rows
				if frac > 20 {
					frac = 40 - frac
				}
				if (x%40) < frac*2 && col%3 == 0 {
					v = glyph
				}
			}
		}
		return color.RGBA{R: v, G: v, B: v, A: 255}
	})
	ok, st := IsAndroidDesktop(img)
	if !ok {
		t.Fatalf("a bar with icons did not read as a desktop: %s", st.Describe())
	}
}
