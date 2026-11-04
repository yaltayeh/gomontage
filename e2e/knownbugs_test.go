//go:build e2e

package e2e

import (
	"fmt"
	"image"
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

// Bug 1: overlay tracks are opaque black outside their content.
// Sources: compiler.go compileGapClip (yuv420p black gaps), compileTrack tpad
// color=black (leading/tail padding), compileGeneratedClip (text drawn on an
// opaque black color source), compileVideoEntry pad color=black.
func TestKnownBug_1_OverlayTrackOpaque(t *testing.T) {
	knownBug(t, 1, "overlay tracks are opaque black outside their content")

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

// Bug 2: position and size are ignored. compileVideoEntry always scales file
// clips to the canvas, and overlayTracks always uses overlay=x=0:y=0.
// Expected: an overlay with WithPosition is drawn at its native size (or
// WithSize) with its top-left corner at (X,Y).
func TestKnownBug_2_PositionAndSizeIgnored(t *testing.T) {
	knownBug(t, 2, "WithPosition/WithSize/TextStyle.Position are ignored")

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

// Bug 3: drawtext text is not escaped (compileGeneratedClip passes the raw
// string; engine/builder.go joins params with ':' and chains with ';').
func TestKnownBug_3_DrawtextNotEscaped(t *testing.T) {
	knownBug(t, 3, "drawtext text with : ' % \\ ; breaks the filter graph")

	nasty := `Time: 5 o'clock; 100% \o/`

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
					for _, kv := range splitUnescaped(body, ':') {
						drawtextKeys = append(drawtextKeys, strings.SplitN(kv, "=", 2)[0])
					}
				}
			}
			return len(parts), drawtextKeys
		}
		wantChains, wantKeys := shape("plain")
		gotChains, gotKeys := shape(nasty)
		if gotChains != wantChains {
			t.Errorf("filter graph has %d chains with special text, %d with plain text", gotChains, wantChains)
		}
		if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
			t.Errorf("drawtext options parsed as %v, want %v", gotKeys, wantKeys)
		}
	})

	t.Run("render", func(t *testing.T) {
		requireFilter(t, "drawtext")
		tl := redBase(t)
		tl.AddVideoTrack("t").Add(clip.NewText(nasty, clip.DefaultTextStyle()).WithDuration(time.Second), 0)
		out := filepath.Join(t.TempDir(), "x.mp4")
		if err := tl.ExportSilent(testProfile(), out); err != nil {
			t.Fatalf("export with special characters in text failed: %v", err)
		}
	})
}

// Bug 4: a timeline containing only generated clips (color/text) fails with
// "graph has no inputs" (engine/builder.go BuildCommand).
func TestKnownBug_4_GeneratedOnlyTimeline(t *testing.T) {
	knownBug(t, 4, "timeline with only color/text clips fails: graph has no inputs")

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
	})
}

// Bug 5: the timeline FPS is not applied to file clips (no fps filter / -r).
func TestKnownBug_5_TimelineFPSIgnored(t *testing.T) {
	knownBug(t, 5, "timeline FPS is not applied to file-backed clips")

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
		v := probe(t, renderMP4(t, tl)).Video()
		if v.FPS < 29.9 || v.FPS > 30.1 {
			t.Errorf("output fps = %.3f, want 30", v.FPS)
		}
	})
}

// Bug 6: Profile.Width/Height are never applied (Profile.Params ignores them
// and the compiler never scales to them).
func TestKnownBug_6_ProfileResolutionIgnored(t *testing.T) {
	knownBug(t, 6, "Profile.Width/Height are ignored")

	src := genVideo(t, VideoSpec{Dur: time.Second})

	t.Run("custom_profile", func(t *testing.T) {
		p := testProfile()
		p.Width, p.Height = 640, 360
		tl := newTL() // 320x180
		tl.AddVideoTrack("v").Add(clip.NewVideo(src), 0)
		v := probe(t, render(t, tl, p, ".mp4")).Video()
		if v.Width != 640 || v.Height != 360 {
			t.Errorf("output %dx%d, want 640x360 from profile", v.Width, v.Height)
		}
	})

	t.Run("YouTube4K", func(t *testing.T) {
		requireEncoder(t, "libx264")
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(src), 0)
		v := probe(t, render(t, tl, export.YouTube4K(), ".mp4")).Video()
		if v.Width != 3840 || v.Height != 2160 {
			t.Errorf("output %dx%d, want 3840x2160", v.Width, v.Height)
		}
	})
}

