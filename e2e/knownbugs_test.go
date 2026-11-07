//go:build e2e

package e2e

import (
	"fmt"
	"github.com/ahmedhodiani/gomontage/engine"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/effects"
	"github.com/ahmedhodiani/gomontage/export"
	"github.com/ahmedhodiani/gomontage/timeline"
)

// Known-bug tests assert the CORRECT behavior. They fail today and are skipped
// unless GOMONTAGE_KNOWN_BUGS=1. When a bug is fixed, delete its knownBug()
// line so the test becomes a permanent regression test.

// redBase returns a timeline with a 3s solid red video on track 0.
func redBase(t *testing.T) *timeline.Timeline {
	t.Helper()
	tl := newTL()
	red := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: 3 * time.Second})
	tl.AddVideoTrack("base").Add(clip.NewVideo(red), 0)
	return tl
}

// Bug 1 (fixed in phase 4): overlay tracks used to be opaque black outside
// their content — gaps, leading/tail padding, letterbox padding and text
// backgrounds. Upper tracks are now composited clip by clip with alpha
// (timeline/layout.go compileOverlayClip), so the base shows through.
func TestKnownBug_1_OverlayTrackOpaque(t *testing.T) {
	requireFFmpeg(t)

	t.Run("gap_and_tail_show_base", func(t *testing.T) {
		tl := redBase(t)
		// Full-canvas PNG: left half opaque blue, right half transparent.
		png := genAlphaPNG(t, W, H, 0, 0, 255)
		tl.AddVideoTrack("overlay").Add(clip.NewImage(png).WithDuration(time.Second), time.Second)
		out := renderMP4(t, tl)

		assertPixel(t, out, 500*time.Millisecond, cx, cy, Red, colorTol)    // before overlay
		assertPixel(t, out, 1500*time.Millisecond, 20, cy, Blue, colorTol)  // opaque half
		assertPixel(t, out, 1500*time.Millisecond, W-20, cy, Red, colorTol) // transparent half
		assertPixel(t, out, 2500*time.Millisecond, cx, cy, Red, colorTol)   // after overlay
	})

	t.Run("gap_between_overlay_clips", func(t *testing.T) {
		tl := redBase(t)
		png := genAlphaPNG(t, W, H, 0, 0, 255)
		ov := tl.AddVideoTrack("overlay")
		ov.Add(clip.NewImage(png).WithDuration(time.Second), 0)
		ov.Add(clip.NewImage(png).WithDuration(time.Second), 2*time.Second)
		out := renderMP4(t, tl)

		assertPixel(t, out, 1500*time.Millisecond, cx, cy, Red, colorTol)
	})

	t.Run("letterbox_pad_shows_base", func(t *testing.T) {
		tl := redBase(t)
		// Square opaque image: fit-to-canvas pads the sides. The padding on an
		// overlay track must be transparent, not black.
		sq := genOpaquePNG(t, 90, 90, "0x0000FF")
		tl.AddVideoTrack("overlay").Add(clip.NewImage(sq).WithDuration(3*time.Second), 0)
		out := renderMP4(t, tl)

		assertPixel(t, out, time.Second, 10, cy, Red, colorTol)
		assertPixel(t, out, time.Second, cx, cy, Blue, colorTol)
	})

	t.Run("text_background_transparent", func(t *testing.T) {
		requireFilter(t, "drawtext")
		tl := redBase(t)
		tl.AddVideoTrack("title").Add(clip.NewText("Hi", clip.DefaultTextStyle()).WithDuration(3*time.Second), 0)
		out := renderMP4(t, tl)

		// Far corner, away from the text: base video must remain visible.
		assertPixel(t, out, time.Second, W-5, H-5, Red, colorTol)
	})
}

