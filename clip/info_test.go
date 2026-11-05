package clip

import (
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/engine"
)

func TestNewVideoFromInfo(t *testing.T) {
	info := &engine.MediaInfo{
		Duration:     6 * time.Second,
		VideoStreams: []engine.VideoStreamInfo{{Width: 1280, Height: 720, FPS: 30}},
	}
	c := NewVideoFromInfo("a.mp4", info)
	if c.Duration() != 6*time.Second || c.TrimEnd() != 6*time.Second {
		t.Errorf("duration %s trimEnd %s, want 6s", c.Duration(), c.TrimEnd())
	}
	if c.HasAudio() {
		t.Error("HasAudio = true for info without audio streams")
	}
	if !c.HasVideo() || c.Width() != 1280 || c.Height() != 720 {
		t.Errorf("video %v %dx%d", c.HasVideo(), c.Width(), c.Height())
	}
	if c.SourcePath() != "a.mp4" || c.Volume() != 1 {
		t.Errorf("path %q volume %g", c.SourcePath(), c.Volume())
	}
	// The source length is recorded, so trims past the end are reported.
	if err := SourceProblem(c.Trim(0, 7*time.Second)); err == nil {
		t.Error("expected SourceProblem for a trim past the probed end")
	}

	info.AudioStreams = []engine.AudioStreamInfo{{SampleRate: 48000, Channels: 2}}
	if !NewVideoFromInfo("a.mp4", info).HasAudio() {
		t.Error("HasAudio = false for info with an audio stream")
	}
}

func TestNewAudioFromInfo(t *testing.T) {
	c := NewAudioFromInfo("a.wav", &engine.MediaInfo{Duration: 3 * time.Second})
	if c.Duration() != 3*time.Second || !c.HasAudio() || c.HasVideo() {
		t.Errorf("duration %s audio %v video %v", c.Duration(), c.HasAudio(), c.HasVideo())
	}
	if err := SourceProblem(c.Trim(time.Second, 4*time.Second)); err == nil {
		t.Error("expected SourceProblem for a trim past the probed end")
	}
}
