package export

import "testing"

func TestOutputStreams(t *testing.T) {
	tests := []struct {
		name         string
		p            *Profile
		path         string
		wantV, wantA bool
	}{
		{"YouTube mp4", YouTube1080p(), "out.mp4", true, true},
		{"ProRes mov", ProRes(), "out.mov", true, true},
		{"WAV preset", WAV(), "out.wav", false, true},
		{"MP3 preset", MP3(), "out", false, true},
		{"FLAC preset", FLAC(), "out.flac", false, true},
		{"GIF preset", GIF(), "out.gif", true, false},
		{"format from extension", NewProfile().Build(), "song.M4A", false, true},
		{"gif extension", NewProfile().Build(), "anim.gif", true, false},
		{"still image (image2)", &Profile{Format: "image2"}, "frame.jpg", true, false},
		{"jpg extension", NewProfile().Build(), "frame.JPG", true, false},
		{"png extension", NewProfile().Build(), "frame.png", true, false},
		{"unknown extension", NewProfile().Build(), "out.xyz", true, true},
	}
	for _, tt := range tests {
		v, a := tt.p.OutputStreams(tt.path)
		if v != tt.wantV || a != tt.wantA {
			t.Errorf("%s: OutputStreams = (%v,%v), want (%v,%v)", tt.name, v, a, tt.wantV, tt.wantA)
		}
	}
}
