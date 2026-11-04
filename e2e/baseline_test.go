//go:build e2e

package e2e

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/export"
	"github.com/ahmedhodiani/gomontage/timeline"
)

// Baseline tests: behavior that works today. They prove the harness and
// guard against regressions while the known bugs are fixed.

const (
	W, H = 320, 180
	cx   = W / 2
	cy   = H / 2
)

func newTL() *timeline.Timeline {
	return timeline.New(timeline.Config{Width: W, Height: H, FPS: 30})
}

func TestBaseline_Trim(t *testing.T) {
	requireFFmpeg(t)
	src := genColorSequence(t, W, H, 30, []string{"0xFF0000", "0x00FF00", "0x0000FF"})

	tl := newTL()
	tl.AddVideoTrack("main").Add(clip.NewVideo(src).Trim(time.Second, 2*time.Second), 0)
	out := renderMP4(t, tl)

	m := probe(t, out)
	assertDuration(t, "video", m.Video().Duration, time.Second, 30)
	assertPixel(t, out, 100*time.Millisecond, cx, cy, Green, colorTol)
	assertPixel(t, out, 900*time.Millisecond, cx, cy, Green, colorTol)
}

func TestBaseline_SequenceConcat(t *testing.T) {
	requireFFmpeg(t)
	red := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: time.Second, ToneHz: 440})
	blue := genVideo(t, VideoSpec{Color: "0x0000FF", Dur: time.Second, ToneHz: 880})

	tl := newTL()
	tl.AddVideoTrack("main").AddSequence(clip.NewVideo(red), clip.NewVideo(blue))
	out := renderMP4(t, tl)

	m := probe(t, out)
	assertDuration(t, "video", m.Video().Duration, 2*time.Second, 30)
	if m.Audio() == nil {
		t.Fatal("expected an audio stream from the concatenated clips")
	}
	assertDuration(t, "audio", m.Audio().Duration, 2*time.Second, 30)
	assertPixel(t, out, 500*time.Millisecond, cx, cy, Red, colorTol)
	assertPixel(t, out, 1500*time.Millisecond, cx, cy, Blue, colorTol)
}

func TestBaseline_GapsFilledWithBlack(t *testing.T) {
	requireFFmpeg(t)
	red := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: time.Second})
	blue := genVideo(t, VideoSpec{Color: "0x0000FF", Dur: time.Second})

	tl := newTL()
	track := tl.AddVideoTrack("main")
	track.Add(clip.NewVideo(red), 0)
	track.Add(clip.NewVideo(blue), 2*time.Second)
	out := renderMP4(t, tl)

	assertDuration(t, "video", probe(t, out).Video().Duration, 3*time.Second, 30)
	assertPixel(t, out, 500*time.Millisecond, cx, cy, Red, colorTol)
	assertPixel(t, out, 1500*time.Millisecond, cx, cy, Black, colorTol)
	assertPixel(t, out, 2500*time.Millisecond, cx, cy, Blue, colorTol)
}

func TestBaseline_LeadingGapSingleClip(t *testing.T) {
	requireFFmpeg(t)
	red := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: time.Second})

	tl := newTL()
	tl.AddVideoTrack("main").Add(clip.NewVideo(red), time.Second)
	out := renderMP4(t, tl)

	assertDuration(t, "video", probe(t, out).Video().Duration, 2*time.Second, 30)
	assertPixel(t, out, 500*time.Millisecond, cx, cy, Black, colorTol)
	assertPixel(t, out, 1500*time.Millisecond, cx, cy, Red, colorTol)
}

func TestBaseline_LetterboxAspectRatio(t *testing.T) {
	requireFFmpeg(t)
	// 4:3 red source on a 16:9 canvas → pillarbox: black sides, red center.
	src := genVideo(t, VideoSpec{W: 240, H: 180, Color: "0xFF0000", Dur: time.Second})

	tl := newTL()
	tl.AddVideoTrack("main").Add(clip.NewVideo(src), 0)
	out := renderMP4(t, tl)

	v := probe(t, out).Video()
	if v.Width != W || v.Height != H {
		t.Errorf("output %dx%d, want %dx%d", v.Width, v.Height, W, H)
	}
	assertPixel(t, out, 500*time.Millisecond, 10, cy, Black, colorTol)
	assertPixel(t, out, 500*time.Millisecond, cx, cy, Red, colorTol)
	assertPixel(t, out, 500*time.Millisecond, W-10, cy, Black, colorTol)
}