// Bug 2 (fixed in phase 4): position and size used to be ignored. The legacy
// pixel API now places a clip's top-left corner at (X,Y) at its native size
// (or WithSize); see clip.ResolvePlacement. The canvas-relative Layout API is
// tested in layout_test.go.
func TestKnownBug_2_PositionAndSizeIgnored(t *testing.T) {
	requireFFmpeg(t)

	t.Run("image_with_position", func(t *testing.T) {
		tl := redBase(t)
		logo := genOpaquePNG(t, 64, 36, "0x0000FF")
		tl.AddVideoTrack("logo").Add(
			clip.NewImage(logo).WithDuration(3*time.Second).WithPosition(clip.Position{X: 200, Y: 100}), 0)
		out := renderMP4(t, tl)

		assertPixel(t, out, time.Second, 232, 118, Blue, colorTol) // inside logo
		assertPixel(t, out, time.Second, 5, 5, Red, colorTol)      // outside logo
		assertPixel(t, out, time.Second, 150, 118, Red, colorTol)  // left of logo
	})

	t.Run("image_with_size", func(t *testing.T) {
		tl := redBase(t)
		logo := genOpaquePNG(t, 64, 36, "0x0000FF")
		tl.AddVideoTrack("logo").Add(
			clip.NewImage(logo).WithDuration(3*time.Second).WithSize(32, 18).WithPosition(clip.Position{X: 0, Y: 0}), 0)
		out := renderMP4(t, tl)

		assertPixel(t, out, time.Second, 10, 8, Blue, colorTol)
		assertPixel(t, out, time.Second, 50, 30, Red, colorTol) // outside 32x18
	})

	t.Run("text_style_position", func(t *testing.T) {
		requireFilter(t, "drawtext")
		// Text over a base video, so this tests positioning on its own (a
		// text-only timeline would hit bug #4 instead). We look for white
		// glyph pixels rather than background color, so the opaque text
		// background of bug #1 doesn't affect the result.
		tl := redBase(t)
		style := clip.DefaultTextStyle()
		style.Size = 40
		style.Color = "#FFFFFF"
		style.BorderWidth = 0
		style.Position = clip.Position{X: 200, Y: 120}
		tl.AddVideoTrack("t").Add(clip.NewText("MMMM", style).WithDuration(3*time.Second), 0)
		out := renderMP4(t, tl)

		img := frameAt(t, out, time.Second)
		white := RGB{255, 255, 255}
		if n := countNear(img, image.Rect(0, 0, 120, 60), white, 60); n > 0 {
			t.Errorf("found %d white text pixels in the top-left corner; text should be at (200,120)", n)
		}
		if n := countNear(img, image.Rect(200, 120, W, H), white, 60); n == 0 {
			t.Errorf("no white text pixels at/after (200,120)")
		}
	})
}

// Bug 3 (fixed): drawtext text is escaped for the filtergraph
// (engine.EscapeFilterValue) and drawtext runs with expansion=none.
func TestKnownBug_3_DrawtextNotEscaped(t *testing.T) {
	requireFFmpeg(t)

	nasty := `Time: 5 o'clock; 100% \o/`
	arabic := "قريباً"

	// Structural check — runs without the drawtext filter. With correct
	// escaping, the filter graph must have the same shape as plain text.
	t.Run("graph_structure", func(t *testing.T) {
		src := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: time.Second})
		shape := func(text string) (chains int, drawtextKeys []string) {
			tl := newTL()
			tl.AddVideoTrack("base").Add(clip.NewVideo(src), 0)
			tl.AddVideoTrack("t").Add(clip.NewText(text, clip.DefaultTextStyle()).WithDuration(time.Second), 0)
			cmd, err := tl.DryRun(testProfile(), filepath.Join(t.TempDir(), "x.mp4"))
			if err != nil {
				t.Fatal(err)
			}
			fc := filterComplexArg(cmd.Args)
			parts := splitUnescaped(fc, ';')
			for _, p := range parts {
				if i := strings.Index(p, "drawtext="); i >= 0 {
					body := p[i+len("drawtext="):]
					if j := strings.LastIndex(body, "["); j >= 0 {
						body = body[:j]
					}
					// FFmpeg removes the filtergraph-level escaping before
					// splitting the option list on ':'.
					for _, kv := range splitUnescaped(unescapeOnce(body), ':') {
						drawtextKeys = append(drawtextKeys, strings.SplitN(kv, "=", 2)[0])
					}
				}
			}
			return len(parts), drawtextKeys
		}
		wantChains, wantKeys := shape("plain")
		for _, text := range []string{nasty, arabic} {
			gotChains, gotKeys := shape(text)
			if gotChains != wantChains {
				t.Errorf("%q: filter graph has %d chains, %d with plain text", text, gotChains, wantChains)
			}
			if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
				t.Errorf("%q: drawtext options parsed as %v, want %v", text, gotKeys, wantKeys)
			}
		}
	})

	// Render the text and compare against a reference render that feeds the
	// exact same string through drawtext's textfile= option (no escaping
	// involved). Identical decoded frames prove the text arrived verbatim.
	for name, text := range map[string]string{"special_chars": nasty, "arabic": arabic} {
		t.Run("render_exact_"+name, func(t *testing.T) {
			requireFilter(t, "drawtext")
			tl := redBase(t)
			style := clip.DefaultTextStyle()
			if name == "arabic" {
				// FFmpeg's default font has no Arabic glyphs (renders empty
				// boxes). Use a real Arabic font, copied to a path with
				// characters that also need escaping in fontfile=.
				style.Font = arabicFontInNastyPath(t)
			}
			tl.AddVideoTrack("t").Add(clip.NewText(text, style).WithDuration(time.Second), 0)

			dir := t.TempDir()
			out := filepath.Join(dir, "lib.mp4")
			if err := tl.ExportSilent(testProfile(), out); err != nil {
				t.Fatalf("export failed: %v", firstLine(err))
			}
			img := frameAt(t, out, 500*time.Millisecond)
			if n := countNear(img, image.Rect(0, 0, W, H), RGB{255, 255, 255}, 40); n == 0 {
				t.Fatalf("no white text pixels rendered")
			}

			// Reference: same command, text= replaced by textfile=.
			txt := filepath.Join(dir, "text.txt")
			if err := os.WriteFile(txt, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			ref := filepath.Join(dir, "ref.mp4")
			cmd, err := tl.DryRun(testProfile(), ref)
			if err != nil {
				t.Fatal(err)
			}
			needle := "text=" + engine.EscapeFilterValue(text)
			for i, a := range cmd.Args {
				if strings.Contains(a, needle) {
					cmd.Args[i] = strings.Replace(a, needle, "textfile="+engine.EscapeFilterValue(txt), 1)
				}
			}
			runFFmpeg(t, cmd.Args[1:]...) // drop the leading -y; runFFmpeg adds it

			md5 := func(p string) string {
				return string(runFFmpeg(t, "-v", "error", "-i", p, "-map", "0:v", "-c:v", "rawvideo", "-f", "md5", "-"))
			}
			if a, b := md5(out), md5(ref); a != b {
				t.Errorf("rendered text differs from textfile= reference (%s vs %s)", a, b)
			}
		})
	}
}

