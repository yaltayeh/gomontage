package timeline

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/effects"
	"github.com/ahmedhodiani/gomontage/engine"
)

// TransitionType names how a transition blends two clips.
type TransitionType string

// Transition types.
const (
	// TransitionCrossfade dissolves the pictures into each other and
	// cross-fades the sounds.
	TransitionCrossfade TransitionType = "crossfade"
)

// Transition joins a clip to the clip before it on the same track (see
// VideoTrack.AddWithTransition). It uses the handle model of the project
// format (docs/project-format.md §9): the clips stay butt-joined and the
// transition is centred on the cut, made from extra source footage beyond
// the clips' trim points, so it never changes any start time, clip duration
// or the timeline duration.
type Transition struct {
	Type     TransitionType
	Duration time.Duration
}

// Crossfade returns a crossfade transition of length d.
func Crossfade(d time.Duration) Transition {
	return Transition{Type: TransitionCrossfade, Duration: d}
}

// Before is the part of the transition before the cut: d/2 rounded down to
// a whole millisecond. The incoming clip shows this much footage from before
// its trim start.
func (t Transition) Before() time.Duration {
	return (t.Duration / 2).Truncate(time.Millisecond)
}

// After is the part of the transition after the cut (Duration - Before).
// The outgoing clip shows this much footage from after its trim end.
func (t Transition) After() time.Duration {
	return t.Duration - t.Before()
}

// AddWithTransition places c at startAt, joined to the clip that ends
// exactly at startAt by tr. Timeline.Validate checks that there is exactly
// one such clip and that both clips have enough source for the handles.
//
// Example:
//
//	track.Add(a, 0)
//	track.AddWithTransition(b, a.Duration(), timeline.Crossfade(time.Second))
func (t *VideoTrack) AddWithTransition(c clip.Clip, startAt time.Duration, tr Transition) {
	t.entries = append(t.entries, Placement{StartAt: startAt, Clip: c, TransitionIn: &tr})
}

// AddWithTransition is VideoTrack.AddWithTransition for audio clips.
func (t *AudioTrack) AddWithTransition(c clip.Clip, startAt time.Duration, tr Transition) {
	t.entries = append(t.entries, Placement{StartAt: startAt, Clip: c, TransitionIn: &tr})
}

// clipEdge records the transitions at a clip's two ends.
type clipEdge struct {
	in  *Transition // from the previous clip; the clip starts in.Before() early
	out *Transition // into the next clip; the clip ends out.After() late
}

// head is the extra time shown before the clip's start (timeline time).
func (e clipEdge) head() time.Duration {
	if e.in == nil {
		return 0
	}
	return e.in.Before()
}

// tail is the extra time shown after the clip's end (timeline time).
func (e clipEdge) tail() time.Duration {
	if e.out == nil {
		return 0
	}
	return e.out.After()
}

// extended reports whether the clip has a transition at either end; its
// streams then include the handles and have an exact length.
func (e clipEdge) extended() bool { return e.in != nil || e.out != nil }

// trackEdges returns the edges of every entry (indexed like entries). It
// assumes validateTransitions passed.
func trackEdges(entries []Placement) []clipEdge {
	edges := make([]clipEdge, len(entries))
	for i, e := range entries {
		if e.TransitionIn == nil {
			continue
		}
		edges[i].in = e.TransitionIn
		for j, p := range entries {
			if j != i && p.StartAt+p.Clip.Duration() == e.StartAt {
				edges[j].out = e.TransitionIn
				break
			}
		}
	}
	return edges
}

// hasTransitions reports whether any entry has a transition.
func hasTransitions(entries []Placement) bool {
	for _, e := range entries {
		if e.TransitionIn != nil {
			return true
		}
	}
	return false
}

// hasSourceWindow reports whether c plays a window of a media file (video
// or audio), so its handles are limited by the source. Images, text and
// colors can be shown longer without limit.
func hasSourceWindow(c clip.Clip) bool {
	t := c.ClipType()
	return c.SourcePath() != "" && (t == clip.TypeVideo || t == clip.TypeAudio)
}

// sourceRate is how fast c consumes its source per second of timeline time
// (its speed factor: source window length / clip duration).
func sourceRate(c clip.Clip) float64 {
	if c.Duration() <= 0 {
		return 1
	}
	return float64(c.TrimEnd()-c.TrimStart()) / float64(c.Duration())
}

