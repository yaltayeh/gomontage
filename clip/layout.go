package clip

import "math"

// Anchor names the point of a clip that is placed at its layout position.
type Anchor string

// Anchors.
const (
	AnchorTopLeft     Anchor = "top-left"
	AnchorTop         Anchor = "top"
	AnchorTopRight    Anchor = "top-right"
	AnchorLeft        Anchor = "left"
	AnchorCenter      Anchor = "center"
	AnchorRight       Anchor = "right"
	AnchorBottomLeft  Anchor = "bottom-left"
	AnchorBottom      Anchor = "bottom"
	AnchorBottomRight Anchor = "bottom-right"
)

// Fractions returns the anchor as fractions of the clip's width and height:
// left/top = 0, center = 0.5, right/bottom = 1. "" means center.
func (a Anchor) Fractions() (ax, ay float64) {
	switch a {
	case AnchorTopLeft:
		return 0, 0
	case AnchorTop:
		return 0.5, 0
	case AnchorTopRight:
		return 1, 0
	case AnchorLeft:
		return 0, 0.5
	case AnchorRight:
		return 1, 0.5
	case AnchorBottomLeft:
		return 0, 1
	case AnchorBottom:
		return 0.5, 1
	case AnchorBottomRight:
		return 1, 1
	default:
		return 0.5, 0.5
	}
}

// Fit says how a picture fills a layout box given by both width and height.
type Fit string

// Fit modes.
const (
	// FitContain shows the whole picture, keeping its aspect ratio.
	FitContain Fit = "contain"
	// FitCover fills the box, keeping the aspect ratio and cropping overflow.
	FitCover Fit = "cover"
	// FitStretch fills the box exactly, ignoring the aspect ratio.
	FitStretch Fit = "stretch"
)

// Layout places a visual clip on the canvas using fractions of the canvas,
// so it survives changes of the timeline size (docs/project-format.md §7).
//
// X, Y is the point (0..1 of canvas width/height) where the clip's Anchor is
// placed. Width/Height are fractions of the canvas width/height; give one
// (the other follows the clip's aspect ratio), both (filled according to
// Fit), or neither (the clip is as large as possible inside the whole canvas,
// i.e. contain).
type Layout struct {
	X, Y   float64
	Anchor Anchor // "" = center
	Width  float64
	Height float64
	Fit    Fit // "" = contain; only used when both Width and Height are set
}

// DefaultLayout is the layout a clip has when none is set: centred, as large
// as possible inside the canvas.
func DefaultLayout() Layout {
	return Layout{X: 0.5, Y: 0.5, Anchor: AnchorCenter}
}

// Placement is a clip's layout resolved to pixels for a given canvas.
type Placement struct {
	// Default is true when the clip uses the default layout (centred, whole
	// canvas, contain); the other fields are then also filled in.
	Default bool
	// PX, PY is the anchor point in pixels; AX, AY the anchor fractions.
	PX, PY, AX, AY float64
	// BoxW, BoxH is the box in pixels (0 = derived from the clip's aspect
	// ratio). Both 0 with Native false means the whole canvas.
	BoxW, BoxH int
	// Fit applies when both BoxW and BoxH are set.
	Fit Fit
	// Native keeps the clip at its source size (legacy pixel positioning
	// without WithSize).
	Native bool
}

// Whole reports whether the box is the whole canvas (no size given).
func (p Placement) Whole() bool { return p.BoxW == 0 && p.BoxH == 0 && !p.Native }

// ResolvePlacement resolves a clip's layout for a canvas of w×h pixels.
//
// A Layout set with WithLayout takes precedence. Otherwise the legacy pixel
// API is used: WithPosition places the clip's top-left corner at X, Y pixels
// (X or Y == -1 means the right/bottom edge, -2 the centre, with the clip
// anchored accordingly), and WithSize scales it to exactly that many pixels;
// a clip positioned without WithSize keeps its source size. A TextClip's
// TextStyle.Position counts as WithPosition when it isn't the zero value.
// A ColorClip created with a size other than the canvas is centred.
func ResolvePlacement(c Clip, w, h int) Placement {
	b := c.base()
	if b.layout != nil {
		return layoutPlacement(*b.layout, w, h, false)
	}
	if !b.positionSet && !b.sizeSet {
		if cc, ok := c.(*ColorClip); ok && cc.width > 0 && cc.height > 0 && (cc.width != w || cc.height != h) {
			p := layoutPlacement(DefaultLayout(), w, h, false)
			p.Default, p.BoxW, p.BoxH, p.Fit = false, cc.width, cc.height, FitStretch
			return p
		}
		return layoutPlacement(DefaultLayout(), w, h, true)
	}
	p := Placement{PX: float64(w) / 2, PY: float64(h) / 2, AX: 0.5, AY: 0.5}
	if b.positionSet {
		p.PX, p.AX = legacyAxis(b.position.X, w)
		p.PY, p.AY = legacyAxis(b.position.Y, h)
	}
	if b.sizeSet {
		p.BoxW, p.BoxH, p.Fit = b.width, b.height, FitStretch
	} else {
		p.Native = true
	}
	return p
}

func legacyAxis(v, size int) (pos, anchor float64) {
	switch v {
	case -1:
		return float64(size), 1
	case -2:
		return float64(size) / 2, 0.5
	default:
		return float64(v), 0
	}
}

func layoutPlacement(l Layout, w, h int, isDefault bool) Placement {
	ax, ay := l.Anchor.Fractions()
	p := Placement{
		Default: isDefault,
		PX:      l.X * float64(w), PY: l.Y * float64(h),
		AX: ax, AY: ay,
		BoxW: int(math.Round(l.Width * float64(w))),
		BoxH: int(math.Round(l.Height * float64(h))),
		Fit:  l.Fit,
	}
	if p.Fit == "" {
		p.Fit = FitContain
	}
	return p
}

// Layout returns the layout set with WithLayout, if any.
func (b *Base) Layout() (Layout, bool) {
	if b.layout == nil {
		return Layout{}, false
	}
	return *b.layout, true
}