// Bug 4 (fixed): a timeline containing only generated clips (color/text)
// used to fail with "graph has no inputs" (engine/builder.go BuildCommand).
// Source filters now stand in for file inputs.
func TestKnownBug_4_GeneratedOnlyTimeline(t *testing.T) {
	requireFFmpeg(t)

	t.Run("color_sequence_with_gap", func(t *testing.T) {
		tl := newTL()
		v := tl.AddVideoTrack("v")
		v.Add(clip.NewColor("#FF0000", 0, 0).WithDuration(time.Second), 0)
		v.Add(clip.NewColor("#0000FF", 0, 0).WithDuration(time.Second), 2*time.Second)
		out := renderMP4(t, tl)
		assertDuration(t, "video", probe(t, out).Video().Duration, 3*time.Second, 30)
		assertPixel(t, out, 500*time.Millisecond, cx, cy, Red, colorTol)
		assertPixel(t, out, 1500*time.Millisecond, cx, cy, Black, colorTol)
		assertPixel(t, out, 2500*time.Millisecond, cx, cy, Blue, colorTol)
	})

	t.Run("color_only", func(t *testing.T) {
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewColor("#FF0000", 0, 0).WithDuration(time.Second), 0)
		out := filepath.Join(t.TempDir(), "x.mp4")
		if err := tl.ExportSilent(testProfile(), out); err != nil {
			t.Fatalf("export failed: %v", err)
		}
		assertDuration(t, "video", probe(t, out).Video().Duration, time.Second, 30)
		assertPixel(t, out, 500*time.Millisecond, cx, cy, Red, colorTol)
	})

	t.Run("text_only", func(t *testing.T) {
		requireFilter(t, "drawtext")
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewText("Title", clip.DefaultTextStyle()).WithDuration(time.Second), 0)
		out := filepath.Join(t.TempDir(), "x.mp4")
		if err := tl.ExportSilent(testProfile(), out); err != nil {
			t.Fatalf("export failed: %v", err)
		}
		assertDuration(t, "video", probe(t, out).Video().Duration, time.Second, 30)
		if n := countNear(frameAt(t, out, 500*time.Millisecond), image.Rect(0, 0, W, H), RGB{255, 255, 255}, 40); n == 0 {
			t.Error("no white text pixels in text-only render")
		}
	})
}