// sourceNeed is the source time needed for d of timeline time at rate,
// rounded up to a whole millisecond (as the project format specifies).
func sourceNeed(d time.Duration, rate float64) time.Duration {
	ms := float64(d) * rate / float64(time.Millisecond)
	return time.Duration(math.Ceil(ms-1e-6)) * time.Millisecond
}

// validateTransitions checks every transition on a track: a known type and
// positive length, exactly one clip ending at the incoming clip's start,
// enough source before the incoming clip's trim start and after the outgoing
// clip's trim end, and no clip whose two transitions overlap.
func validateTransitions(trackName string, entries []Placement) error {
	at := func(e Placement) string {
		return fmt.Sprintf("track %q: clip at %.3fs", trackName, e.StartAt.Seconds())
	}
	for i, e := range entries {
		tr := e.TransitionIn
		if tr == nil {
			continue
		}
		if tr.Type != TransitionCrossfade {
			return fmt.Errorf("%s: unknown transition type %q (supported: %q)", at(e), tr.Type, TransitionCrossfade)
		}
		if tr.Duration <= 0 {
			return fmt.Errorf("%s: transition duration must be positive, got %s", at(e), tr.Duration)
		}
		var prevs []Placement
		var nearest *Placement
		for j := range entries {
			p := entries[j]
			end := p.StartAt + p.Clip.Duration()
			if j == i {
				continue
			}
			if end == e.StartAt {
				prevs = append(prevs, p)
			}
			if end <= e.StartAt && (nearest == nil || end > nearest.StartAt+nearest.Clip.Duration()) {
				nearest = &entries[j]
			}
		}
		switch {
		case len(prevs) == 0 && nearest == nil:
			return fmt.Errorf("%s: transition on the first clip of the track (no clip before it)", at(e))
		case len(prevs) == 0:
			end := nearest.StartAt + nearest.Clip.Duration()
			return fmt.Errorf("%s: transition needs the previous clip to end exactly at %.3fs; the nearest previous clip ends at %.3fs (%.3fs gap)",
				at(e), e.StartAt.Seconds(), end.Seconds(), (e.StartAt - end).Seconds())
		case len(prevs) > 1:
			return fmt.Errorf("%s: transition is ambiguous: %d clips end at %.3fs", at(e), len(prevs), e.StartAt.Seconds())
		}
		prev := prevs[0]
		if hasSourceWindow(e.Clip) {
			need := sourceNeed(tr.Before(), sourceRate(e.Clip))
			if avail := e.Clip.TrimStart(); need > avail {
				return fmt.Errorf("%s: transition needs %.3fs of source before the clip's trim start (%.3fs); only %.3fs available",
					at(e), need.Seconds(), avail.Seconds(), avail.Seconds())
			}
		}
		if src := clip.SourceDuration(prev.Clip); hasSourceWindow(prev.Clip) && src > 0 {
			need := sourceNeed(tr.After(), sourceRate(prev.Clip))
			if avail := src - prev.Clip.TrimEnd(); need > avail {
				return fmt.Errorf("%s: transition needs %.3fs of source after the trim end (%.3fs) of the previous clip; only %.3fs available (%q is %.3fs long)",
					at(e), need.Seconds(), prev.Clip.TrimEnd().Seconds(), avail.Seconds(), prev.Clip.SourcePath(), src.Seconds())
			}
		}
	}
	edges := trackEdges(entries)
	for i, e := range entries {
		if edges[i].in != nil && edges[i].out != nil {
			if used := edges[i].in.After() + edges[i].out.Before(); used > e.Clip.Duration() {
				return fmt.Errorf("%s: transitions overlap: its transition in uses its first %.3fs and the next clip's transition uses its last %.3fs, but the clip is only %.3fs long",
					at(e), edges[i].in.After().Seconds(), edges[i].out.Before().Seconds(), e.Clip.Duration().Seconds())
			}
		}
	}
	return nil
}

// pictureWindow is the source window [start, end) a file clip's picture
// reads with its handles: the handles are scaled by the clip's speed.
func pictureWindow(c clip.Clip, e clipEdge) (time.Duration, time.Duration) {
	rate := sourceRate(c)
	start := c.TrimStart() - time.Duration(float64(e.head())*rate)
	end := c.TrimEnd() + time.Duration(float64(e.tail())*rate)
	return max(start, 0), end
}

