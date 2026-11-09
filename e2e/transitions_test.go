//go:build e2e

package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/effects"
	"github.com/ahmedhodiani/gomontage/timeline"
)

// Transitions (handle model, docs/project-format.md §9): a crossfade is
// centred on the cut and made from source beyond the clips' trim points;
// it never changes clip positions or the timeline length.

const ms = time.Millisecond

// genColorSteps generates a silent video that shows each color for step.
func genColorSteps(t *testing.T, step time.Duration, colors ...string) string {
	t.Helper()
	out := filepath.Join(fixtureDir(t), "steps.mp4")
	var parts, labels []string
	for i, c := range colors {
		parts = append(parts, fmt.Sprintf("color=c=%s:s=%dx%d:r=30:d=%s[c%d]", c, W, H, secs(step), i))
		labels = append(labels, fmt.Sprintf("[c%d]", i))
	}
	fc := strings.Join(parts, ";") + ";" + strings.Join(labels, "") + fmt.Sprintf("concat=n=%d:v=1:a=0[v]", len(colors))
	runFFmpeg(t, "-filter_complex", fc, "-map", "[v]", "-c:v", "mpeg4", "-q:v", "2", "-pix_fmt", "yuv420p", out)
	return out
}

// genSilence generates a stereo 48 kHz WAV of digital silence.
func genSilence(t *testing.T, d time.Duration) string {
	t.Helper()
	out := filepath.Join(fixtureDir(t), "silence.wav")
	runFFmpeg(t, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo:d="+secs(d), "-c:a", "pcm_s16le", out)
	return out
}

// redBlue is a red clip (0-2 s) followed by a blue clip (2-4 s), with a
// crossfade of d at the cut when d > 0. Both sources have 1 s to spare on
// each side.
func redBlue(t *testing.T, d time.Duration) *timeline.Timeline {
	red := clip.NewVideo(genVideo(t, VideoSpec{Color: "0xFF0000", Dur: 4 * time.Second}))
	blue := clip.NewVideo(genVideo(t, VideoSpec{Color: "0x0000FF", Dur: 4 * time.Second}))
	tl := newTL()
	v := tl.AddVideoTrack("v")
	v.Add(red.Trim(time.Second, 3*time.Second), 0)
	b := blue.Trim(time.Second, 3*time.Second)
	if d > 0 {
		v.AddWithTransition(b, 2*time.Second, timeline.Crossfade(d))
	} else {
		v.Add(b, 2*time.Second)
	}
	return tl
}

func TestTransition_CrossfadeBlend(t *testing.T) {
	requireFFmpeg(t)
	out := renderMP4(t, redBlue(t, time.Second))
	// Window 1.5-2.5 s. Before and after it the pure colors.
	assertPixel(t, out, 1400*ms, cx, cy, Red, colorTol)
	assertPixel(t, out, 2600*ms, cx, cy, Blue, colorTol)
	// The midpoint (on the cut) is a 50 % blend; a quarter in, 25 % blue.
	assertPixel(t, out, 2000*ms, cx, cy, RGB{128, 0, 128}, colorTol)
	assertPixel(t, out, 1750*ms, cx, cy, RGB{191, 0, 64}, colorTol)
	assertPixel(t, out, 2250*ms, cx, cy, RGB{64, 0, 191}, colorTol)
}

func TestTransition_DurationUnchanged(t *testing.T) {
	requireFFmpeg(t)
	plain := probe(t, renderMP4(t, redBlue(t, 0)))
	faded := probe(t, renderMP4(t, redBlue(t, time.Second)))
	assertDuration(t, "without transition", plain.Video().Duration, 4*time.Second, 30)
	if faded.Video().Duration != plain.Video().Duration || faded.Video().Frames != plain.Video().Frames {
		t.Errorf("with transition: %s (%d frames), without: %s (%d frames)",
			faded.Video().Duration, faded.Video().Frames, plain.Video().Duration, plain.Video().Frames)
	}
}

// The transition shows footage from beyond the trim points: the outgoing
// clip's source after its end (green) and the incoming clip's source before
// its start (white).
func TestTransition_UsesHandles(t *testing.T) {
	requireFFmpeg(t)
	a := clip.NewVideo(genColorSteps(t, time.Second, "0xFF0000", "0x00FF00")) // red | green
	b := clip.NewVideo(genColorSteps(t, time.Second, "0xFFFFFF", "0x0000FF")) // white | blue
	tl := newTL()
	v := tl.AddVideoTrack("v")
	v.Add(a.Trim(0, time.Second), 0)                                                                      // red
	v.AddWithTransition(b.Trim(time.Second, 2*time.Second), time.Second, timeline.Crossfade(time.Second)) // blue
	out := renderMP4(t, tl)
	// 0.75 s: 75 % red (a's own tail) + 25 % white (b's handle).
	assertPixel(t, out, 750*ms, cx, cy, RGB{255, 64, 64}, colorTol)
	// 1.25 s: 25 % green (a's handle) + 75 % blue (b's own head).
	assertPixel(t, out, 1250*ms, cx, cy, RGB{0, 64, 191}, colorTol)
	assertDuration(t, "video", probe(t, out).Video().Duration, 2*time.Second, 30)
}

// Handles are scaled by the clip's speed: a 2x clip's tail handle plays at
// 2x and covers twice as much source, so it lasts the whole second half of
// the window. The incoming clip is a black color (unlimited handle), so the
// frame is (1-p) times the outgoing clip's handle frame.
func TestTransition_SpeedScaledHandle(t *testing.T) {
	requireFFmpeg(t)
	// 0-2 s blue, 2-2.25 green, 2.25-2.5 white, then red.
	src := genColorSteps(t, 250*ms, "0x0000FF", "0x0000FF", "0x0000FF", "0x0000FF", "0x0000FF", "0x0000FF", "0x0000FF", "0x0000FF",
		"0x00FF00", "0xFFFFFF", "0xFF0000", "0xFF0000", "0xFF0000", "0xFF0000", "0xFF0000", "0xFF0000")
	tl := newTL()
	v := tl.AddVideoTrack("v")
	v.Add(clip.NewVideo(src).Trim(0, 2*time.Second).WithEffect(effects.SpeedUp(2)), 0) // 0-1 s
	v.AddWithTransition(clip.NewColor("black", W, H).WithDuration(2*time.Second), time.Second, timeline.Crossfade(2*time.Second))
	out := renderMP4(t, tl)
	// Window 0-2 s; at 1.375 s p = 0.6875. The 2x handle reads source
	// 2 + 0.375*2 = 2.75 s (red); at normal speed it would read 2.375 s
	// (white).
	assertPixel(t, out, 1375*ms, cx, cy, RGB{80, 0, 0}, 16)
	// At 1.6 s (source 3.2 s, red at 1-p = 0.2) the handle is still there;
	// an unscaled handle (1 s of source = 0.5 s at 2x) would have ended at
	// 1.5 s.
	assertPixel(t, out, 1600*ms, cx, cy, RGB{51, 0, 0}, 16)
	assertDuration(t, "video", probe(t, out).Video().Duration, 3*time.Second, 30)
}

// The sound of video clips cross-fades with the picture and stays in sync
// over several transitions; durations are unchanged.
func TestTransition_AudioCrossfadeAndSync(t *testing.T) {
	requireFFmpeg(t)
	tone := clip.NewVideo(genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: 4 * time.Second}))
	mute := clip.NewVideo(genVideo(t, VideoSpec{Color: "0x0000FF", Dur: 4 * time.Second}))
	tl := newTL()
	v := tl.AddVideoTrack("v")
	half := 500 * ms
	v.Add(tone.Trim(time.Second, 3*time.Second), 0)                                                     // 0-2 tone
	v.AddWithTransition(mute.Trim(time.Second, 3*time.Second), 2*time.Second, timeline.Crossfade(half)) // 2-4 silent
	v.AddWithTransition(tone.Trim(time.Second, 3*time.Second), 4*time.Second, timeline.Crossfade(half)) // 4-6 tone
	v.AddWithTransition(mute.Trim(time.Second, 3*time.Second), 6*time.Second, timeline.Crossfade(half)) // 6-8 silent
	out := renderMP4(t, tl)

	m := probe(t, out)
	assertDuration(t, "video", m.Video().Duration, 8*time.Second, 30)
	assertDuration(t, "audio", m.Audio().Duration, 8*time.Second, 30)

	full := audioLevel(t, out, 4900*ms, 200*ms).MeanDB // middle of the second tone clip
	if full < -30 {
		t.Fatalf("tone not audible: %.1f dB", full)
	}
	if l := audioLevel(t, out, 3000*ms, 300*ms).MeanDB; l > -60 {
		t.Errorf("silent clip not silent: %.1f dB", l)
	}
	// On each cut the tone is at half amplitude (-6 dB): the sound fades in
	// step with the picture, also after three transitions.
	for _, cut := range []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second} {
		l := audioLevel(t, out, cut-20*ms, 40*ms).MeanDB
		if d := l - full; d < -8 || d > -4 {
			t.Errorf("level at the cut %s = %.1f dB relative to the tone, want -6 ±2", cut, d)
		}
		assertPixel(t, out, cut, cx, cy, RGB{128, 0, 128}, colorTol)
		// Outside the 0.5 s window: the clips' own sound.
		before := audioLevel(t, out, cut-500*ms, 200*ms).MeanDB
		after := audioLevel(t, out, cut+300*ms, 200*ms).MeanDB
		toneFirst := cut != 4*time.Second
		if toneFirst && (before < full-1 || after > -60) || !toneFirst && (before > -60 || after < full-1) {
			t.Errorf("around the cut %s: %.1f dB before, %.1f dB after", cut, before, after)
		}
	}
}

