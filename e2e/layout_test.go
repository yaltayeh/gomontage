//go:build e2e

package e2e

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/timeline"
)

// testFont returns the committed El Messiri font file and makes fontconfig
// (used by drawtext's font= option) find it by family name, independent of
// the fonts installed on the machine.
func testFont(t *testing.T) string {
	t.Helper()
	requireFilter(t, "drawtext")
	dir, err := filepath.Abs(filepath.Join("..", "testdata", "fonts"))
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(t.TempDir(), "fonts.conf")
	cache := filepath.Join(t.TempDir(), "fc-cache")
	xml := fmt.Sprintf(`<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<fontconfig><dir>%s</dir><cachedir>%s</cachedir></fontconfig>
`, dir, cache)
	if err := os.WriteFile(conf, []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FONTCONFIG_FILE", conf)
	return filepath.Join(dir, "ElMessiri.ttf")
}

// bbox returns the bounding box of the pixels within tol of want.
func bbox(img image.Image, want RGB, tol int) image.Rectangle {
	var r image.Rectangle
	first := true
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			if colorClose(RGB{int(cr >> 8), int(cg >> 8), int(cb >> 8)}, want, tol) {
				p := image.Rect(x, y, x+1, y+1)
				if first {
					r, first = p, false
				} else {
					r = r.Union(p)
				}
			}
		}
	}
	return r
}

// redBaseSized returns a w×h timeline with a solid red base video.
func redBaseSized(t *testing.T, w, h int, d time.Duration) *timeline.Timeline {
	t.Helper()
	tl := timeline.New(timeline.Config{Width: w, Height: h, FPS: 30})
	red := genVideo(t, VideoSpec{W: w, H: h, Color: "0xFF0000", Dur: d})
	tl.AddVideoTrack("base").Add(clip.NewVideo(red), 0)
	return tl
}

var allAnchors = []clip.Anchor{
	clip.AnchorTopLeft, clip.AnchorTop, clip.AnchorTopRight,
	clip.AnchorLeft, clip.AnchorCenter, clip.AnchorRight,
	clip.AnchorBottomLeft, clip.AnchorBottom, clip.AnchorBottomRight,
}