// soundRate is how fast a clip's sound consumes its source per second of
// timeline time: 1 for SpeedEffect.KeepAudioSpeed, the speed factor when the
// sound follows the picture's speed, and the AudioSpeed factor otherwise.
func soundRate(c clip.Clip) float64 {
	if !c.HasVideo() {
		return sourceRate(c) // the sound is the clip: AudioSpeed sets its length
	}
	if keepsAudioSpeed(c) {
		return 1
	}
	if f := implicitAudioTempo(c); f != 1 {
		return f
	}
	rate := 1.0
	for _, e := range c.Effects() {
		if a, ok := e.(*effects.AudioSpeedEffect); ok && a.DurationFactor() > 0 {
			rate /= a.DurationFactor()
		}
	}
	return rate
}

// soundWindow is the source window [start, end) a clip's sound reads with
// its handles, and lead, the silence (timeline time) needed in front when
// the source has less than the head handle before the trim start.
//
// Handles are scaled by soundRate. With SpeedEffect.KeepAudioSpeed the
// sound plays the source from the trim start at normal speed, so its tail
// handle continues from where the clip's sound stops, and never past the
// trim end (a slowed-down clip's sound has already ended: its tail handle
// is silence). The end is limited to the source length when it is known;
// the missing part becomes silence.
func soundWindow(c clip.Clip, e clipEdge) (start, end, lead time.Duration) {
	rate := soundRate(c)
	start = c.TrimStart() - time.Duration(float64(e.head())*rate)
	if start < 0 {
		lead = time.Duration(float64(-start) / rate)
		start = 0
	}
	if keepsAudioSpeed(c) {
		end = min(c.TrimEnd(), c.TrimStart()+c.Duration()+e.tail())
	} else {
		end = c.TrimEnd() + time.Duration(float64(e.tail())*rate)
	}
	if src := clip.SourceDuration(c); src > 0 && end > src {
		end = src
	}
	return start, end, lead
}

// formatPrecise formats a duration as seconds with microsecond precision,
// for handle trims whose source times are scaled by a speed factor.
func formatPrecise(d time.Duration) string {
	return formatFloat(d.Seconds())
}

// joinWithTransitions joins the sorted, gap-filled clips of the base track.
// Runs of clips joined by plain cuts are concatenated exactly as without
// transitions; consecutive runs are joined at their transition with xfade
// (picture) and acrossfade (sound). Each run's first clip starts with its
// head handle and its last clip ends with its tail handle, so at a cut T
// with transition d the left side lasts until T+After and the right side
// starts at T-Before: xfade at offset T-Before for d, and acrossfade over
// the last d of the left sound, give a stream exactly as long as the clips
// themselves, with the sound in sync.
func (c *Compiler) joinWithTransitions(labels []clipLabel, cfg Config) (string, string) {
	hasAudio := anyAudio(labels)
	var groups [][]clipLabel
	for _, l := range labels {
		if l.edge.in != nil || len(groups) == 0 {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], l)
	}

	var video, audio string
	for gi, g := range groups {
		v, a := g[0].video, g[0].audio
		if len(g) > 1 {
			v, a = c.concatSegments(g, hasAudio)
		} else if hasAudio && a == "" {
			a = c.silence(g[0].length())
		}
		if gi == 0 {
			video, audio = v, a
			continue
		}
		tr := g[0].edge.in
		offset := g[0].entry.StartAt - tr.Before()
		left := c.then(c.streamOf(video), "settb", "xtb", map[string]string{"expr": "AVTB"}, engine.StreamVideo)
		right := c.then(c.streamOf(v), "settb", "xtb", map[string]string{"expr": "AVTB"}, engine.StreamVideo)
		xf := c.graph.AddFilter("xfade", map[string]string{
			"transition": xfadeName(tr.Type),
			"duration":   formatSeconds(tr.Duration),
			"offset":     formatSeconds(offset),
		})
		video = c.nextLabelFor("xfade", xf)
		c.graph.Connect(left.node, xf, left.label, engine.StreamVideo)
		c.graph.Connect(right.node, xf, right.label, engine.StreamVideo)
		if hasAudio {
			audio = c.crossfadeAudio(audio, a, tr)
		}
	}
	return video, audio
}