// Bug 7: compileAudioFromVideoEntry applies trim, volume and effects but not
// the clip's fadeIn/fadeOut, so embedded audio never fades.
func TestKnownBug_7_EmbeddedAudioIgnoresFades(t *testing.T) {
	knownBug(t, 7, "VideoClip.WithFadeIn/WithFadeOut don't fade embedded audio")

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
}

// Bug 8: effects.SpeedUp only retimes video (setpts); embedded audio keeps
// its original length unless AudioSpeed is added separately.
func TestKnownBug_8_SpeedUpAudioDrift(t *testing.T) {
	knownBug(t, 8, "SpeedUp retimes video only; embedded audio drifts")

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
}

// Bug 9: probe errors are swallowed and trims are not bounds-checked.
// The clip constructors have no error return, so the earliest place an error
// can surface without an API change is Timeline.Validate (also called by
// Compile). Suggested library change: clip.LoadVideo/LoadAudio returning
// (clip, error), and/or Validate checking each source exists and that
// TrimEnd <= probed source duration.
func TestKnownBug_9_ProbeErrorsSwallowed(t *testing.T) {
	knownBug(t, 9, "missing files become 0s clips; trims past source end are accepted")

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

// Bug 10 (found by this suite): the compiler maps every stream it produced,
// regardless of the profile. Audio-only profiles (WAV/FLAC/MP3) receive a
// video stream and GIF receives an audio stream; FFmpeg then exits with
// "Automatic encoder selection failed … (codec none)". Expected: streams the
// profile can't carry are dropped.
func TestKnownBug_10_ProfileStreamMismatch(t *testing.T) {
	knownBug(t, 10, "audio-only profiles fail on timelines with video; GIF fails with audio")

	src := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: time.Second})
	tl := newTL()
	tl.AddVideoTrack("v").Add(clip.NewVideo(src), 0)

	t.Run("WAV_from_video_timeline", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "x.wav")
		if err := tl.ExportSilent(export.WAV(), out); err != nil {
			t.Fatalf("export failed: %v", firstLine(err))
		}
		m := probe(t, out)
		if m.count("video") != 0 || m.count("audio") != 1 {
			t.Errorf("WAV streams: %+v, want exactly one audio stream", m.Streams)
		}
	})

	t.Run("GIF_from_video_with_audio", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "x.gif")
		if err := tl.ExportSilent(export.GIF(), out); err != nil {
			t.Fatalf("export failed: %v", firstLine(err))
		}
		m := probe(t, out)
		if m.count("video") != 1 || m.count("audio") != 0 {
			t.Errorf("GIF streams: %+v, want exactly one video stream", m.Streams)
		}
	})
}

// Bug 11 (found by this suite): the compiler shares one FFmpeg input per
// source path (getOrAddInput) but sets image input options on it per clip
// (-loop/-framerate/-t in compileVideoEntry). Using the same image twice with
// different durations makes the last -t win for every use. Observed: a
// 2s image + 1s video + 0.5s image (same PNG) renders 0.5s blue + 1s red +
// 0.5s blue = 2s instead of 3.5s (the first image is cut to 0.5s).
func TestKnownBug_11_SameImageReusedWithDifferentDurations(t *testing.T) {
	knownBug(t, 11, "reusing one image file with different durations truncates the output")

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
	if probe(t, out).Video().Duration > 2600*time.Millisecond {
		assertPixel(t, out, 2500*time.Millisecond, cx, cy, Red, colorTol)
	} else {
		t.Errorf("output too short to contain the red clip at 2s–3s")
	}
}

func firstLine(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
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