// Bug 5 (fixed): every video clip is normalized to the timeline FPS with an
// fps filter, including mixed-fps sequences (which used to crash mpeg4 with
// a 1/1000000 timebase).
func TestKnownBug_5_TimelineFPSIgnored(t *testing.T) {
	requireFFmpeg(t)

	for _, srcFPS := range []int{24, 60} {
		t.Run(fmt.Sprintf("%dfps_source", srcFPS), func(t *testing.T) {
			src := genVideo(t, VideoSpec{FPS: srcFPS, Dur: 2 * time.Second})
			tl := newTL() // 30 fps
			tl.AddVideoTrack("v").Add(clip.NewVideo(src), 0)
			v := probe(t, renderMP4(t, tl)).Video()
			if v.FPS < 29.9 || v.FPS > 30.1 {
				t.Errorf("output fps = %.3f, want 30 (source was %d)", v.FPS, srcFPS)
			}
			if v.Frames < 58 || v.Frames > 62 {
				t.Errorf("output frames = %d, want ≈60 for 2s @30fps", v.Frames)
			}
		})
	}

	t.Run("mixed_fps_sequence", func(t *testing.T) {
		a := genVideo(t, VideoSpec{FPS: 24, Dur: time.Second, Color: "0xFF0000"})
		b := genVideo(t, VideoSpec{FPS: 60, Dur: time.Second, Color: "0x0000FF"})
		tl := newTL()
		tl.AddVideoTrack("v").AddSequence(clip.NewVideo(a), clip.NewVideo(b))
		out := renderMP4(t, tl)
		v := probe(t, out).Video()
		if v.FPS < 29.9 || v.FPS > 30.1 {
			t.Errorf("output fps = %.3f, want 30", v.FPS)
		}
		if v.Frames < 58 || v.Frames > 62 {
			t.Errorf("output frames = %d, want ≈60 for 2s @30fps", v.Frames)
		}
		assertDuration(t, "video", v.Duration, 2*time.Second, 30)
		assertPixel(t, out, 500*time.Millisecond, cx, cy, Red, colorTol)
		assertPixel(t, out, 1500*time.Millisecond, cx, cy, Blue, colorTol)
	})
}

// Bug 6 (fixed): Profile.Width/Height set the output size. The timeline size
// is the composition canvas; the composite is scaled to fit the profile size
// (aspect preserved, black bars). See timeline.resolveOutputSize.
func TestKnownBug_6_ProfileResolutionIgnored(t *testing.T) {
	requireFFmpeg(t)

	red := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: time.Second})
	redTL := func() *timeline.Timeline {
		tl := newTL() // 320x180
		tl.AddVideoTrack("v").Add(clip.NewVideo(red), 0)
		return tl
	}

	t.Run("custom_profile_upscale", func(t *testing.T) {
		p := testProfile()
		p.Width, p.Height = 640, 360
		out := render(t, redTL(), p, ".mp4")
		v := probe(t, out).Video()
		if v.Width != 640 || v.Height != 360 {
			t.Errorf("output %dx%d, want 640x360 from profile", v.Width, v.Height)
		}
		assertPixel(t, out, 500*time.Millisecond, 10, 10, Red, colorTol) // same aspect: no bars
	})

	t.Run("different_aspect_letterboxed", func(t *testing.T) {
		p := testProfile()
		p.Width, p.Height = 180, 320 // vertical output from a 16:9 timeline
		out := render(t, redTL(), p, ".mp4")
		v := probe(t, out).Video()
		if v.Width != 180 || v.Height != 320 {
			t.Fatalf("output %dx%d, want 180x320", v.Width, v.Height)
		}
		assertPixel(t, out, 500*time.Millisecond, 90, 10, Black, colorTol)  // top bar
		assertPixel(t, out, 500*time.Millisecond, 90, 160, Red, colorTol)   // content
		assertPixel(t, out, 500*time.Millisecond, 90, 310, Black, colorTol) // bottom bar
	})

	t.Run("width_only_keeps_timeline_aspect", func(t *testing.T) {
		p := testProfile()
		p.Width = 160
		v := probe(t, render(t, redTL(), p, ".mp4")).Video()
		if v.Width != 160 || v.Height != 90 {
			t.Errorf("output %dx%d, want 160x90", v.Width, v.Height)
		}
	})

	t.Run("no_profile_size_keeps_timeline", func(t *testing.T) {
		v := probe(t, render(t, redTL(), testProfile(), ".mp4")).Video()
		if v.Width != W || v.Height != H {
			t.Errorf("output %dx%d, want timeline %dx%d", v.Width, v.Height, W, H)
		}
	})

	t.Run("YouTube4K", func(t *testing.T) {
		requireEncoder(t, "libx264")
		v := probe(t, render(t, redTL(), export.YouTube4K(), ".mp4")).Video()
		if v.Width != 3840 || v.Height != 2160 {
			t.Errorf("output %dx%d, want 3840x2160", v.Width, v.Height)
		}
	})
}

