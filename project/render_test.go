package project

import (
	"strings"
	"testing"

	"github.com/ahmedhodiani/gomontage/export"
)

func TestRenderTarget(t *testing.T) {
	p := New(t.TempDir(), Format{Width: 320, Height: 180, FPS: 30})
	if _, _, err := RenderTarget(p, "", ""); err == nil || !strings.Contains(err.Error(), "no output preset") {
		t.Errorf("err = %v", err)
	}
	p.Output = &Output{Preset: "reel", Path: "out/r.mp4"}
	prof, path, err := RenderTarget(p, "", "")
	if err != nil || prof.Name != export.Reel().Name || path != p.Resolve("out/r.mp4") {
		t.Errorf("RenderTarget = %v %q %v", prof, path, err)
	}
	prof, path, err = RenderTarget(p, "wav", "x.wav")
	if err != nil || prof.Name != "WAV" || path != "x.wav" {
		t.Errorf("override: %v %q %v", prof, path, err)
	}
	if _, _, err := RenderTarget(p, "wav", "x.mp4"); err == nil || !strings.Contains(err.Error(), `doesn't suit preset "wav"`) {
		t.Errorf("extension check: %v", err)
	}
	if _, _, err := RenderTarget(p, "tiktok", "x.mp4"); err == nil || !strings.Contains(err.Error(), "unknown output preset") {
		t.Errorf("unknown preset: %v", err)
	}
	if prof.Fit != FitContain {
		t.Errorf("default fit %q", prof.Fit)
	}
	p.Output.Fit = FitCover
	if prof, _, err = RenderTarget(p, "", ""); err != nil || prof.Fit != FitCover {
		t.Errorf("cover: %v %v", prof, err)
	}
}
