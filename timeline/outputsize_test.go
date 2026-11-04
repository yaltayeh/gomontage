package timeline

import (
	"testing"

	"github.com/ahmedhodiani/gomontage/export"
)

func TestResolveOutputSize(t *testing.T) {
	hd := Config{Width: 1920, Height: 1080, FPS: 30}
	tests := []struct {
		name         string
		cfg          Config
		pw, ph       int
		wantW, wantH int
	}{
		{"no profile size keeps timeline", hd, 0, 0, 0, 0},
		{"same size is a no-op", hd, 1920, 1080, 0, 0},
		{"both set", hd, 3840, 2160, 3840, 2160},
		{"different aspect kept as given", hd, 1080, 1920, 1080, 1920},
		{"width only derives height", hd, 1280, 0, 1280, 720},
		{"height only derives width", hd, 0, 720, 1280, 720},
		{"derived size is even", Config{Width: 1000, Height: 333}, 501, 0, 501, 166},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &export.Profile{Width: tt.pw, Height: tt.ph}
			w, h := resolveOutputSize(tt.cfg, p)
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("resolveOutputSize = %dx%d, want %dx%d", w, h, tt.wantW, tt.wantH)
			}
		})
	}
	if w, h := resolveOutputSize(hd, nil); w != 0 || h != 0 {
		t.Errorf("nil profile: %dx%d, want 0x0", w, h)
	}
}