// Bug 7 (fixed): compileAudioFromVideoEntry now applies the clip's
// fadeIn/fadeOut to embedded audio, not just to the picture.
func TestKnownBug_7_EmbeddedAudioIgnoresFades(t *testing.T) {
	requireFFmpeg(t)

	src := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: 3 * time.Second})
	tl := newTL()
	tl.AddVideoTrack("v").Add(clip.NewVideo(src).WithFadeIn(time.Second).WithFadeOut(time.Second), 0)
	out := renderMP4(t, tl)

	start := audioLevel(t, out, 0, 150*time.Millisecond)
	mid := audioLevel(t, out, 1200*time.Millisecond, 600*time.Millisecond)
	end := audioLevel(t, out, 2800*time.Millisecond, 150*time.Millisecond)
	if mid.MeanDB-start.MeanDB < 12 {
		t.Errorf("audio fade-in: start %.1f dB vs middle %.1f dB, want ≥12 dB quieter", start.MeanDB, mid.MeanDB)
	}
	if mid.MeanDB-end.MeanDB < 12 {
		t.Errorf("audio fade-out: end %.1f dB vs middle %.1f dB, want ≥12 dB quieter", end.MeanDB, mid.MeanDB)
	}

	// In a sequence (concat path), with a trimmed second clip.
	seq := newTL()
	seq.AddVideoTrack("v").AddSequence(
		clip.NewVideo(src).Trim(0, time.Second),
		clip.NewVideo(src).Trim(500*time.Millisecond, 2500*time.Millisecond).WithFadeIn(time.Second),
	)
	seqOut := renderMP4(t, seq)
	first := audioLevel(t, seqOut, 200*time.Millisecond, 600*time.Millisecond)
	secondStart := audioLevel(t, seqOut, time.Second, 150*time.Millisecond)
	secondLate := audioLevel(t, seqOut, 2300*time.Millisecond, 500*time.Millisecond)
	if first.MeanDB-secondStart.MeanDB < 12 {
		t.Errorf("sequence: second clip start %.1f dB vs first clip %.1f dB, want ≥12 dB quieter", secondStart.MeanDB, first.MeanDB)
	}
	if d := first.MeanDB - secondLate.MeanDB; d > 2 || d < -2 {
		t.Errorf("sequence: second clip after fade %.1f dB, want ≈ first clip %.1f dB", secondLate.MeanDB, first.MeanDB)
	}
}

// Bug 8 (fixed): effects.SpeedUp/SlowDown now also retime the clip's audio
// (atempo chain), unless the clip has an explicit effects.AudioSpeed, in
// which case the caller controls audio speed (backward compatible).
func TestKnownBug_8_SpeedUpAudioDrift(t *testing.T) {
	requireFFmpeg(t)

	src := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: 2 * time.Second})

	t.Run("single_clip", func(t *testing.T) {
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(src).WithEffect(effects.SpeedUp(2)), 0)
		m := probe(t, renderMP4(t, tl))
		assertDuration(t, "video", m.Video().Duration, time.Second, 30)
		assertDuration(t, "audio", m.Audio().Duration, time.Second, 30)
	})

	t.Run("sequence_stays_in_sync", func(t *testing.T) {
		blue := genVideo(t, VideoSpec{Color: "0x0000FF", ToneHz: 880, Dur: time.Second})
		tl := newTL()
		tl.AddVideoTrack("v").AddSequence(clip.NewVideo(src).WithEffect(effects.SpeedUp(2)), clip.NewVideo(blue))
		m := probe(t, renderMP4(t, tl))
		assertDuration(t, "video", m.Video().Duration, 2*time.Second, 30)
		assertDuration(t, "audio", m.Audio().Duration, 2*time.Second, 30)
	})

	t.Run("tone_ends_at_cut", func(t *testing.T) {
		// Sped-up tone clip (2s → 1s) followed by a silent clip: the tone
		// must stop at the cut, not spill into the second clip.
		silent := genVideo(t, VideoSpec{Color: "0x0000FF", Dur: time.Second})
		tl := newTL()
		tl.AddVideoTrack("v").AddSequence(clip.NewVideo(src).WithEffect(effects.SpeedUp(2)), clip.NewVideo(silent))
		out := renderMP4(t, tl)
		if l := audioLevel(t, out, 200*time.Millisecond, 600*time.Millisecond); l.MeanDB < -40 {
			t.Errorf("expected tone during sped-up clip, got %.1f dB", l.MeanDB)
		}
		if l := audioLevel(t, out, 1200*time.Millisecond, 600*time.Millisecond); l.MeanDB > -60 {
			t.Errorf("expected silence after the cut at 1s, got %.1f dB", l.MeanDB)
		}
		assertPixel(t, out, 1500*time.Millisecond, cx, cy, Blue, colorTol)
	})

	t.Run("slow_down_4x_chains_atempo", func(t *testing.T) {
		short := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: 500 * time.Millisecond})
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(short).WithEffect(effects.SlowDown(4)), 0)
		m := probe(t, renderMP4(t, tl))
		assertDuration(t, "video", m.Video().Duration, 2*time.Second, 30)
		assertDuration(t, "audio", m.Audio().Duration, 2*time.Second, 30)
	})

	t.Run("explicit_audiospeed_not_doubled", func(t *testing.T) {
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(src).WithEffect(effects.SpeedUp(2)).WithEffect(effects.AudioSpeed(2)), 0)
		m := probe(t, renderMP4(t, tl))
		assertDuration(t, "audio", m.Audio().Duration, time.Second, 30) // not 0.5s
	})
}