func TestBaseline_AudioMixing(t *testing.T) {
	requireFFmpeg(t)
	tone := genTone(t, 440, time.Second)

	// Two tracks at different times → loud / silent / loud.
	tl := newTL()
	tl.AddAudioTrack("a").Add(clip.NewAudio(tone), 0)
	tl.AddAudioTrack("b").Add(clip.NewAudio(tone), 2*time.Second)
	out := render(t, tl, export.WAV(), ".wav")

	m := probe(t, out)
	assertDuration(t, "audio", m.Duration, 3*time.Second, 30)
	l0 := audioLevel(t, out, 100*time.Millisecond, 800*time.Millisecond)
	l1 := audioLevel(t, out, 1100*time.Millisecond, 800*time.Millisecond)
	l2 := audioLevel(t, out, 2100*time.Millisecond, 800*time.Millisecond)
	if l0.MeanDB < -40 || l2.MeanDB < -40 {
		t.Errorf("expected tone in [0,1) and [2,3): got %.1f / %.1f dB", l0.MeanDB, l2.MeanDB)
	}
	if l1.MeanDB > -60 {
		t.Errorf("expected silence in [1,2): got %.1f dB", l1.MeanDB)
	}

	// Two identical tones at the same time sum (+6 dB, amix normalize=0).
	single := newTL()
	single.AddAudioTrack("a").Add(clip.NewAudio(tone), 0)
	singleOut := render(t, single, export.WAV(), ".wav")

	both := newTL()
	both.AddAudioTrack("a").Add(clip.NewAudio(tone), 0)
	both.AddAudioTrack("b").Add(clip.NewAudio(tone), 0)
	bothOut := render(t, both, export.WAV(), ".wav")

	ls := audioLevel(t, singleOut, 100*time.Millisecond, 800*time.Millisecond)
	lb := audioLevel(t, bothOut, 100*time.Millisecond, 800*time.Millisecond)
	if d := lb.MeanDB - ls.MeanDB; d < 5 || d > 7 {
		t.Errorf("mixing two identical tones: +%.1f dB, want ≈ +6 dB", d)
	}
}

func TestBaseline_AudioVolume(t *testing.T) {
	requireFFmpeg(t)
	tone := genTone(t, 440, time.Second)

	full := newTL()
	full.AddAudioTrack("a").Add(clip.NewAudio(tone), 0)
	half := newTL()
	half.AddAudioTrack("a").Add(clip.NewAudio(tone).WithVolume(0.5), 0)

	lf := audioLevel(t, render(t, full, export.WAV(), ".wav"), 100*time.Millisecond, 800*time.Millisecond)
	lh := audioLevel(t, render(t, half, export.WAV(), ".wav"), 100*time.Millisecond, 800*time.Millisecond)
	if d := lf.MeanDB - lh.MeanDB; d < 5 || d > 7 {
		t.Errorf("WithVolume(0.5): %.1f dB quieter, want ≈ 6 dB", d)
	}
}

func TestBaseline_VideoClipVolume(t *testing.T) {
	requireFFmpeg(t)
	src := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: time.Second})

	full := newTL()
	full.AddVideoTrack("v").Add(clip.NewVideo(src), 0)
	half := newTL()
	half.AddVideoTrack("v").Add(clip.NewVideo(src).WithVolume(0.5), 0)

	lf := audioLevel(t, renderMP4(t, full), 200*time.Millisecond, 600*time.Millisecond)
	lh := audioLevel(t, renderMP4(t, half), 200*time.Millisecond, 600*time.Millisecond)
	if d := lf.MeanDB - lh.MeanDB; d < 5 || d > 7 {
		t.Errorf("VideoClip.WithVolume(0.5): %.1f dB quieter, want ≈ 6 dB", d)
	}
}

