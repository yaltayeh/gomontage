package timeline

import (
	"fmt"
	"math"
	"strings"
	"time"

	"golang.org/x/text/unicode/bidi"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/engine"
)

// vstream is the end of a filter chain: the label of its output and the
// node that produces it.
type vstream struct {
	label string
	node  *engine.Node
}

// then appends a filter to the chain and returns the new end.
func (c *Compiler) then(s vstream, name, prefix string, params map[string]string, kind engine.StreamType) vstream {
	n := c.graph.AddFilter(name, params)
	l := c.nextLabelFor(prefix, n)
	c.graph.Connect(s.node, n, s.label, kind)
	return vstream{label: l, node: n}
}

// streamOf returns the vstream ending at label.
func (c *Compiler) streamOf(label string) vstream {
	return vstream{label: label, node: c.findFilterByLabel(label)}
}

// scaleToBox scales a file-based clip (video or image) to its layout box.
// The whole-canvas box uses contain without padding, so the space around the
// picture stays empty (transparent on upper tracks).
func (c *Compiler) scaleToBox(s vstream, pl clip.Placement, cfg Config) vstream {
	itoa := func(v int) string { return fmt.Sprintf("%d", v) }
	switch {
	case pl.Native:
		// keep the source size
	case pl.Whole():
		s = c.then(s, "scale", "lscale", map[string]string{
			"w": itoa(cfg.Width), "h": itoa(cfg.Height), "force_original_aspect_ratio": "decrease",
		}, engine.StreamVideo)
	case pl.BoxW > 0 && pl.BoxH > 0:
		switch pl.Fit {
		case clip.FitStretch:
			s = c.then(s, "scale", "lscale", map[string]string{"w": itoa(pl.BoxW), "h": itoa(pl.BoxH)}, engine.StreamVideo)
		case clip.FitCover:
			s = c.then(s, "scale", "lscale", map[string]string{
				"w": itoa(pl.BoxW), "h": itoa(pl.BoxH), "force_original_aspect_ratio": "increase",
			}, engine.StreamVideo)
			s = c.then(s, "crop", "lcrop", map[string]string{"w": itoa(pl.BoxW), "h": itoa(pl.BoxH)}, engine.StreamVideo)
		default: // contain
			s = c.then(s, "scale", "lscale", map[string]string{
				"w": itoa(pl.BoxW), "h": itoa(pl.BoxH), "force_original_aspect_ratio": "decrease",
			}, engine.StreamVideo)
		}
	case pl.BoxW > 0:
		s = c.then(s, "scale", "lscale", map[string]string{"w": itoa(pl.BoxW), "h": "-1"}, engine.StreamVideo)
	default:
		s = c.then(s, "scale", "lscale", map[string]string{"w": "-1", "h": itoa(pl.BoxH)}, engine.StreamVideo)
	}
	return c.then(s, "setsar", "lsar", map[string]string{"ratio": "1/1"}, engine.StreamVideo)
}

// colorBox returns the size of a color clip's box: the whole canvas, the
// given box, or one dimension with the other following the canvas aspect.
func colorBox(pl clip.Placement, cfg Config) (int, int) {
	switch {
	case pl.Whole() || pl.Native:
		return cfg.Width, cfg.Height
	case pl.BoxW > 0 && pl.BoxH > 0:
		return pl.BoxW, pl.BoxH
	case pl.BoxW > 0:
		return pl.BoxW, int(math.Round(float64(pl.BoxW) * float64(cfg.Height) / float64(cfg.Width)))
	default:
		return int(math.Round(float64(pl.BoxH) * float64(cfg.Width) / float64(cfg.Height))), pl.BoxH
	}
}

// colorHasAlpha reports whether a color string carries an alpha component
// other than fully opaque (#RRGGBBAA with AA != FF, or name@alpha).
func colorHasAlpha(col string) bool {
	h := strings.TrimPrefix(strings.TrimPrefix(col, "#"), "0x")
	if len(h) == 8 {
		return !strings.EqualFold(h[6:], "ff")
	}
	return strings.Contains(col, "@")
}