// Bug 9 (fixed): clip.LoadVideo/LoadAudio/LoadImage return real errors, and
// Timeline.Validate (also run by Compile/Export) reports missing sources
// recorded by NewVideo/NewAudio and trims/durations past the source end.
func TestKnownBug_9_ProbeErrorsSwallowed(t *testing.T) {
	requireFFmpeg(t)

	t.Run("load_functions", func(t *testing.T) {
		dir := t.TempDir()
		missing := filepath.Join(dir, "missing.mp4")
		garbage := filepath.Join(dir, "garbage.mp4")
		if err := os.WriteFile(garbage, []byte("not a video"), 0o644); err != nil {
			t.Fatal(err)
		}
		videoOnly := genVideo(t, VideoSpec{Dur: time.Second})
		tone := genTone(t, 440, time.Second)
		png := genOpaquePNG(t, 32, 18, "0x0000FF")

		for _, tc := range []struct {
			name    string
			load    func() error
			wantErr string // "" = success
		}{
			{"video_missing", func() error { _, e := clip.LoadVideo(missing); return e }, "file not found"},
			{"video_garbage", func() error { _, e := clip.LoadVideo(garbage); return e }, "cannot read media"},
			{"video_from_audio_file", func() error { _, e := clip.LoadVideo(tone); return e }, "no video stream"},
			{"video_ok", func() error { _, e := clip.LoadVideo(videoOnly); return e }, ""},
			{"audio_missing", func() error { _, e := clip.LoadAudio(missing); return e }, "file not found"},
			{"audio_from_silent_video", func() error { _, e := clip.LoadAudio(videoOnly); return e }, "no audio stream"},
			{"audio_ok", func() error { _, e := clip.LoadAudio(tone); return e }, ""},
			{"image_missing", func() error { _, e := clip.LoadImage(missing); return e }, "file not found"},
			{"image_garbage", func() error { _, e := clip.LoadImage(garbage); return e }, "cannot read media"},
			{"image_ok", func() error { _, e := clip.LoadImage(png); return e }, ""},
		} {
			err := tc.load()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("%s: unexpected error %v", tc.name, err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("%s: error = %v, want it to contain %q", tc.name, err, tc.wantErr)
			}
		}

		v, err := clip.LoadVideo(genVideo(t, VideoSpec{ToneHz: 440, Dur: 2 * time.Second}))
		if err != nil {
			t.Fatal(err)
		}
		if !v.HasAudio() || v.Width() != W {
			t.Errorf("LoadVideo metadata: hasAudio=%v width=%d", v.HasAudio(), v.Width())
		}
		assertDuration(t, "LoadVideo", v.Duration(), 2*time.Second, 30)
	})

	t.Run("export_reports_missing_file", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "gone.mp4")
		tl := newTL()
		tl.AddVideoTrack("main").Add(clip.NewVideo(missing), 0)
		err := tl.ExportSilent(testProfile(), filepath.Join(t.TempDir(), "x.mp4"))
		if err == nil || !strings.Contains(err.Error(), "file not found") || !strings.Contains(err.Error(), `track "main"`) {
			t.Errorf("export error = %v, want a validation error naming the track and missing file", err)
		}
	})

	t.Run("trim_within_source_ok", func(t *testing.T) {
		src := genVideo(t, VideoSpec{Dur: 2 * time.Second})
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(src).Trim(500*time.Millisecond, 2*time.Second), 0)
		if err := tl.Validate(); err != nil {
			t.Errorf("Validate() = %v for a trim inside the source", err)
		}
	})

	t.Run("missing_video_file", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist.mp4")
		c := clip.NewVideo(missing)
		tl := newTL()
		tl.AddVideoTrack("v").Add(c, 0)
		if err := tl.Validate(); err == nil {
			t.Errorf("Validate() = nil for missing source %q (clip duration silently %s)", missing, c.Duration())
		}
	})

	t.Run("missing_audio_file", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist.wav")
		tl := newTL()
		tl.AddAudioTrack("a").Add(clip.NewAudio(missing), 0)
		if err := tl.Validate(); err == nil {
			t.Errorf("Validate() = nil for missing audio source %q", missing)
		}
	})

	t.Run("trim_past_source_end", func(t *testing.T) {
		src := genVideo(t, VideoSpec{Dur: 2 * time.Second})
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(src).Trim(time.Second, 5*time.Second), 0)
		if err := tl.Validate(); err == nil {
			t.Errorf("Validate() = nil for Trim(1s,5s) on a 2s source (timeline claims %s)", tl.Duration())
		}
	})

	t.Run("audio_withduration_past_source_end", func(t *testing.T) {
		// Today this renders a 1s file while tl.Duration() reports 3s.
		tone := genTone(t, 440, time.Second)
		tl := newTL()
		tl.AddAudioTrack("a").Add(clip.NewAudio(tone).WithDuration(3*time.Second), 0)
		if err := tl.Validate(); err == nil {
			t.Errorf("Validate() = nil for WithDuration(3s) on a 1s audio source (timeline claims %s)", tl.Duration())
		}
	})
}

