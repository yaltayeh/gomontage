package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ahmedhodiani/gomontage/project"
)

// problemText formats a problem for agents: the message (which names the
// item it is about), prefixed with the JSON pointer only when the message
// names nothing (schema errors).
func problemText(p project.Problem) string {
	if p.Path == "" || strings.Contains(p.Message, `"`) {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// clipEnd returns the clip's timeline end in ms.
func clipEnd(p *project.Project, c *project.Clip) int64 { return c.AtMS + p.ClipDuration(c) }

// projectDuration is the end of the last clip on an enabled track.
func projectDuration(p *project.Project) int64 {
	var d int64
	for ti := range p.Tracks {
		t := &p.Tracks[ti]
		if !t.IsEnabled() {
			continue
		}
		for ci := range t.Clips {
			d = max(d, clipEnd(p, &t.Clips[ci]))
		}
	}
	return d
}

// sortedClipIndexes returns the track's clip indexes ordered by at_ms.
func sortedClipIndexes(t *project.Track) []int {
	idx := make([]int, len(t.Clips))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return t.Clips[idx[a]].AtMS < t.Clips[idx[b]].AtMS })
	return idx
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// quote shortens text to n runes and quotes it.
func quote(s string, n int) string {
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n-1]) + "…"
	}
	return strconv.Quote(s)
}

// clipSummary is a one-line description of a clip: type, source, range and
// the properties that differ from their defaults.
func clipSummary(p *project.Project, c *project.Clip) string {
	var parts []string
	head := c.Type
	switch c.Type {
	case project.ClipVideo, project.ClipAudio, project.ClipImage:
		head += " " + c.Media
	case project.ClipText:
		head += " " + quote(c.Text, 40)
	case project.ClipColor:
		head += " " + c.Color
	}
	parts = append(parts, head, fmt.Sprintf("%d–%d", c.AtMS, clipEnd(p, c)))
	if c.HasSourceWindow() {
		out, _ := p.ClipOut(c)
		parts = append(parts, fmt.Sprintf("src %d–%d", c.InMS, out))
	}
	if c.Label != "" {
		parts = append(parts, "label "+quote(c.Label, 30))
	}
	if c.Type == project.ClipVideo && !c.IncludesAudio() {
		parts = append(parts, "no audio")
	}
	if c.Volume != nil {
		parts = append(parts, "vol "+num(*c.Volume))
	}
	if c.FadeInMS > 0 {
		parts = append(parts, fmt.Sprintf("fade_in %d", c.FadeInMS))
	}
	if c.FadeOutMS > 0 {
		parts = append(parts, fmt.Sprintf("fade_out %d", c.FadeOutMS))
	}
	if t := c.TransitionIn; t != nil {
		parts = append(parts, fmt.Sprintf("%s_in %d", t.Type, t.DurationMS))
	}
	for _, e := range c.Effects {
		switch e.Type {
		case project.EffectSpeed:
			s := "speed " + num(e.Factor)
			if !e.AffectsAudio() {
				s += " (audio unchanged)"
			}
			parts = append(parts, s)
		case project.EffectNormalize:
			parts = append(parts, fmt.Sprintf("normalize %s LUFS", num(e.TargetLUFSValue())))
		}
	}
	if c.Position != nil {
		parts = append(parts, fmt.Sprintf("pos %s,%s", num(c.Position.X), num(c.Position.Y)))
	}
	if c.Anchor != "" {
		parts = append(parts, "anchor "+c.Anchor)
	}
	if s := c.Size; s != nil {
		w, h := "auto", "auto"
		if s.Width > 0 {
			w = num(s.Width)
		}
		if s.Height > 0 {
			h = num(s.Height)
		}
		sz := "size " + w + "x" + h
		if s.Fit != "" {
			sz += " " + s.Fit
		}
		parts = append(parts, sz)
	}
	if st := c.Style; st != nil {
		var sp []string
		if st.Size != nil {
			sp = append(sp, num(*st.Size))
		}
		if st.Color != "" {
			sp = append(sp, st.Color)
		}
		if st.Font != nil {
			if st.Font.Family != "" {
				sp = append(sp, strconv.Quote(st.Font.Family))
			} else if st.Font.File != "" {
				sp = append(sp, st.Font.File)
			}
		}
		if st.Box != nil {
			sp = append(sp, "box")
		}
		if st.Outline != nil {
			sp = append(sp, "outline")
		}
		if len(sp) > 0 {
			parts = append(parts, "style "+strings.Join(sp, " "))
		}
	}
	return strings.Join(parts, " · ")
}