// overlayXY returns overlay x/y expressions placing a picture of size w×h
// (FFmpeg's overlay variables) at the placement's anchor point.
func overlayXY(pl clip.Placement) (string, string) {
	return fmt.Sprintf("%s-%s*w", formatFloat(pl.PX), formatFloat(pl.AX)),
		fmt.Sprintf("%s-%s*h", formatFloat(pl.PY), formatFloat(pl.AY))
}

// placeOnCanvas composites a positioned clip onto an opaque black canvas of
// the timeline size, for base-track clips with a custom layout (or a
// translucent color). The result is a full-frame, opaque yuv420p stream of
// duration d, like every other base-track clip.
func (c *Compiler) placeOnCanvas(s vstream, pl clip.Placement, d time.Duration, cfg Config) vstream {
	canvas := c.graph.AddFilter("color", map[string]string{
		"c": "black",
		"s": fmt.Sprintf("%dx%d", cfg.Width, cfg.Height),
		"d": formatSeconds(d),
		"r": fmt.Sprintf("%g", cfg.FPS),
	})
	canvasLabel := c.nextLabelFor("canvas", canvas)
	x, y := overlayXY(pl)
	ov := c.graph.AddFilter("overlay", map[string]string{"x": x, "y": y, "eof_action": "pass", "format": "auto"})
	ovLabel := c.nextLabelFor("place", ov)
	c.graph.Connect(canvas, ov, canvasLabel, engine.StreamVideo)
	c.graph.Connect(s.node, ov, s.label, engine.StreamVideo)
	out := vstream{label: ovLabel, node: ov}
	out = c.then(out, "format", "placefmt", map[string]string{"pix_fmts": "yuv420p"}, engine.StreamVideo)
	return c.then(out, "setsar", "placesar", map[string]string{"ratio": "1/1"}, engine.StreamVideo)
}

// textMargin is the space drawn around the text itself (box padding, else
// the outline), included when anchoring so the visible box is placed.
func textMargin(st clip.TextStyle) int {
	if st.Background != "" {
		return st.BoxPadding
	}
	return st.BorderWidth
}

// applyDirection sets each line's paragraph direction with Unicode embedding
// marks (RLE/LRE ... PDF), which FriBidi in drawtext honors. drawtext itself
// always lays out paragraphs left to right, so "auto" (or "") is resolved
// here: a line whose first strong letter is right-to-left (Arabic, Hebrew)
// is made RTL, any other line is left as is. "rtl"/"ltr" force a direction.
func applyDirection(text, dir string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		switch {
		case dir == "rtl" || (dir != "ltr" && firstStrongRTL(l)):
			lines[i] = "\u202B" + l + "\u202C"
		case dir == "ltr":
			lines[i] = "\u202A" + l + "\u202C"
		}
	}
	return strings.Join(lines, "\n")
}

// firstStrongRTL reports whether the first strongly directional character of
// s is right-to-left (Unicode bidi class R or AL).
func firstStrongRTL(s string) bool {
	for _, r := range s {
		switch p, _ := bidi.LookupRune(r); p.Class() {
		case bidi.R, bidi.AL:
			return true
		case bidi.L:
			return false
		}
	}
	return false
}

// drawtextParams returns the drawtext options for a text clip: escaped text
// (with forced direction), font file or family, size, color (alpha allowed),
// box, outline, alignment, and x/y expressions placing the text box (with its
// margin) at the placement's anchor.
func drawtextParams(tc *clip.TextClip, pl clip.Placement) map[string]string {
	st := tc.Style
	p := map[string]string{
		"text":      engine.EscapeFilterValue(applyDirection(tc.Text, st.Direction)),
		"expansion": "none",
		"fontsize":  fmt.Sprintf("%d", st.Size),
		"fontcolor": engine.EscapeFilterValue(st.Color),
	}
	if st.Font != "" {
		p["fontfile"] = engine.EscapeFilterValue(st.Font)
	} else if st.FontFamily != "" {
		p["font"] = engine.EscapeFilterValue(st.FontFamily)
	}
	if st.Background != "" {
		p["box"] = "1"
		p["boxcolor"] = engine.EscapeFilterValue(st.Background)
		p["boxborderw"] = fmt.Sprintf("%d", st.BoxPadding)
	}
	if st.BorderWidth > 0 {
		p["borderw"] = fmt.Sprintf("%d", st.BorderWidth)
		bc := st.BorderColor
		if bc == "" {
			bc = "black"
		}
		p["bordercolor"] = engine.EscapeFilterValue(bc)
	}
	switch st.Align {
	case "left", "center", "right":
		p["text_align"] = st.Align
	}
	m := textMargin(st)
	p["x"] = fmt.Sprintf("%s-%s*(tw+%d)+%d", formatFloat(pl.PX), formatFloat(pl.AX), 2*m, m)
	p["y"] = fmt.Sprintf("%s-%s*(th+%d)+%d", formatFloat(pl.PY), formatFloat(pl.AY), 2*m, m)
	return p
}

