package project

import "strings"

// canonicalize rewrites p in place so that every field that equals its
// specified default holds the Go zero value (nil / "" / 0), and fields that
// have no effect for their context are cleared. Load calls it so a loaded
// project is canonical; Save calls it on a copy so files written from
// programmatically built projects are canonical too. The result is that
// Load→Save→Load yields an identical struct and Save is byte-stable.
func canonicalize(p *Project) {
	if len(p.Meta) == 0 {
		p.Meta = nil
	}
	if p.Media == nil {
		p.Media = []Media{}
	}
	if p.Tracks == nil {
		p.Tracks = []Track{}
	}
	for i := range p.Media {
		m := &p.Media[i]
		if f := m.Fingerprint; f != nil && f.SizeBytes == nil && f.ModifiedUnixMS == nil {
			m.Fingerprint = nil
		}
	}
	for ti := range p.Tracks {
		t := &p.Tracks[ti]
		if t.Enabled != nil && *t.Enabled {
			t.Enabled = nil
		}
		if t.Clips == nil {
			t.Clips = []Clip{}
		}
		for ci := range t.Clips {
			canonicalizeClip(p, &t.Clips[ci])
		}
	}
	if o := p.Output; o != nil && o.Fit == FitContain {
		o.Fit = ""
	}
}

func canonicalizeClip(p *Project, c *Clip) {
	if c.IncludeAudio != nil && *c.IncludeAudio {
		c.IncludeAudio = nil
	}
	if c.Volume != nil && *c.Volume == DefaultVolume {
		c.Volume = nil
	}
	if c.HasSourceWindow() && c.OutMS != 0 {
		if m := p.MediaByID(c.Media); m != nil && c.OutMS == m.Probe.DurationMS {
			c.OutMS = 0
		}
	}
	if len(c.Effects) == 0 {
		c.Effects = nil
	}
	for i := range c.Effects {
		e := &c.Effects[i]
		if e.AffectAudio != nil && *e.AffectAudio {
			e.AffectAudio = nil
		}
		if e.TargetLUFS != nil && *e.TargetLUFS == DefaultTargetLUFS {
			e.TargetLUFS = nil
		}
	}
	if c.Position != nil && *c.Position == DefaultPosition {
		c.Position = nil
	}
	if c.Anchor == DefaultAnchor {
		c.Anchor = ""
	}
	if s := c.Size; s != nil {
		both := s.Width != 0 && s.Height != 0
		if !both || s.Fit == FitContain {
			s.Fit = "" // ignored with one dimension; "contain" is the default
		}
		if both && s.Width == 1 && s.Height == 1 && s.Fit == "" {
			c.Size = nil // the whole canvas, contain: the default
		}
	}
	if st := c.Style; st != nil {
		if st.Size != nil && *st.Size == DefaultTextSize {
			st.Size = nil
		}
		if strings.EqualFold(st.Color, DefaultTextColor) || strings.EqualFold(st.Color, DefaultTextColor+"FF") {
			st.Color = ""
		}
		if st.Align == DefaultAlign {
			st.Align = ""
		}
		if st.Direction == DefaultDirection {
			st.Direction = ""
		}
		if b := st.Box; b != nil && b.Padding != nil && *b.Padding == DefaultBoxPadding {
			b.Padding = nil
		}
		if o := st.Outline; o != nil && o.Width != nil && *o.Width == DefaultOutlineW {
			o.Width = nil
		}
		if *st == (TextStyle{}) {
			c.Style = nil
		}
	}
}
