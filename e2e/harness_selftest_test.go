//go:build e2e

package e2e

import (
	"image/png"
	"os"
	"testing"
	"time"
)

// TestHarness_Fixtures verifies the fixture generators and probe/pixel/audio
// helpers before any GoMontage behavior is tested with them.
func TestHarness_Fixtures(t *testing.T) {
	requireFFmpeg(t)

	t.Run("frame_rates", func(t *testing.T) {
		for _, fps := range []int{24, 30, 60} {
			p := genVideo(t, VideoSpec{FPS: fps, Dur: time.Second})
			v := probe(t, p).Video()
			if v == nil || int(v.FPS+0.5) != fps {
				t.Errorf("fixture fps %d: probed %+v", fps, v)
			}
		}
	})

	t.Run("resolutions", func(t *testing.T) {
		for _, wh := range [][2]int{{320, 180}, {180, 320}, {240, 240}, {320, 240}} {
			p := genVideo(t, VideoSpec{W: wh[0], H: wh[1], Dur: time.Second})
			v := probe(t, p).Video()
			if v == nil || v.Width != wh[0] || v.Height != wh[1] {
				t.Errorf("fixture %dx%d: probed %+v", wh[0], wh[1], v)
			}
		}
	})

	t.Run("with_and_without_audio", func(t *testing.T) {
		withA := probe(t, genVideo(t, VideoSpec{ToneHz: 440, Dur: time.Second}))
		noA := probe(t, genVideo(t, VideoSpec{Dur: time.Second}))
		if withA.count("audio") != 1 || withA.count("video") != 1 {
			t.Errorf("video+audio fixture streams: %+v", withA.Streams)
		}
		if noA.count("audio") != 0 || noA.count("video") != 1 {
			t.Errorf("video-only fixture streams: %+v", noA.Streams)
		}
	})

	t.Run("tone", func(t *testing.T) {
		p := genTone(t, 440, 2*time.Second)
		m := probe(t, p)
		a := m.Audio()
		if a == nil || a.SampleRate != 48000 || a.Channels != 2 || m.count("video") != 0 {
			t.Fatalf("tone fixture: %+v", m.Streams)
		}
		assertDuration(t, "tone", m.Duration, 2*time.Second, 30)
		lvl := audioLevel(t, p, 0, time.Second)
		if lvl.MeanDB < -40 || lvl.MeanDB > -3 {
			t.Errorf("tone level %.1f dB, want an audible tone", lvl.MeanDB)
		}
	})

	t.Run("color_sequence_pixels", func(t *testing.T) {
		p := genColorSequence(t, 160, 90, 30, []string{"0xFF0000", "0x00FF00", "0x0000FF"})
		assertPixel(t, p, 500*time.Millisecond, 80, 45, Red, colorTol)
		assertPixel(t, p, 1500*time.Millisecond, 80, 45, Green, colorTol)
		assertPixel(t, p, 2500*time.Millisecond, 80, 45, Blue, colorTol)
	})

	t.Run("alpha_png", func(t *testing.T) {
		p := genAlphaPNG(t, 64, 36, 0, 0, 255)
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		img, err := png.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		_, _, b, a := img.At(5, 5).RGBA()
		_, _, _, a2 := img.At(60, 5).RGBA()
		if a>>8 != 255 || b>>8 != 255 || a2 != 0 {
			t.Errorf("alpha png: left alpha=%d blue=%d, right alpha=%d", a>>8, b>>8, a2>>8)
		}
	})
}