// Bug 10 (fixed): the compiler maps only the streams the profile's container
// can carry (export.Profile.OutputStreams). Audio-only profiles used to get a
// video stream and GIF an audio stream, and FFmpeg exited with "Automatic
// encoder selection failed … (codec none)".
func TestKnownBug_10_ProfileStreamMismatch(t *testing.T) {
	requireFFmpeg(t)

	src := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: time.Second})
	tl := newTL()
	tl.AddVideoTrack("v").Add(clip.NewVideo(src), 0)

	for _, tc := range []struct {
		name    string
		profile *export.Profile
		ext     string
		enc     string
		video   int
		audio   int
	}{
		{"WAV_from_video_timeline", export.WAV(), ".wav", "pcm_s16le", 0, 1},
		{"FLAC_from_video_timeline", export.FLAC(), ".flac", "flac", 0, 1},
		{"MP3_from_video_timeline", export.MP3(), ".mp3", "libmp3lame", 0, 1},
		{"GIF_from_video_with_audio", export.GIF(), ".gif", "gif", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireEncoder(t, tc.enc)
			out := filepath.Join(t.TempDir(), "x"+tc.ext)
			if err := tl.ExportSilent(tc.profile, out); err != nil {
				t.Fatalf("export failed: %v", firstLine(err))
			}
			m := probe(t, out)
			if m.count("video") != tc.video || m.count("audio") != tc.audio {
				t.Errorf("streams: %+v, want %d video / %d audio", m.Streams, tc.video, tc.audio)
			}
			if tc.audio == 1 {
				assertDuration(t, "audio", m.Duration, time.Second, 30)
			}
		})
	}

	t.Run("audio_profile_without_audio_errors_early", func(t *testing.T) {
		silent := newTL()
		silent.AddVideoTrack("v").Add(clip.NewVideo(genVideo(t, VideoSpec{Dur: time.Second})), 0)
		_, err := silent.DryRun(export.WAV(), "x.wav")
		if err == nil || !strings.Contains(err.Error(), "audio-only") || !strings.Contains(err.Error(), "no audio") {
			t.Errorf("DryRun error = %v, want an audio-only/no audio error", err)
		}
	})

	t.Run("gif_without_video_errors_early", func(t *testing.T) {
		audioTL := newTL()
		audioTL.AddAudioTrack("a").Add(clip.NewAudio(genTone(t, 440, time.Second)), 0)
		_, err := audioTL.DryRun(export.GIF(), "x.gif")
		if err == nil || !strings.Contains(err.Error(), "video-only") {
			t.Errorf("DryRun error = %v, want a video-only/no video error", err)
		}
	})

	t.Run("av_profile_still_accepts_silent_video", func(t *testing.T) {
		silent := newTL()
		silent.AddVideoTrack("v").Add(clip.NewVideo(genVideo(t, VideoSpec{Dur: time.Second})), 0)
		m := probe(t, renderMP4(t, silent))
		if m.count("video") != 1 || m.count("audio") != 0 {
			t.Errorf("streams: %+v", m.Streams)
		}
	})
}

// Bug 11 (fixed): image inputs are keyed by path and duration, so reusing
// the same image with different durations gives each use its own
// -loop/-t input. Previously the last -t won for every use: a 2s image +
// 1s video + 0.5s image (same PNG) rendered 0.5s + 1s + 0.5s = 2s.
func TestKnownBug_11_SameImageReusedWithDifferentDurations(t *testing.T) {
	requireFFmpeg(t)

	img := genOpaquePNG(t, W, H, "0x0000FF")
	red := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: time.Second})
	tl := newTL()
	tl.AddVideoTrack("v").AddSequence(
		clip.NewImage(img).WithDuration(2*time.Second),
		clip.NewVideo(red),
		clip.NewImage(img).WithDuration(500*time.Millisecond),
	)
	out := renderMP4(t, tl)

	assertDuration(t, "video", probe(t, out).Video().Duration, 3500*time.Millisecond, 30)
	assertPixel(t, out, time.Second, cx, cy, Blue, colorTol)
	assertPixel(t, out, 2500*time.Millisecond, cx, cy, Red, colorTol)
	assertPixel(t, out, 3250*time.Millisecond, cx, cy, Blue, colorTol)
}