// mediaSummary is a one-line description of a media entry.
func mediaSummary(m *project.Media) string {
	pr := m.Probe
	parts := []string{m.Kind}
	switch m.Kind {
	case project.KindVideo:
		parts = append(parts, fmt.Sprintf("%dms %dx%d %sfps", pr.DurationMS, pr.Width, pr.Height, num(pr.FPS)))
		if pr.HasAudio {
			parts = append(parts, "+audio")
		} else {
			parts = append(parts, "no audio")
		}
	case project.KindAudio:
		parts = append(parts, fmt.Sprintf("%dms %dHz %dch", pr.DurationMS, pr.SampleRate, pr.Channels))
	case project.KindImage:
		parts = append(parts, fmt.Sprintf("%dx%d", pr.Width, pr.Height))
	}
	parts = append(parts, m.Path)
	return strings.Join(parts, " · ")
}

// timelineText renders the summary timeline_get returns. With detail, each
// clip is followed by its full JSON as stored in the file.
func timelineText(e *entry, detail bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project %s\n", e.path)
	p := e.proj
	if p == nil {
		b.WriteString("INVALID: the file can't be loaded until these are fixed:\n")
		var ve *project.ValidationError
		if errors.As(e.loadErr, &ve) {
			for _, pr := range ve.Problems {
				b.WriteString("  error " + problemText(pr) + "\n")
			}
		} else if e.loadErr != nil {
			b.WriteString("  error " + e.loadErr.Error() + "\n")
		}
		return b.String()
	}
	if p.Name != "" {
		fmt.Fprintf(&b, "name %s\n", strconv.Quote(p.Name))
	}
	fmt.Fprintf(&b, "format %dx%d @ %sfps · duration %d ms\n", p.Format.Width, p.Format.Height, num(p.Format.FPS), projectDuration(p))
	if o := p.Output; o != nil {
		s := "output"
		if o.Preset != "" {
			s += " preset " + o.Preset
		}
		if o.Path != "" {
			s += " → " + o.Path
		}
		if o.Fit != "" {
			s += " · fit " + o.Fit
		}
		b.WriteString(s + "\n")
	}

	fmt.Fprintf(&b, "\nmedia (%d)\n", len(p.Media))
	for i := range p.Media {
		fmt.Fprintf(&b, "  %s · %s\n", p.Media[i].ID, mediaSummary(&p.Media[i]))
	}

	fmt.Fprintf(&b, "\ntracks (%d; video tracks listed first draw at the bottom)\n", len(p.Tracks))
	for ti := range p.Tracks {
		t := &p.Tracks[ti]
		head := fmt.Sprintf("  %s · %s", t.ID, t.Kind)
		if t.Name != "" {
			head += " " + strconv.Quote(t.Name)
		}
		head += " · " + plural(len(t.Clips), "clip")
		if !t.IsEnabled() {
			head += " · DISABLED"
		}
		b.WriteString(head + "\n")
		var cursor int64
		for _, ci := range sortedClipIndexes(t) {
			c := &t.Clips[ci]
			if c.AtMS > cursor {
				fmt.Fprintf(&b, "    (gap %d–%d)\n", cursor, c.AtMS)
			}
			fmt.Fprintf(&b, "    %s · %s\n", c.ID, clipSummary(p, c))
			if detail {
				fmt.Fprintf(&b, "      %s\n", compactJSON(c))
			}
			cursor = max(cursor, clipEnd(p, c))
		}
	}

	b.WriteString("\n" + checkText(project.Check(p), e.warnings))
	return b.String()
}

// checkText reports a project.Check result compactly.
func checkText(r *project.CheckResult, loadWarnings []project.Warning) string {
	var b strings.Builder
	warnings := r.Warnings
	if len(warnings) == 0 {
		warnings = loadWarnings
	}
	if r.OK() && len(warnings) == 0 {
		return "check: ok (renderable)\n"
	}
	state := "renderable"
	switch {
	case len(r.ProjectErrors) > 0:
		state = "INVALID"
	case len(r.RenderBlockers) > 0:
		state = "not renderable yet"
	}
	fmt.Fprintf(&b, "check: %s · %s · %s · %s\n", state, plural(len(r.ProjectErrors), "error"), plural(len(r.RenderBlockers), "render blocker"), plural(len(warnings), "warning"))
	for _, p := range r.ProjectErrors {
		b.WriteString("  error " + problemText(p) + "\n")
	}
	for _, p := range r.RenderBlockers {
		b.WriteString("  blocker " + problemText(p) + "\n")
	}
	for _, p := range warnings {
		b.WriteString("  warning " + problemText(p) + "\n")
	}
	return b.String()
}

// compactJSON encodes v on one line without HTML escaping.
func compactJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("<%v>", err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