// xfadeName maps a transition type to FFmpeg's xfade transition.
func xfadeName(t TransitionType) string {
	switch t {
	case TransitionCrossfade:
		return "fade"
	}
	return string(t)
}

// crossfadeAudio cross-fades the last tr.Duration of left with the first
// tr.Duration of right (linear gain curves, like the picture's dissolve).
func (c *Compiler) crossfadeAudio(left, right string, tr *Transition) string {
	n := c.graph.AddFilter("acrossfade", map[string]string{
		"d":  formatSeconds(tr.Duration),
		"c1": "tri",
		"c2": "tri",
	})
	out := c.nextLabelFor("axfade", n)
	c.graph.Connect(c.findFilterByLabel(left), n, left, engine.StreamAudio)
	c.graph.Connect(c.findFilterByLabel(right), n, right, engine.StreamAudio)
	return out
}

// silence returns a silent stereo stream of length d.
func (c *Compiler) silence(d time.Duration) string {
	n := c.graph.AddFilter("anullsrc", map[string]string{"r": "48000", "cl": "stereo", "d": formatSeconds(d)})
	return c.nextLabelFor("asilence", n)
}

// audioPiece is the sound of a clip with transitions, not yet shifted to
// its start (it begins edge.head() before entry.StartAt). label is "" for a
// clip without sound.
type audioPiece struct {
	label string
	entry Placement
	edge  clipEdge
}

// chainAudio joins sounds linked by transitions into chains with
// acrossfade (a clip without sound counts as silence) and shifts each chain
// to its first clip's start. It returns one label per chain that has sound.
func (c *Compiler) chainAudio(pieces []audioPiece) []string {
	sort.SliceStable(pieces, func(i, j int) bool { return pieces[i].entry.StartAt < pieces[j].entry.StartAt })
	next := make([]int, len(pieces))
	for i := range next {
		next[i] = -1
	}
	for k, p := range pieces {
		if p.edge.in == nil {
			continue
		}
		for j, q := range pieces {
			if j != k && q.edge.out != nil && q.entry.StartAt+q.entry.Clip.Duration() == p.entry.StartAt {
				next[j] = k
				break
			}
		}
	}
	var out []string
	for h, p := range pieces {
		if p.edge.in != nil {
			continue // not the first clip of a chain
		}
		var chain []audioPiece
		sound := false
		for k := h; k >= 0; k = next[k] {
			chain = append(chain, pieces[k])
			sound = sound || pieces[k].label != ""
		}
		if !sound {
			continue
		}
		label := ""
		for _, m := range chain {
			l := m.label
			if l == "" {
				l = c.silence(m.edge.head() + m.entry.Clip.Duration() + m.edge.tail())
			}
			if label == "" {
				label = l
				continue
			}
			label = c.crossfadeAudio(label, l, m.edge.in)
		}
		out = append(out, c.delayAudio(label, p.entry.StartAt))
	}
	return out
}

// textAlpha is textFadeAlpha for a text clip with transitions: it also
// fades the text in over its transition in (starting edge.head() before
// start) and out over its transition out (ending edge.tail() after end).
// The clip's own fades apply to its own span only.
func textAlpha(start, end, fadeIn, fadeOut time.Duration, edge clipEdge) string {
	if !edge.extended() {
		return textFadeAlpha(start, end, fadeIn, fadeOut)
	}
	var terms []string
	if fadeIn > 0 {
		terms = append(terms, fmt.Sprintf("if(lt(t,%s),1,(t-%s)/%s)", formatSeconds(start), formatSeconds(start), formatSeconds(fadeIn)))
	}
	if fadeOut > 0 {
		terms = append(terms, fmt.Sprintf("if(gte(t,%s),1,(%s-t)/%s)", formatSeconds(end), formatSeconds(end), formatSeconds(fadeOut)))
	}
	if tr := edge.in; tr != nil {
		terms = append(terms, fmt.Sprintf("(t-%s)/%s", formatSeconds(start-tr.Before()), formatSeconds(tr.Duration)))
	}
	if tr := edge.out; tr != nil {
		terms = append(terms, fmt.Sprintf("(%s-t)/%s", formatSeconds(end+tr.After()), formatSeconds(tr.Duration)))
	}
	expr := terms[0]
	for _, t := range terms[1:] {
		expr = fmt.Sprintf("min(%s,%s)", expr, t)
	}
	return fmt.Sprintf("clip(%s,0,1)", expr)
}