// Bug 12 (fixed): clip fades used to be applied BEFORE speed effects while
// the fade times were computed in output time, so on a 4s clip at 2x the
// 0.5s fade-out turned the picture black from ~1.0s instead of fading over
// 1.5s–2.0s (same for AudioClip + AudioSpeed). Fades now run after the
// effects chain, as they already did for embedded audio (#7).
func TestKnownBug_12_FadesWithSpeedMisplaced(t *testing.T) {
	requireFFmpeg(t)

	t.Run("video_fade_out_after_speedup", func(t *testing.T) {
		red := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: 4 * time.Second})
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(red).WithEffect(effects.SpeedUp(2)).WithFadeOut(500*time.Millisecond), 0)
		out := renderMP4(t, tl)
		assertPixel(t, out, 1200*time.Millisecond, cx, cy, Red, colorTol)   // before the fade
		assertPixel(t, out, 1950*time.Millisecond, cx, cy, Black, colorTol) // end of the fade
	})

	t.Run("video_fade_in_after_slowdown", func(t *testing.T) {
		// 1s clip slowed 2x = 2s; a 1s fade-in must be complete at 1s of
		// output time (it used to stretch to 2s).
		red := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: time.Second})
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(red).WithEffect(effects.SlowDown(2)).WithFadeIn(time.Second), 0)
		out := renderMP4(t, tl)
		assertDuration(t, "video", probe(t, out).Video().Duration, 2*time.Second, 30)
		assertPixel(t, out, 0, cx, cy, Black, colorTol)
		assertPixel(t, out, 1200*time.Millisecond, cx, cy, Red, colorTol)
	})

	t.Run("audio_fade_out_after_audiospeed", func(t *testing.T) {
		tone := genTone(t, 440, 4*time.Second)
		tl := newTL()
		tl.AddAudioTrack("a").Add(clip.NewAudio(tone).WithEffect(effects.AudioSpeed(2)).WithFadeOut(500*time.Millisecond), 0)
		out := render(t, tl, export.WAV(), ".wav")
		if l := audioLevel(t, out, 1100*time.Millisecond, 300*time.Millisecond); l.MeanDB < -40 {
			t.Errorf("audio before the fade-out = %.1f dB, want the tone (fade should start at 1.5s)", l.MeanDB)
		}
	})
}

func firstLine(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// arabicFontInNastyPath finds an Arabic-capable font and copies it into a
// directory whose name contains spaces, ':', ',', '[' and ']'.
func arabicFontInNastyPath(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"/System/Library/Fonts/Supplemental/Tahoma.ttf",
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
		"/usr/share/fonts/truetype/noto/NotoSansArabic-Regular.ttf",
		"/usr/share/fonts/noto/NotoSansArabic-Regular.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
	}
	for _, c := range candidates {
		data, err := os.ReadFile(c)
		if err != nil {
			continue
		}
		dir := filepath.Join(t.TempDir(), "My Fonts: v1, [ar]")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, "arabic font.ttf")
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return dst
	}
	t.Skip("e2e: no Arabic-capable font found (looked for Tahoma, Arial Unicode, Noto Sans Arabic, DejaVu Sans)")
	return ""
}

// --- helpers for filter-graph inspection ---

func filterComplexArg(args []string) string {
	for i, a := range args {
		if a == "-filter_complex" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// unescapeOnce removes one level of backslash escaping (\x → x) and
// single-quote quoting, as FFmpeg's filtergraph parser does for filter args.
func unescapeOnce(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			i++
			b.WriteByte(s[i])
		case s[i] == '\'':
			// quotes delimit a literal span; drop them
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// splitUnescaped splits s on sep, honoring FFmpeg filtergraph escaping:
// backslash escapes the next character and '...' quotes a span.
func splitUnescaped(s string, sep byte) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\\' && i+1 < len(s):
			cur.WriteByte(ch)
			cur.WriteByte(s[i+1])
			i++
		case ch == '\'':
			inQuote = !inQuote
			cur.WriteByte(ch)
		case ch == sep && !inQuote:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	return append(parts, cur.String())
}