// Audio tracks cross-fade with acrossfade.
func TestTransition_AudioTrack(t *testing.T) {
	requireFFmpeg(t)
	tone := clip.NewAudio(genTone(t, 440, 4*time.Second))
	quiet := clip.NewAudio(genSilence(t, 4*time.Second))
	tl := newTL()
	tl.AddVideoTrack("v").Add(clip.NewColor("black", W, H).WithDuration(4*time.Second), 0)
	a := tl.AddAudioTrack("music")
	a.Add(tone.Trim(time.Second, 3*time.Second), 0)
	a.AddWithTransition(quiet.Trim(time.Second, 3*time.Second), 2*time.Second, timeline.Crossfade(time.Second))
	out := renderMP4(t, tl)

	m := probe(t, out)
	assertDuration(t, "audio", m.Audio().Duration, 4*time.Second, 30)
	full := audioLevel(t, out, 1000*ms, 300*ms).MeanDB
	if l := audioLevel(t, out, 1980*ms, 40*ms).MeanDB - full; l < -8 || l > -4 {
		t.Errorf("level at the cut = %.1f dB relative, want -6 ±2", l)
	}
	if l := audioLevel(t, out, 1500*ms+100*ms, 40*ms).MeanDB - full; l < -4 || l > -0.5 {
		t.Errorf("level 0.1 s into the window = %.1f dB relative, want about -1.9", l)
	}
	if l := audioLevel(t, out, 2600*ms, 300*ms).MeanDB; l > -60 {
		t.Errorf("after the window: %.1f dB, want silence", l)
	}
}