func TestBaseline_AudioClipFades(t *testing.T) {
	requireFFmpeg(t)
	tone := genTone(t, 440, 3*time.Second)

	tl := newTL()
	tl.AddAudioTrack("a").Add(clip.NewAudio(tone).WithFadeIn(time.Second).WithFadeOut(time.Second), 0)
	out := render(t, tl, export.WAV(), ".wav")

	start := audioLevel(t, out, 0, 150*time.Millisecond)
	mid := audioLevel(t, out, 1200*time.Millisecond, 600*time.Millisecond)
	end := audioLevel(t, out, 2850*time.Millisecond, 150*time.Millisecond)
	if mid.MeanDB-start.MeanDB < 12 {
		t.Errorf("fade-in: start %.1f dB vs middle %.1f dB, want start ≥12 dB quieter", start.MeanDB, mid.MeanDB)
	}
	if mid.MeanDB-end.MeanDB < 12 {
		t.Errorf("fade-out: end %.1f dB vs middle %.1f dB, want end ≥12 dB quieter", end.MeanDB, mid.MeanDB)
	}
}

func TestBaseline_VideoClipVisualFade(t *testing.T) {
	requireFFmpeg(t)
	red := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: 2 * time.Second})

	tl := newTL()
	tl.AddVideoTrack("v").Add(clip.NewVideo(red).WithFadeIn(time.Second), 0)
	out := renderMP4(t, tl)

	assertPixel(t, out, 0, cx, cy, Black, colorTol)
	assertPixel(t, out, 1500*time.Millisecond, cx, cy, Red, colorTol)
}

func TestBaseline_ImageClip(t *testing.T) {
	requireFFmpeg(t)
	img := genOpaquePNG(t, W, H, "0x0000FF")

	tl := newTL()
	tl.AddVideoTrack("v").Add(clip.NewImage(img).WithDuration(2*time.Second), 0)
	out := renderMP4(t, tl)

	assertDuration(t, "video", probe(t, out).Video().Duration, 2*time.Second, 30)
	assertPixel(t, out, time.Second, cx, cy, Blue, colorTol)
}

func TestBaseline_ExportPresets(t *testing.T) {
	requireFFmpeg(t)

	t.Run("ProRes", func(t *testing.T) {
		requireEncoder(t, "prores_ks")
		src := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: time.Second})
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(src), 0)
		m := probe(t, render(t, tl, export.ProRes(), ".mov"))
		if v := m.Video(); v == nil || v.Codec != "prores" {
			t.Errorf("video stream %+v, want prores", v)
		}
		if a := m.Audio(); a == nil || a.Codec != "pcm_s16le" {
			t.Errorf("audio stream %+v, want pcm_s16le", a)
		}
	})

	audioOnly := func(t *testing.T) *timeline.Timeline {
		tl := newTL()
		tl.AddAudioTrack("a").Add(clip.NewAudio(genTone(t, 440, time.Second)), 0)
		return tl
	}

	t.Run("WAV", func(t *testing.T) {
		m := probe(t, render(t, audioOnly(t), export.WAV(), ".wav"))
		a := m.Audio()
		if a == nil || a.Codec != "pcm_s16le" || a.SampleRate != 48000 || a.Channels != 2 || len(m.Streams) != 1 {
			t.Errorf("WAV streams: %+v", m.Streams)
		}
	})

	t.Run("FLAC", func(t *testing.T) {
		requireEncoder(t, "flac")
		m := probe(t, render(t, audioOnly(t), export.FLAC(), ".flac"))
		if a := m.Audio(); a == nil || a.Codec != "flac" {
			t.Errorf("FLAC streams: %+v", m.Streams)
		}
	})

	t.Run("MP3", func(t *testing.T) {
		requireEncoder(t, "libmp3lame")
		m := probe(t, render(t, audioOnly(t), export.MP3(), ".mp3"))
		if a := m.Audio(); a == nil || a.Codec != "mp3" {
			t.Errorf("MP3 streams: %+v", m.Streams)
		}
	})

	t.Run("GIF_video_only", func(t *testing.T) {
		src := genVideo(t, VideoSpec{Color: "0xFF0000", Dur: time.Second})
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(src), 0)
		m := probe(t, render(t, tl, export.GIF(), ".gif"))
		if v := m.Video(); v == nil || v.Codec != "gif" {
			t.Errorf("GIF streams: %+v", m.Streams)
		}
	})

	for name, p := range map[string]*export.Profile{
		"YouTube720p":  export.YouTube720p(),
		"YouTube1080p": export.YouTube1080p(),
	} {
		t.Run(name, func(t *testing.T) {
			requireEncoder(t, "libx264")
			src := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: time.Second})
			tl := newTL()
			tl.AddVideoTrack("v").Add(clip.NewVideo(src), 0)
			m := probe(t, render(t, tl, p, ".mp4"))
			if v := m.Video(); v == nil || v.Codec != "h264" {
				t.Errorf("video %+v, want h264", v)
			}
			if a := m.Audio(); a == nil || a.Codec != "aac" {
				t.Errorf("audio %+v, want aac", a)
			}
		})
	}

	t.Run("H265", func(t *testing.T) {
		requireEncoder(t, "libx265")
		src := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: time.Second})
		tl := newTL()
		tl.AddVideoTrack("v").Add(clip.NewVideo(src), 0)
		if v := probe(t, render(t, tl, export.H265(), ".mp4")).Video(); v == nil || v.Codec != "hevc" {
			t.Errorf("video %+v, want hevc", v)
		}
	})
}