// A blue 16:9 logo with Width 0.2 at (0.3, 0.6) lands where the layout math
// says for every anchor, at 16:9 and after changing the format to 9:16.
func TestLayout_LogoAnchorsAndFormatChange(t *testing.T) {
	requireFFmpeg(t)
	logo := genOpaquePNG(t, 64, 36, "0x0000FF")
	const slot = 400 * time.Millisecond

	for _, f := range []struct {
		name string
		w, h int
	}{{"16x9", 320, 180}, {"9x16", 180, 320}} {
		t.Run(f.name, func(t *testing.T) {
			tl := redBaseSized(t, f.w, f.h, slot*time.Duration(len(allAnchors)))
			ov := tl.AddVideoTrack("logo")
			for i, a := range allAnchors {
				ov.Add(clip.NewImage(logo).WithDuration(slot).
					WithLayout(clip.Layout{X: 0.3, Y: 0.6, Anchor: a, Width: 0.2}), slot*time.Duration(i))
			}
			out := renderMP4(t, tl)

			bw := int(0.2*float64(f.w) + 0.5)
			bh := int(float64(bw)*36/64 + 0.5)
			for i, a := range allAnchors {
				ax, ay := a.Fractions()
				x0 := int(0.3*float64(f.w) - ax*float64(bw) + 0.5)
				y0 := int(0.6*float64(f.h) - ay*float64(bh) + 0.5)
				want := image.Rect(x0, y0, x0+bw, y0+bh)
				got := bbox(frameAt(t, out, slot*time.Duration(i)+slot/2), Blue, 60)
				if abs(got.Min.X-want.Min.X) > 2 || abs(got.Min.Y-want.Min.Y) > 2 || abs(got.Max.X-want.Max.X) > 2 || abs(got.Max.Y-want.Max.Y) > 2 {
					t.Errorf("anchor %s: logo at %v, want %v", a, got, want)
				}
			}
			// The base stays visible around the logo.
			assertPixel(t, out, slot/2, f.w-3, 3, Red, colorTol)
		})
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// A square logo in a 0.5×0.5 box: contain keeps it square, cover and stretch
// fill the box.
func TestLayout_SizeFit(t *testing.T) {
	requireFFmpeg(t)
	sq := genOpaquePNG(t, 40, 40, "0x0000FF")
	for _, tc := range []struct {
		fit  clip.Fit
		want image.Rectangle
	}{
		{clip.FitContain, image.Rect(115, 45, 205, 135)}, // 90×90 centred
		{clip.FitCover, image.Rect(80, 45, 240, 135)},    // 160×90
		{clip.FitStretch, image.Rect(80, 45, 240, 135)},  // 160×90
	} {
		t.Run(string(tc.fit), func(t *testing.T) {
			tl := redBaseSized(t, 320, 180, time.Second)
			tl.AddVideoTrack("logo").Add(clip.NewImage(sq).WithDuration(time.Second).
				WithLayout(clip.Layout{X: 0.5, Y: 0.5, Width: 0.5, Height: 0.5, Fit: tc.fit}), 0)
			got := bbox(frameAt(t, renderMP4(t, tl), 500*time.Millisecond), Blue, 60)
			if abs(got.Min.X-tc.want.Min.X) > 2 || abs(got.Max.X-tc.want.Max.X) > 2 || abs(got.Min.Y-tc.want.Min.Y) > 2 || abs(got.Max.Y-tc.want.Max.Y) > 2 {
				t.Errorf("%s: %v, want %v", tc.fit, got, tc.want)
			}
		})
	}
}

// Layout on the base track: the clip is placed on black.
func TestLayout_BaseTrackClip(t *testing.T) {
	requireFFmpeg(t)
	tl := timeline.New(timeline.Config{Width: 320, Height: 180, FPS: 30})
	blue := genVideo(t, VideoSpec{Color: "0x0000FF", Dur: time.Second})
	tl.AddVideoTrack("base").Add(clip.NewVideo(blue).WithLayout(clip.Layout{X: 1, Y: 1, Anchor: clip.AnchorBottomRight, Width: 0.25}), 0)
	out := renderMP4(t, tl)
	got := bbox(frameAt(t, out, 500*time.Millisecond), Blue, 60)
	if want := image.Rect(240, 135, 320, 180); abs(got.Min.X-want.Min.X) > 2 || abs(got.Min.Y-want.Min.Y) > 2 || got.Max != want.Max {
		t.Errorf("clip at %v, want %v", got, want)
	}
	assertPixel(t, out, 500*time.Millisecond, 10, 10, Black, colorTol)
}

// Translucent color clips blend with the layer beneath (upper track) or
// with black (base track).
func TestLayout_ColorAlpha(t *testing.T) {
	requireFFmpeg(t)
	tl := redBaseSized(t, 320, 180, time.Second)
	tl.AddVideoTrack("tint").Add(clip.NewColor("#0000FF80", 0, 0).WithDuration(time.Second), 0)
	assertPixel(t, renderMP4(t, tl), 500*time.Millisecond, 160, 90, RGB{127, 0, 128}, colorTol)

	base := timeline.New(timeline.Config{Width: 320, Height: 180, FPS: 30})
	base.AddVideoTrack("v").Add(clip.NewColor("#FFFFFF80", 0, 0).WithDuration(time.Second), 0)
	assertPixel(t, renderMP4(t, base), 500*time.Millisecond, 160, 90, RGB{128, 128, 128}, colorTol)
}

// Text with a box: the box (exactly the text box plus padding) lands at the
// anchor for every anchor; outside the box the base shows through.
func TestText_AnchorsAndBox(t *testing.T) {
	font := testFont(t)
	green := RGB{0, 255, 0}
	const slot = 400 * time.Millisecond
	tl := redBaseSized(t, 480, 270, slot*time.Duration(len(allAnchors)))
	tr := tl.AddVideoTrack("text")
	for i, a := range allAnchors {
		st := clip.TextStyle{Font: font, Size: 40, Color: "#FFFFFF", Background: "#00FF00", BoxPadding: 6}
		tr.Add(clip.NewText("Hi", st).WithDuration(slot).WithLayout(clip.Layout{X: 0.4, Y: 0.6, Anchor: a}), slot*time.Duration(i))
	}
	out := renderMP4(t, tl)
	px, py := 0.4*480, 0.6*270
	for i, a := range allAnchors {
		ax, ay := a.Fractions()
		box := bbox(frameAt(t, out, slot*time.Duration(i)+slot/2), green, 60)
		w, h := float64(box.Dx()), float64(box.Dy())
		if box.Empty() || w > 200 || h > 120 {
			t.Fatalf("anchor %s: box %v (missing or not just behind the text)", a, box)
		}
		if dx := float64(box.Min.X) + ax*w - px; dx < -2 || dx > 2 {
			t.Errorf("anchor %s: box %v, horizontal anchor off by %.1f px", a, box, dx)
		}
		if dy := float64(box.Min.Y) + ay*h - py; dy < -2 || dy > 2 {
			t.Errorf("anchor %s: box %v, vertical anchor off by %.1f px", a, box, dy)
		}
	}
	assertPixel(t, out, slot/2, 5, 5, Red, colorTol)
}

// An outline adds dark pixels around the glyphs; text color alpha blends the
// glyphs with the layer beneath.
func TestText_OutlineAndColorAlpha(t *testing.T) {
	font := testFont(t)
	render := func(st clip.TextStyle) image.Image {
		tl := redBaseSized(t, 320, 180, time.Second)
		st.Font, st.Size = font, 60
		tl.AddVideoTrack("t").Add(clip.NewText("HIH", st).WithDuration(time.Second), 0)
		return frameAt(t, renderMP4(t, tl), 500*time.Millisecond)
	}
	plain := render(clip.TextStyle{Color: "#FFFFFF"})
	outlined := render(clip.TextStyle{Color: "#FFFFFF", BorderWidth: 4, BorderColor: "#000000"})
	area := bbox(plain, RGB{255, 255, 255}, 40).Inset(-6)
	if n := countNear(plain, area, Black, 40); n != 0 {
		t.Errorf("no outline: %d black pixels around the text", n)
	}
	if n := countNear(outlined, area, Black, 40); n < 50 {
		t.Errorf("outline: only %d black pixels around the text", n)
	}

	translucent := render(clip.TextStyle{Color: "#FFFFFF80"})
	// Pixels fully covered by glyphs in the opaque render are a 50% blend
	// of white and the red base in the translucent one.
	checked := 0
	b := area
	for y := b.Min.Y; y < b.Max.Y && checked < 20; y++ {
		for x := b.Min.X; x < b.Max.X && checked < 20; x++ {
			r, g, bl, _ := plain.At(x, y).RGBA()
			if r>>8 > 250 && g>>8 > 250 && bl>>8 > 250 {
				cr, cg, cb, _ := translucent.At(x, y).RGBA()
				got := RGB{int(cr >> 8), int(cg >> 8), int(cb >> 8)}
				if !colorClose(got, RGB{255, 128, 128}, colorTol) {
					t.Errorf("glyph pixel (%d,%d) = %s, want ≈ rgb(255,128,128)", x, y, got)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no fully covered glyph pixels found")
	}
}

// Arabic: "قريباً" is shaped (joined letters differ from the same letters
// forced apart with ZWNJ) and drawn with real glyphs (differs from missing-
// glyph boxes); a mixed line with direction rtl reads right to left.
func TestText_ArabicShapingAndRTL(t *testing.T) {
	font := testFont(t)
	frame := func(text, dir, family string) image.Image {
		tl := timeline.New(timeline.Config{Width: 480, Height: 160, FPS: 30})
		st := clip.TextStyle{Size: 56, Color: "#FFFFFF", Direction: dir}
		if family != "" {
			st.FontFamily = family
		} else {
			st.Font = font
		}
		tl.AddVideoTrack("t").Add(clip.NewText(text, st).WithDuration(time.Second), 0)
		return frameAt(t, renderMP4(t, tl), 500*time.Millisecond)
	}
	ink := func(img image.Image) int { return countNear(img, img.Bounds(), RGB{255, 255, 255}, 60) }
	same := func(a, b image.Image) bool {
		diff := 0
		bounds := a.Bounds()
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				r1, g1, b1, _ := a.At(x, y).RGBA()
				r2, g2, b2, _ := b.At(x, y).RGBA()
				if !colorClose(RGB{int(r1 >> 8), int(g1 >> 8), int(b1 >> 8)}, RGB{int(r2 >> 8), int(g2 >> 8), int(b2 >> 8)}, 40) {
					diff++
				}
			}
		}
		return diff < 20
	}

	shaped := frame("قريباً", "", "")
	if ink(shaped) < 200 {
		t.Fatalf("Arabic text not drawn (%d ink pixels)", ink(shaped))
	}
	if same(shaped, frame("ق‌ر‌ي‌ب‌ا‌ً", "", "")) {
		t.Error("Arabic letters are not joined (no shaping)")
	}
	if same(shaped, frame("", "", "")) {
		t.Error("Arabic text renders like missing-glyph boxes")
	}
	// The family name resolves to the same font through fontconfig.
	if !same(shaped, frame("قريباً", "", "El Messiri")) {
		t.Error("font family \"El Messiri\" doesn't render like the font file")
	}

	// RTL: an RTL paragraph "Hello مرحبا" displays as "مرحبا Hello", i.e. the
	// same as the LTR paragraph "مرحبا Hello"; the LTR paragraph
	// "Hello مرحبا" (auto, first strong letter is Latin) is different.
	rtl := frame("Hello مرحبا", "rtl", "")
	if !same(rtl, frame("مرحبا Hello", "ltr", "")) {
		t.Error(`rtl "Hello مرحبا" should display as "مرحبا Hello"`)
	}
	if same(rtl, frame("Hello مرحبا", "auto", "")) {
		t.Error("direction rtl had no effect on a mixed line")
	}
}

// Text alignment of multi-line text: right-aligned lines share their right
// edge, left-aligned lines their left edge.
func TestText_Align(t *testing.T) {
	font := testFont(t)
	edges := func(align string) (top, bottom image.Rectangle) {
		tl := timeline.New(timeline.Config{Width: 480, Height: 200, FPS: 30})
		st := clip.TextStyle{Font: font, Size: 40, Color: "#FFFFFF", Align: align}
		tl.AddVideoTrack("t").Add(clip.NewText("HHHHHH\nHH", st).WithDuration(time.Second), 0)
		img := frameAt(t, renderMP4(t, tl), 500*time.Millisecond)
		all := bbox(img, RGB{255, 255, 255}, 60)
		mid := all.Min.Y + all.Dy()/2
		sub := func(r image.Rectangle) image.Rectangle {
			return bbox(img.(interface {
				SubImage(image.Rectangle) image.Image
			}).SubImage(r), RGB{255, 255, 255}, 60)
		}
		return sub(image.Rect(0, all.Min.Y, 480, mid)), sub(image.Rect(0, mid, 480, all.Max.Y))
	}
	top, bottom := edges("right")
	if abs(top.Max.X-bottom.Max.X) > 3 || bottom.Min.X-top.Min.X < 40 {
		t.Errorf("right: lines %v / %v", top, bottom)
	}
	top, bottom = edges("left")
	if abs(top.Min.X-bottom.Min.X) > 3 || top.Max.X-bottom.Max.X < 40 {
		t.Errorf("left: lines %v / %v", top, bottom)
	}
}
