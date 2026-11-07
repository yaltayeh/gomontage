package project

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sort"
	"strings"
)

// ruleRun runs the loader rules (specification section 12.2) over a
// schema-valid, canonicalized project.
type ruleRun struct {
	p        *Project
	problems []Problem
	warnings []Warning
}

func (r *ruleRun) fail(path, format string, args ...any) {
	r.problems = append(r.problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (r *ruleRun) warn(path, format string, args ...any) {
	r.warnings = append(r.warnings, Warning{Path: path, Message: fmt.Sprintf(format, args...)})
}

func mediaPath(i int) string   { return fmt.Sprintf("/media/%d", i) }
func trackPath(t int) string   { return fmt.Sprintf("/tracks/%d", t) }
func clipPath(t, c int) string { return fmt.Sprintf("/tracks/%d/clips/%d", t, c) }
func effectPath(t, c, e int) string {
	return fmt.Sprintf("/tracks/%d/clips/%d/effects/%d", t, c, e)
}

// L1: IDs are unique across media, tracks and clips.
func (r *ruleRun) checkIDs() {
	first := map[string]string{}
	check := func(id, path string) {
		if prev, ok := first[id]; ok {
			r.fail(path, "duplicate id %q (already used at %s)", id, prev)
			return
		}
		first[id] = path
	}
	for i, m := range r.p.Media {
		check(m.ID, mediaPath(i))
	}
	for ti, t := range r.p.Tracks {
		check(t.ID, trackPath(ti))
		for ci, c := range t.Clips {
			check(c.ID, clipPath(ti, ci))
		}
	}
}

// placed is a clip whose timeline span is known.
type placed struct {
	c     *Clip
	ci    int
	start int64
	end   int64
}

func (r *ruleRun) checkClips() {
	for ti := range r.p.Tracks {
		t := &r.p.Tracks[ti]
		var spans []placed
		for ci := range t.Clips {
			c := &t.Clips[ci]
			if r.checkClip(ti, ci, c) {
				spans = append(spans, placed{c: c, ci: ci, start: c.AtMS, end: c.AtMS + r.p.ClipDuration(c)})
			}
		}
		sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
		if t.Kind == KindVideo {
			r.checkOverlaps(ti, t, spans)
		}
		r.checkTransitions(ti, t, spans)
	}
}

// checkClip runs the per-clip rules (L2, L3, L4, L6, L11). It returns true
// if the clip's timeline duration is known (its media exists and its source
// window is valid), so track-level rules can use it.
func (r *ruleRun) checkClip(ti, ci int, c *Clip) bool {
	path := clipPath(ti, ci)

	// L2: media reference exists and has a compatible kind.
	var m *Media
	if c.Media != "" {
		m = r.p.MediaByID(c.Media)
		if m == nil {
			r.fail(path, "clip %q: media %q does not exist", c.ID, c.Media)
			return false
		}
		switch c.Type {
		case ClipVideo:
			if m.Kind != KindVideo {
				r.fail(path, "clip %q: media %q is of kind %q; video clips need media of kind \"video\"", c.ID, m.ID, m.Kind)
				return false
			}
		case ClipAudio:
			if m.Kind == KindImage {
				r.fail(path, "clip %q: media %q is of kind \"image\"; audio clips need media of kind \"audio\" or \"video\"", c.ID, m.ID)
				return false
			}
			if !m.Probe.HasAudio {
				r.fail(path, "clip %q: media %q has no audio", c.ID, m.ID)
				return false
			}
		case ClipImage:
			if m.Kind != KindImage {
				r.fail(path, "clip %q: media %q is of kind %q; image clips need media of kind \"image\"", c.ID, m.ID, m.Kind)
				return false
			}
		}
	}

	// L3: in_ms < out_ms <= media duration.
	if c.HasSourceWindow() {
		dur := m.Probe.DurationMS
		if c.OutMS > dur {
			r.fail(path, "clip %q: out_ms (%d) is past the end of media %q (%dms)", c.ID, c.OutMS, m.ID, dur)
			return false
		}
		if c.OutMS == 0 && c.InMS >= dur {
			r.fail(path, "clip %q: in_ms (%d) must be less than the end of media %q (%dms)", c.ID, c.InMS, m.ID, dur)
			return false
		}
		if c.OutMS != 0 && c.InMS >= c.OutMS {
			r.fail(path, "clip %q: in_ms (%d) must be less than out_ms (%d)", c.ID, c.InMS, c.OutMS)
			return false
		}
	}

	// L6: affect_audio false only on video clips.
	for ei := range c.Effects {
		e := &c.Effects[ei]
		if e.Type == EffectSpeed && !e.AffectsAudio() && c.Type != ClipVideo {
			r.fail(effectPath(ti, ci, ei), "clip %q: speed.affect_audio false is only allowed on video clips", c.ID)
		}
	}

	// L4: fades fit in the clip.
	dur := r.p.ClipDuration(c)
	if dur <= 0 {
		out, _ := r.p.ClipOut(c)
		r.fail(path, "clip %q: duration rounds to 0ms (source window %dms at speed %g)", c.ID, out-c.InMS, c.SpeedFactor())
		return false
	}
	if c.FadeInMS+c.FadeOutMS > dur {
		r.fail(path, "clip %q: fade_in_ms + fade_out_ms (%dms) exceed the clip's duration (%dms)", c.ID, c.FadeInMS+c.FadeOutMS, dur)
	}

	// L11 (warning): sound settings on a clip without sound.
	if c.Type == ClipVideo {
		reason := ""
		switch {
		case !c.IncludesAudio():
			reason = "include_audio is false"
		case !m.Probe.HasAudio:
			reason = fmt.Sprintf("media %q has no audio", m.ID)
		}
		if reason != "" {
			if c.Volume != nil {
				r.warn(path, "clip %q: volume has no effect: the clip has no sound (%s)", c.ID, reason)
			}
			if c.Normalize() != nil {
				r.warn(path, "clip %q: normalize has no effect: the clip has no sound (%s)", c.ID, reason)
			}
		}
	}
	return true
}

// L5: no overlapping clips on video tracks.
func (r *ruleRun) checkOverlaps(ti int, t *Track, spans []placed) {
	var last *placed
	for i := range spans {
		s := &spans[i]
		if last != nil && s.start < last.end {
			r.fail(clipPath(ti, s.ci), "clip %q: overlaps clip %q on video track %q by %dms (%q ends at %dms, %q starts at %dms)",
				s.c.ID, last.c.ID, t.ID, last.end-s.start, last.c.ID, last.end, s.c.ID, s.start)
		}
		if last == nil || s.end > last.end {
			last = s
		}
	}
}

// L14: transition placement and handles (specification section 9.2).
func (r *ruleRun) checkTransitions(ti int, t *Track, spans []placed) {
	next := map[*Clip]*placed{} // outgoing clip -> incoming clip with a transition
	for i := range spans {
		s := &spans[i]
		tr := s.c.TransitionIn
		if tr == nil {
			continue
		}
		path := clipPath(ti, s.ci)
		var prevs, earlier []*placed
		for j := range spans {
			o := &spans[j]
			if o == s {
				continue
			}
			if o.end == s.start {
				prevs = append(prevs, o)
			}
			if o.start < s.start {
				earlier = append(earlier, o)
			}
		}
		if len(prevs) == 0 {
			if len(earlier) == 0 {
				r.fail(path, "clip %q: transition_in on the first clip of track %q (there is no previous clip)", s.c.ID, t.ID)
				continue
			}
			var near *placed
			for _, o := range earlier {
				if o.end <= s.start && (near == nil || o.end > near.end) {
					near = o
				}
			}
			if near == nil { // only overlapping earlier clips (audio tracks)
				near = earlier[len(earlier)-1]
			}
			r.fail(path, "clip %q: transition_in needs the previous clip to end exactly at at_ms (%dms); the nearest previous clip %q ends at %dms (%dms gap)",
				s.c.ID, s.start, near.c.ID, near.end, s.start-near.end)
			continue
		}
		if len(prevs) > 1 {
			ids := make([]string, len(prevs))
			for k, o := range prevs {
				ids[k] = fmt.Sprintf("%q", o.c.ID)
			}
			r.fail(path, "clip %q: transition_in is ambiguous: clips %s all end at at_ms (%dms)", s.c.ID, strings.Join(ids, ", "), s.start)
			continue
		}
		prev := prevs[0]
		next[prev.c] = s

		if s.c.HasSourceWindow() {
			need := int64(math.Ceil(float64(tr.Before()) * s.c.SpeedFactor()))
			if avail := s.c.InMS; need > avail {
				r.fail(path, "clip %q: transition_in needs %dms of source before in_ms (%dms); only %dms available", s.c.ID, need, avail, avail)
			}
		}
		if prev.c.HasSourceWindow() {
			need := int64(math.Ceil(float64(tr.After()) * prev.c.SpeedFactor()))
			m := r.p.MediaByID(prev.c.Media)
			out, _ := r.p.ClipOut(prev.c)
			if avail := m.Probe.DurationMS - out; need > avail {
				r.fail(path, "clip %q: transition_in needs %dms of source after out_ms (%dms) of previous clip %q; only %dms available (media %q is %dms long)",
					s.c.ID, need, out, prev.c.ID, avail, m.ID, m.Probe.DurationMS)
			}
		}
	}
	for i := range spans {
		s := &spans[i]
		n := next[s.c]
		if s.c.TransitionIn == nil || n == nil {
			continue
		}
		head, tail := s.c.TransitionIn.After(), n.c.TransitionIn.Before()
		if dur := s.end - s.start; head+tail > dur {
			r.fail(clipPath(ti, s.ci), "clip %q: transitions overlap: transition_in of %q uses its first %dms and transition_in of %q uses its last %dms, but %q is only %dms long",
				s.c.ID, s.c.ID, head, n.c.ID, tail, s.c.ID, dur)
		}
	}
}

// L7: output path extension suits the preset.
func (r *ruleRun) checkOutput() {
	o := r.p.Output
	if o == nil {
		return
	}
	ok, exts := extensionAllowed(o.Preset, o.Path)
	if ok {
		return
	}
	got := "no extension"
	if ext := strings.ToLower(pathExt(o.Path)); ext != "" {
		got = fmt.Sprintf("extension %q", ext)
	}
	r.fail("/output/path", "output: path %q has %s; preset %q needs %s", o.Path, got, o.Preset, strings.Join(exts, " or "))
}

// L8 and L10 (warnings at load): media files exist and are unchanged.
func (r *ruleRun) checkMediaFiles() {
	for i := range r.p.Media {
		m := &r.p.Media[i]
		info, err := os.Stat(r.p.Resolve(m.Path))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				r.warn(mediaPath(i), "media %q: file not found: %s", m.ID, m.Path)
			} else {
				r.warn(mediaPath(i), "media %q: cannot read file %s: %v", m.ID, m.Path, err)
			}
			continue
		}
		if f := m.Fingerprint; f != nil {
			changed := (f.SizeBytes != nil && *f.SizeBytes != info.Size()) ||
				(f.ModifiedUnixMS != nil && *f.ModifiedUnixMS != info.ModTime().UnixMilli())
			if changed {
				r.warn(mediaPath(i), "media %q: file changed since it was probed; re-import it", m.ID)
			}
		}
	}
}

func pathExt(p string) string {
	if i := strings.LastIndexAny(p, "./"); i >= 0 && p[i] == '.' {
		return p[i:]
	}
	return ""
}
