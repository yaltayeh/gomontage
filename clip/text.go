package clip

import (
	"time"

	"github.com/ahmedhodiani/gomontage/effects"
)

// TextStyle configures the visual appearance of a TextClip.
type TextStyle struct {
	// Font is the path to a .ttf or .otf font file. Takes precedence over
	// FontFamily. If both are empty, the system's default sans-serif font is
	// used.
	Font string

	// FontFamily is an installed font family name (e.g. "Noto Sans Arabic"),
	// looked up with fontconfig. Used when Font is empty.
	FontFamily string

	// Size is the font size in points. Default is 48.
	Size int

	// Color is the text color as a hex string ("#RRGGBB", or "#RRGGBBAA"
	// with alpha) or a named color. Default is white.
	Color string

	// Background is the color of a box drawn behind the text ("#RRGGBBAA"
	// allowed). Empty means no box.
	Background string

	// BorderWidth is the width of the outline drawn around each letter, in
	// pixels. 0 = no outline.
	BorderWidth int

	// BorderColor is the outline color. Default is black.
	BorderColor string

	// Position is the legacy pixel position of the text box's top-left
	// corner (see clip.Position). The zero value means "not set": the text is
	// centred on the canvas. Prefer TextClip.WithLayout.
	Position Position

	// BoxPadding is the space between the text and the edge of the
	// Background box, in pixels.
	BoxPadding int

	// Align aligns the lines of multi-line text: "left", "center" or
	// "right". Empty uses FFmpeg's default (left).
	Align string

	// Direction is the text direction: "" or "auto" (detected from the
	// text by FriBidi), "ltr" or "rtl" (forces the paragraph direction, e.g.
	// so a mixed Arabic/English line reads right to left).
	Direction string
}

// DefaultTextStyle returns a TextStyle with sensible defaults.
func DefaultTextStyle() TextStyle {
	return TextStyle{
		Size:        48,
		Color:       "#FFFFFF",
		BorderColor: "#000000",
		BorderWidth: 2,
	}
}

// TextClip represents a dynamically generated text overlay.
type TextClip struct {
	Base

	// Text is the string content to render.
	Text string

	// Style controls the visual appearance.
	Style TextStyle
}

// NewText creates a new TextClip with the given text and style.
// The default duration is 5 seconds — use WithDuration to change it.
//
// Example:
//
//	title := clip.NewText("Chapter 1: The Beginning", clip.TextStyle{
//	    Font:  "resources/fonts/bold.ttf",
//	    Size:  72,
//	    Color: "#FFFFFF",
//	}).WithDuration(4 * time.Second)
func NewText(text string, style TextStyle) *TextClip {
	if style.Size == 0 {
		style.Size = DefaultTextStyle().Size
	}
	if style.Color == "" {
		style.Color = DefaultTextStyle().Color
	}
	return &TextClip{
		Base: Base{
			clipType: TypeText,
			duration: 5 * time.Second,
			trimEnd:  5 * time.Second,
			hasVideo: true,
			volume:   1.0,
			position: style.Position,
			// A non-zero TextStyle.Position counts as WithPosition.
			positionSet: style.Position != (Position{}),
		},
		Text:  text,
		Style: style,
	}
}

// WithDuration returns a new TextClip with the specified duration.
func (c *TextClip) WithDuration(d time.Duration) *TextClip {
	n := &TextClip{Base: *c.base(), Text: c.Text, Style: c.Style}
	n.duration = d
	n.trimEnd = d
	return n
}

// WithPosition returns a new TextClip placed at the given position.
func (c *TextClip) WithPosition(pos Position) *TextClip {
	n := &TextClip{Base: *c.base(), Text: c.Text, Style: c.Style}
	n.position = pos
	n.positionSet = true
	return n
}

// WithFadeIn returns a new TextClip with a fade-in effect.
func (c *TextClip) WithFadeIn(d time.Duration) *TextClip {
	n := &TextClip{Base: *c.base(), Text: c.Text, Style: c.Style}
	n.fadeIn = d
	return n
}

// WithFadeOut returns a new TextClip with a fade-out effect.
func (c *TextClip) WithFadeOut(d time.Duration) *TextClip {
	n := &TextClip{Base: *c.base(), Text: c.Text, Style: c.Style}
	n.fadeOut = d
	return n
}

// WithEffect returns a new TextClip with the given effect appended.
// Effects are composable — call WithEffect multiple times to stack effects.
//
// Example:
//
//	clip.NewText("Chapter 1", clip.DefaultTextStyle()).
//	    WithDuration(4 * time.Second).
//	    WithEffect(effects.FadeIn(1 * time.Second))
func (c *TextClip) WithEffect(e effects.Effect) *TextClip {
	n := &TextClip{Base: *c.base(), Text: c.Text, Style: c.Style}
	n.applyEffect(e)
	return n
}

// WithLayout returns a new TextClip placed on the canvas with l (fractions of
// the canvas, an anchor and an optional size; see Layout). It takes
// precedence over WithPosition/WithSize.
func (c *TextClip) WithLayout(l Layout) *TextClip {
	n := &TextClip{Base: *c.base(), Text: c.Text, Style: c.Style}
	n.layout = &l
	return n
}