func TestBaseline_ExportRange(t *testing.T) {
	requireFFmpeg(t)
	src := genColorSequence(t, W, H, 30, []string{"0xFF0000", "0x00FF00", "0x0000FF"})

	tl := newTL()
	tl.AddVideoTrack("main").Add(clip.NewVideo(src), 0)
	out := filepath.Join(t.TempDir(), "range.mp4")
	if err := tl.ExportRangeSilent(time.Second, 2*time.Second, testProfile(), out); err != nil {
		t.Fatal(err)
	}

	assertDuration(t, "video", probe(t, out).Video().Duration, time.Second, 30)
	assertPixel(t, out, 100*time.Millisecond, cx, cy, Green, colorTol)
	assertPixel(t, out, 900*time.Millisecond, cx, cy, Green, colorTol)
}

// TestBaseline_DryRunMatchesExport executes the command returned by DryRun
// directly and checks it decodes to exactly the same frames and samples as
// ExportSilent produces for the same timeline.
func TestBaseline_DryRunMatchesExport(t *testing.T) {
	requireFFmpeg(t)
	red := genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: time.Second})
	blue := genVideo(t, VideoSpec{Color: "0x0000FF", ToneHz: 660, Dur: time.Second})
	tone := genTone(t, 330, 2*time.Second)

	tl := newTL()
	tl.AddVideoTrack("v").AddSequence(clip.NewVideo(red), clip.NewVideo(blue).Trim(0, 500*time.Millisecond))
	tl.AddAudioTrack("bgm").Add(clip.NewAudio(tone).WithVolume(0.3), 0)

	dir := t.TempDir()
	dryOut := filepath.Join(dir, "dry.mp4")
	expOut := filepath.Join(dir, "export.mp4")

	cmd, err := tl.DryRun(testProfile(), dryOut)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Binary != "ffmpeg" || cmd.Args[len(cmd.Args)-1] != dryOut {
		t.Fatalf("unexpected command shape: %s", cmd.String())
	}
	var stderr bytes.Buffer
	run := exec.Command(cmd.Binary, cmd.Args...)
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("running DryRun command: %v\n%s", err, tail(stderr.String(), 20))
	}
	if err := tl.ExportSilent(testProfile(), expOut); err != nil {
		t.Fatal(err)
	}

	md5 := func(p string) string {
		return string(runFFmpeg(t, "-v", "error", "-i", p, "-map", "0",
			"-c:v", "rawvideo", "-c:a", "pcm_s16le", "-f", "md5", "-"))
	}
	if a, b := md5(dryOut), md5(expOut); a != b {
		t.Errorf("decoded output differs: DryRun command %s vs Export %s", a, b)
	}
}
