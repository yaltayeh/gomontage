package timeline

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/effects"
	"github.com/ahmedhodiani/gomontage/engine"
	"github.com/ahmedhodiani/gomontage/export"
)

// goldenTimelines are timelines without transitions covering every clip
// type, track kind, effect and profile path. Their FFmpeg commands are
// recorded in testdata/no_transitions.golden (generated before transitions
// were implemented), so adding transitions can't change the command of any
// timeline that doesn't use them. Regenerate with GOMONTAGE_UPDATE_GOLDEN=1
// only for an intended change.
func goldenTimelines() []struct {
	name    string
	profile *export.Profile
	tl      *Timeline
} {
	sec := time.Second
	video := func(path string, d time.Duration, w, h int, audio bool) *clip.VideoClip {
		info := &engine.MediaInfo{Path: path, Duration: d, VideoStreams: []engine.VideoStreamInfo{{Width: w, Height: h, FPS: 25}}}
		if audio {
			info.AudioStreams = []engine.AudioStreamInfo{{SampleRate: 48000, Channels: 2}}
		}
		return clip.NewVideoFromInfo(path, info)
	}
	audio := func(path string, d time.Duration) *clip.AudioClip {
		return clip.NewAudioFromInfo(path, &engine.MediaInfo{Path: path, Duration: d, AudioStreams: []engine.AudioStreamInfo{{SampleRate: 48000, Channels: 2}}})
	}
	style := clip.DefaultTextStyle()

	var out []struct {
		name    string
		profile *export.Profile
		tl      *Timeline
	}
	add := func(name string, p *export.Profile, tl *Timeline) {
		out = append(out, struct {
			name    string
			profile *export.Profile
			tl      *Timeline
		}{name, p, tl})
	}

	// 1. One trimmed clip, starting late.
	tl := New(Config{Width: 1280, Height: 720, FPS: 25})
	tl.AddVideoTrack("main").Add(video("a.mp4", 20*sec, 1920, 1080, true).Trim(2*sec, 7*sec), 1*sec)
	add("single-trimmed", export.YouTube720p(), tl)

	// 2. A base sequence with gaps, fades, speed effects and kept sound.
	tl = New(Config{Width: 1920, Height: 1080, FPS: 25})
	m := tl.AddVideoTrack("main")
	m.Add(video("a.mp4", 60*sec, 1920, 1080, true).Trim(0, 4*sec).WithFadeIn(sec), 0)
	m.Add(video("b.mp4", 60*sec, 1280, 720, true).Trim(10*sec, 18*sec).WithEffect(effects.SpeedUp(2)).WithFadeOut(500*time.Millisecond), 4*sec)
	m.Add(video("c.mp4", 30*sec, 3840, 2160, false).Trim(1*sec, 3*sec).WithEffect(effects.SlowDown(2)), 9*sec)
	m.Add(video("b.mp4", 60*sec, 1280, 720, true).Trim(20*sec, 24*sec).WithEffect(effects.SlowDown(2).KeepAudioSpeed()).WithVolume(0.4), 13*sec)
	m.Add(clip.NewColor("#112233", 0, 0).WithDuration(2*sec), 21*sec)
	m.Add(clip.NewImage("still.png").WithDuration(2*sec).WithFadeIn(sec), 23*sec)
	m.Add(clip.NewText("Hello مرحبا", style).WithDuration(2*sec), 25*sec)
	m.Add(video("a.mp4", 60*sec, 1920, 1080, true).Trim(30*sec, 34*sec).WithEffect(effects.SpeedUp(4)).WithEffect(effects.AudioSpeed(4)), 27*sec)
	add("base-sequence", export.YouTube1080p(), tl)

	// 3. Base + upper tracks: logo, text, color, positioned video with sound.
	tl = New(Config{Width: 1920, Height: 1080, FPS: 30})
	tl.AddVideoTrack("main").Add(video("a.mp4", 60*sec, 1920, 1080, true).Trim(0, 20*sec), 0)
	up := tl.AddVideoTrack("overlays")
	up.Add(clip.NewImage("logo.png").WithDuration(5*sec).WithLayout(clip.Layout{X: 0.95, Y: 0.05, Anchor: clip.AnchorTopRight, Width: 0.1}).WithFadeIn(sec), 1*sec)
	up.Add(clip.NewText("Title", style).WithDuration(4*sec).WithFadeIn(500*time.Millisecond).WithFadeOut(sec), 6*sec)
	up.Add(clip.NewColor("#00000080", 0, 0).WithDuration(3*sec).WithLayout(clip.Layout{X: 0.5, Y: 1, Anchor: clip.AnchorBottom, Width: 1, Height: 0.2, Fit: clip.FitStretch}), 10*sec)
	up.Add(video("pip.mp4", 30*sec, 1280, 720, true).Trim(5*sec, 10*sec).WithPosition(clip.BottomRight).WithSize(480, 270).WithFadeOut(sec), 14*sec)
	tl.AddVideoTrack("top").Add(clip.NewText("Top", style).WithDuration(3*sec), 2*sec)
	add("upper-tracks", export.YouTube1080p(), tl)

	// 4. Audio tracks with overlaps, fades, volume, normalize, tempo.
	tl = New(Config{Width: 1280, Height: 720, FPS: 25})
	tl.AddVideoTrack("main").Add(clip.NewColor("black", 0, 0).WithDuration(30*sec), 0)
	mu := tl.AddAudioTrack("music")
	mu.Add(audio("song.mp3", 180*sec).Trim(30*sec, 45*sec).WithVolume(0.8).WithFadeIn(2*sec).WithEffect(effects.NormalizeTo(-14)), 0)
	mu.Add(audio("song.mp3", 180*sec).Trim(100*sec, 112*sec).WithFadeOut(3*sec), 12*sec)
	mu.Add(audio("song.mp3", 180*sec).Trim(0, 10*sec).WithEffect(effects.AudioSpeed(2)), 20*sec)
	tl.AddAudioTrack("voice").Add(audio("voice.wav", 20*sec), 3*sec)
	add("audio-tracks", export.YouTube720p(), tl)

	// 5. Output size, cover fit, and stream dropping.
	tl = New(Config{Width: 1920, Height: 1080, FPS: 30})
	tl.AddVideoTrack("main").AddSequence(video("a.mp4", 60*sec, 1920, 1080, true).Trim(0, 3*sec), video("b.mp4", 60*sec, 1080, 1920, true).Trim(0, 3*sec).WithLayout(clip.Layout{X: 0.5, Y: 0.5, Width: 0.5}))
	add("reel-contain", export.Reel(), tl)
	cover := export.Reel()
	cover.Fit = "cover"
	add("reel-cover", cover, tl)
	add("gif", export.GIF(), tl)
	add("mp3", export.MP3(), tl)

	// 6. Generated-only base track with a front gap.
	tl = New(Config{Width: 640, Height: 360, FPS: 24})
	g := tl.AddVideoTrack("main")
	g.Add(clip.NewText("A", style).WithDuration(sec), 2*sec)
	g.Add(clip.NewColor("red", 320, 180).WithDuration(sec), 4*sec)
	add("generated-gaps", export.YouTube720p(), tl)

	return out
}

func goldenDump(t *testing.T) []byte {
	var buf bytes.Buffer
	for _, c := range goldenTimelines() {
		out := "out.mp4"
		switch c.name {
		case "gif":
			out = "out.gif"
		case "mp3":
			out = "out.mp3"
		}
		cmd, err := c.tl.DryRun(c.profile, out)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		fmt.Fprintf(&buf, "== %s\n", c.name)
		for _, a := range cmd.Args {
			fmt.Fprintf(&buf, "%s\n", a)
		}
	}
	return buf.Bytes()
}

func TestDryRun_NoTransitionsGolden(t *testing.T) {
	path := filepath.Join("testdata", "no_transitions.golden")
	got := goldenDump(t)
	if os.Getenv("GOMONTAGE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		gl, wl := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if !bytes.Equal(gl[i], wl[i]) {
				t.Fatalf("command changed at line %d:\ngot  %s\nwant %s", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("command changed: %d lines, want %d", len(gl), len(wl))
	}
}