// On an upper track two logos dissolve into each other over the base.
func TestTransition_UpperTrackLogos(t *testing.T) {
	requireFFmpeg(t)
	tl := newTL()
	tl.AddVideoTrack("base").Add(clip.NewColor("0x0000FF", W, H).WithDuration(4*time.Second), 0)
	up := tl.AddVideoTrack("logos")
	red := clip.NewImage(genOpaquePNG(t, 40, 40, "0xFF0000")).WithDuration(2 * time.Second).
		WithLayout(clip.Layout{X: 0.1, Y: 0.1, Anchor: clip.AnchorTopLeft, Width: 0.125})
	green := clip.NewImage(genOpaquePNG(t, 40, 40, "0x00FF00")).WithDuration(2 * time.Second).
		WithLayout(clip.Layout{X: 0.9, Y: 0.9, Anchor: clip.AnchorBottomRight, Width: 0.125})
	up.Add(red, 0)
	up.AddWithTransition(green, 2*time.Second, timeline.Crossfade(time.Second))
	out := renderMP4(t, tl)

	rx, ry := 32+20, 18+20   // red logo centre: top-left at (32, 18), 40 px
	gx, gy := 288-20, 162-20 // green logo centre: bottom-right at (288, 162)
	assertPixel(t, out, 1400*ms, rx, ry, Red, colorTol)
	assertPixel(t, out, 1400*ms, gx, gy, Blue, colorTol)
	assertPixel(t, out, 2000*ms, rx, ry, RGB{128, 0, 128}, colorTol) // half red over blue
	assertPixel(t, out, 2000*ms, gx, gy, RGB{0, 128, 128}, colorTol) // half green over blue
	assertPixel(t, out, 2600*ms, rx, ry, Blue, colorTol)
	assertPixel(t, out, 2600*ms, gx, gy, Green, colorTol)
	assertPixel(t, out, 2000*ms, cx, cy, Blue, colorTol) // the base stays visible
	assertDuration(t, "video", probe(t, out).Video().Duration, 4*time.Second, 30)
}

// A transition into a sped-up clip and out of a slowed-down one, with
// sound: durations and sync hold.
func TestTransition_WithSpeedEffects(t *testing.T) {
	requireFFmpeg(t)
	tone := clip.NewVideo(genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: 6 * time.Second}))
	mute := clip.NewVideo(genVideo(t, VideoSpec{Color: "0x0000FF", Dur: 6 * time.Second}))
	tl := newTL()
	v := tl.AddVideoTrack("v")
	v.Add(mute.Trim(0, 2*time.Second), 0)                                                                                                   // 0-2
	v.AddWithTransition(tone.Trim(2*time.Second, 4*time.Second).WithEffect(effects.SpeedUp(2)), 2*time.Second, timeline.Crossfade(500*ms))  // 2-3
	v.AddWithTransition(mute.Trim(2*time.Second, 3*time.Second).WithEffect(effects.SlowDown(2)), 3*time.Second, timeline.Crossfade(500*ms)) // 3-5
	out := renderMP4(t, tl)
	m := probe(t, out)
	assertDuration(t, "video", m.Video().Duration, 5*time.Second, 30)
	assertDuration(t, "audio", m.Audio().Duration, 5*time.Second, 30)
	full := audioLevel(t, out, 2400*ms, 200*ms).MeanDB
	if full < -30 {
		t.Fatalf("sped-up tone not audible: %.1f dB", full)
	}
	for _, cut := range []time.Duration{2 * time.Second, 3 * time.Second} {
		if l := audioLevel(t, out, cut-20*ms, 40*ms).MeanDB - full; l < -8 || l > -4 {
			t.Errorf("level at the cut %s = %.1f dB relative, want -6 ±2", cut, l)
		}
		assertPixel(t, out, cut, cx, cy, RGB{128, 0, 128}, colorTol)
	}
	assertPixel(t, out, 4*time.Second, cx, cy, Blue, colorTol)
}