// textFadeAlpha returns a drawtext alpha expression fading a text clip drawn
// on the composite between start and end, or "" without fades.
func textFadeAlpha(start, end, fadeIn, fadeOut time.Duration) string {
	var terms []string
	if fadeIn > 0 {
		terms = append(terms, fmt.Sprintf("(t-%s)/%s", formatSeconds(start), formatSeconds(fadeIn)))
	}
	if fadeOut > 0 {
		terms = append(terms, fmt.Sprintf("(%s-t)/%s", formatSeconds(end), formatSeconds(fadeOut)))
	}
	switch len(terms) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("clip(%s,0,1)", terms[0])
	default:
		return fmt.Sprintf("clip(min(%s,%s),0,1)", terms[0], terms[1])
	}
}

// compileOverlayClip draws one upper-track clip onto the composite and
// returns the new composite label and, for video clips with sound, the
// clip's audio label (already delayed to its start time).
//
// Text is drawn directly onto the composite with drawtext, enabled only
// between the clip's start and end. Other clips are compiled with alpha
// (yuva420p), scaled to their layout box (no padding), shifted to their
// start time and overlaid with eof_action=pass, so the layers beneath show
// through everywhere the clip isn't — before, after and around it.
func (c *Compiler) compileOverlayClip(entry Placement, cfg Config, composite string) (string, string, error) {
	cl := entry.Clip
	start := entry.StartAt
	end := start + cl.Duration()
	pl := clip.ResolvePlacement(cl, cfg.Width, cfg.Height)

	if tc, ok := cl.(*clip.TextClip); ok {
		p := drawtextParams(tc, pl)
		p["enable"] = engine.EscapeFilterValue(fmt.Sprintf("between(t,%s,%s)", formatSeconds(start), formatSeconds(end)))
		if a := textFadeAlpha(start, end, cl.FadeInDuration(), cl.FadeOutDuration()); a != "" {
			p["alpha"] = engine.EscapeFilterValue(a)
		}
		out := c.then(c.streamOf(composite), "drawtext", "otext", p, engine.StreamVideo)
		return out.label, "", nil
	}

	cls, err := c.compileVideoEntry(entry, cfg, true)
	if err != nil {
		return "", "", err
	}
	s := c.streamOf(cls[0].video)
	s = c.then(s, "setpts", "oshift", map[string]string{
		"expr": fmt.Sprintf("PTS-STARTPTS+%s/TB", formatSeconds(start)),
	}, engine.StreamVideo)

	x, y := overlayXY(pl)
	ov := c.graph.AddFilter("overlay", map[string]string{"x": x, "y": y, "eof_action": "pass", "format": "auto"})
	ovLabel := c.nextLabelFor("ovl", ov)
	c.graph.Connect(c.findFilterByLabel(composite), ov, composite, engine.StreamVideo)
	c.graph.Connect(s.node, ov, s.label, engine.StreamVideo)

	audio := ""
	if cl.HasAudio() && cl.SourcePath() != "" {
		a, err := c.compileAudioFromVideoEntry(entry)
		if err != nil {
			return "", "", err
		}
		audio = c.delayAudio(a, start)
	}
	return ovLabel, audio, nil
}

// delayAudio delays an audio stream to start at the given timeline time.
func (c *Compiler) delayAudio(label string, start time.Duration) string {
	if start <= 0 {
		return label
	}
	ms := start.Milliseconds()
	out := c.then(c.streamOf(label), "adelay", "odel", map[string]string{
		"delays": fmt.Sprintf("%d|%d", ms, ms),
	}, engine.StreamAudio)
	return out.label
}
