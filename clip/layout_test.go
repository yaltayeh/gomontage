package clip

import (
	"testing"
	"time"
)

func TestResolvePlacement(t *testing.T) {
	const W, H = 1920, 1080
	img := NewImage("x.png").WithDuration(time.Second)

	if p := ResolvePlacement(img, W, H); !p.Default || !p.Whole() || p.PX != 960 || p.PY != 540 || p.AX != 0.5 {
		t.Errorf("default: %+v", p)
	}

	p := ResolvePlacement(img.WithLayout(Layout{X: 0.96, Y: 0.04, Anchor: AnchorTopRight, Width: 0.2}), W, H)
	near := func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }
	if p.Default || !near(p.PX, 1843.2) || !near(p.PY, 43.2) || p.AX != 1 || p.AY != 0 || p.BoxW != 384 || p.BoxH != 0 {
		t.Errorf("layout: %+v", p)
	}
	p = ResolvePlacement(img.WithLayout(Layout{X: 0.5, Y: 0.5, Width: 1, Height: 1, Fit: FitCover}), W, H)
	if p.BoxW != W || p.BoxH != H || p.Fit != FitCover {
		t.Errorf("cover: %+v", p)
	}
	if p := ResolvePlacement(img.WithLayout(Layout{Width: 0.5, Height: 0.5}), W, H); p.Fit != FitContain {
		t.Errorf("fit defaults to contain: %+v", p)
	}

	// Legacy pixel API.
	p = ResolvePlacement(img.WithPosition(Position{X: 200, Y: 100}), W, H)
	if !p.Native || p.PX != 200 || p.PY != 100 || p.AX != 0 || p.AY != 0 {
		t.Errorf("legacy position: %+v", p)
	}
	p = ResolvePlacement(img.WithPosition(BottomRight).WithSize(64, 36), W, H)
	if p.Native || p.PX != W || p.PY != H || p.AX != 1 || p.AY != 1 || p.BoxW != 64 || p.BoxH != 36 || p.Fit != FitStretch {
		t.Errorf("legacy bottom-right + size: %+v", p)
	}
	if p := ResolvePlacement(img.WithPosition(Center), W, H); p.PX != 960 || p.AX != 0.5 || !p.Native {
		t.Errorf("legacy Center: %+v", p)
	}
	if p := ResolvePlacement(img.WithPosition(TopLeft), W, H); p.PX != 0 || p.AX != 0 || p.Default {
		t.Errorf("legacy TopLeft: %+v", p)
	}
	// WithLayout wins over the legacy API.
	if p := ResolvePlacement(img.WithPosition(TopLeft).WithLayout(DefaultLayout()), W, H); p.PX != 960 {
		t.Errorf("layout precedence: %+v", p)
	}

	// Text: a non-zero TextStyle.Position counts as WithPosition; zero = centred.
	if p := ResolvePlacement(NewText("x", DefaultTextStyle()), W, H); !p.Default {
		t.Errorf("text default: %+v", p)
	}
	st := DefaultTextStyle()
	st.Position = Position{X: 10, Y: 20}
	if p := ResolvePlacement(NewText("x", st), W, H); p.PX != 10 || p.PY != 20 || p.AX != 0 {
		t.Errorf("text style position: %+v", p)
	}

	// Colors: full-canvas (or no size) is the default; another size is centred.
	if p := ResolvePlacement(NewColor("#000", W, H), W, H); !p.Default {
		t.Errorf("full-canvas color: %+v", p)
	}
	if p := ResolvePlacement(NewColor("#000", 100, 50), W, H); p.Default || p.BoxW != 100 || p.BoxH != 50 || p.PX != 960 {
		t.Errorf("small color: %+v", p)
	}
}

func TestAnchorFractions(t *testing.T) {
	for a, want := range map[Anchor][2]float64{
		AnchorTopLeft: {0, 0}, AnchorTop: {0.5, 0}, AnchorTopRight: {1, 0},
		AnchorLeft: {0, 0.5}, AnchorCenter: {0.5, 0.5}, AnchorRight: {1, 0.5},
		AnchorBottomLeft: {0, 1}, AnchorBottom: {0.5, 1}, AnchorBottomRight: {1, 1}, "": {0.5, 0.5},
	} {
		if x, y := a.Fractions(); x != want[0] || y != want[1] {
			t.Errorf("%q = %g,%g want %v", a, x, y, want)
		}
	}
}
