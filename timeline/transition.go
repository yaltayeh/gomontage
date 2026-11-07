package timeline

import (
	"fmt"
	"math"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
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
