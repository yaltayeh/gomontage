package project

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sort"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/effects"
	"github.com/ahmedhodiani/gomontage/engine"
	"github.com/ahmedhodiani/gomontage/timeline"
)

// Validate checks p exactly as Load checks a file (schema, then loader rules)
// and returns the load-time warnings. Use it for projects built or edited in
// Go before saving them; Check also runs the render-time checks.
func Validate(p *Project) ([]Warning, error) {
	data, err := Marshal(p)
	if err != nil {
		return nil, err
	}
	_, warnings, err := Parse(data, p.dir)
	return warnings, err
}

// Build turns a project into a timeline.Timeline using the cached probe data
// in the media bin (no file is probed).
//
// Build runs Check first: project errors (schema, loader rules) or, if there
// are none, render blockers (missing media used by enabled clips, missing
// font files or font families, no enabled clips) are returned in a
// *ValidationError, so a project
// is never rendered differently from what the file says. output.fit is not
// checked here (it doesn't affect the timeline); RenderTarget and Check do.
//
// Disabled tracks are skipped. Media paths in the timeline are resolved
// against p.Dir().
func Build(p *Project) (*timeline.Timeline, error) {
	return BuildWith(p, BuildOptions{})
}

// BuildOptions changes what BuildWith puts into the timeline.
type BuildOptions struct {
	// VideoOnly leaves all sound out: audio tracks are skipped and video
	// clips play without their sound. The picture is identical to Build's.
	// Use it for still frames and other video-only previews, so FFmpeg
	// doesn't decode audio that is thrown away.
	VideoOnly bool
}

// BuildWith is Build with options. The checks are the same as Build's (a
// project with render blockers in its audio is still rejected with
// VideoOnly).
func BuildWith(p *Project, opts BuildOptions) (*timeline.Timeline, error) {
	r, q := check(p)
	if len(r.ProjectErrors) > 0 {
		return nil, r.Err()
	}
	var blockers []Problem
	for _, b := range r.RenderBlockers {
		if b.Path != "/output/fit" {
			blockers = append(blockers, b)
		}
	}
	if len(blockers) > 0 {
		return nil, &ValidationError{Problems: blockers}
	}

	tl := timeline.New(timeline.Config{Width: q.Format.Width, Height: q.Format.Height, FPS: q.Format.FPS})
	for ti := range q.Tracks {
		t := &q.Tracks[ti]
		if !t.IsEnabled() || (opts.VideoOnly && t.Kind != KindVideo) {
			continue
		}
		name := t.Name
		if name == "" {
			name = t.ID
		}
		var track interface {
			Add(clip.Clip, time.Duration)
			AddWithTransition(clip.Clip, time.Duration, timeline.Transition)
		}
		if t.Kind == KindVideo {
			track = tl.AddVideoTrack(name)
		} else {
			track = tl.AddAudioTrack(name)
		}
		for _, c := range sortedClips(t.Clips) {
			gc, err := buildClip(q, c)
			if err != nil {
				return nil, err
			}
			if v, ok := gc.(*clip.VideoClip); ok && opts.VideoOnly && v.HasAudio() {
				gc = v.VideoOnly()
			}
			if tr := c.TransitionIn; tr != nil {
				track.AddWithTransition(gc, ms(c.AtMS), timeline.Transition{Type: timeline.TransitionType(tr.Type), Duration: ms(tr.DurationMS)})
				continue
			}
			track.Add(gc, ms(c.AtMS))
		}
	}
	if err := tl.Validate(); err != nil {
		// The loader rules should make this impossible.
		return nil, fmt.Errorf("project: built timeline is invalid: %w", err)
	}
	return tl, nil
}

func ms(v int64) time.Duration { return time.Duration(v) * time.Millisecond }

// sortedClips returns the clips ordered by at_ms (array order for ties), the
// order in which they are added to the timeline.
func sortedClips(clips []Clip) []*Clip {
	out := make([]*Clip, len(clips))
	for i := range clips {
		out[i] = &clips[i]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AtMS < out[j].AtMS })
	return out
}

// mediaInfo rebuilds an engine.MediaInfo from a stored probe.
func mediaInfo(path string, m *Media) *engine.MediaInfo {
	info := &engine.MediaInfo{Path: path, Duration: ms(m.Probe.DurationMS)}
	if m.Probe.HasVideo || m.Kind == KindVideo {
		info.VideoStreams = []engine.VideoStreamInfo{{Width: m.Probe.Width, Height: m.Probe.Height, FPS: m.Probe.FPS}}
	}
	if m.Probe.HasAudio {
		info.AudioStreams = []engine.AudioStreamInfo{{SampleRate: m.Probe.SampleRate, Channels: m.Probe.Channels}}
	}
	return info
}

func buildClip(p *Project, c *Clip) (clip.Clip, error) {
	switch c.Type {
	case ClipVideo, ClipAudio:
		m := p.MediaByID(c.Media)
		path := p.Resolve(m.Path)
		info := mediaInfo(path, m)
		out, _ := p.ClipOut(c)
		whole := c.InMS == 0 && out == m.Probe.DurationMS

		if c.Type == ClipAudio && m.Kind == KindAudio {
			a := clip.NewAudioFromInfo(path, info)
			if !whole {
				a = a.Trim(ms(c.InMS), ms(out))
			}
			if c.Volume != nil {
				a = a.WithVolume(*c.Volume)
			}
			for _, e := range c.Effects {
				a = a.WithEffect(audioEffect(e))
			}
			return withFades(a, c), nil
		}

		v := clip.NewVideoFromInfo(path, info)
		if c.Type == ClipAudio {
			v = v.AudioOnly() // the sound of a video file, on an audio track
		} else if !c.IncludesAudio() {
			v = v.VideoOnly()
		}
		if !whole {
			v = v.Trim(ms(c.InMS), ms(out))
		}
		if c.Volume != nil {
			v = v.WithVolume(*c.Volume)
		}
		for _, e := range c.Effects {
			if c.Type == ClipAudio {
				v = v.WithEffect(audioEffect(e))
			} else {
				v = v.WithEffect(videoEffect(e))
			}
		}
		if l, ok := clipLayout(c); ok {
			v = v.WithLayout(l)
		}
		return withFades(v, c), nil

	case ClipImage:
		m := p.MediaByID(c.Media)
		img := clip.NewImage(p.Resolve(m.Path)).WithDuration(ms(c.DurationMS))
		if l, ok := clipLayout(c); ok {
			img = img.WithLayout(l)
		}
		return withFades(img, c), nil

	case ClipColor:
		col := clip.NewColor(c.Color, 0, 0).WithDuration(ms(c.DurationMS))
		if l, ok := clipLayout(c); ok {
			col = col.WithLayout(l)
		}
		return withFades(col, c), nil

	case ClipText:
		t := clip.NewText(c.Text, textStyle(p, c)).WithDuration(ms(c.DurationMS))
		if l, ok := clipLayout(c); ok {
			t = t.WithLayout(l)
		}
		return withFades(t, c), nil
	}
	return nil, fmt.Errorf("project: clip %q: unknown type %q", c.ID, c.Type)
}

// clipLayout maps a clip's position/anchor/size to a clip.Layout; ok is
// false when all three are at their defaults.
func clipLayout(c *Clip) (clip.Layout, bool) {
	if c.Position == nil && c.Anchor == "" && c.Size == nil {
		return clip.Layout{}, false
	}
	pos := c.PositionValue()
	l := clip.Layout{X: pos.X, Y: pos.Y, Anchor: clip.Anchor(c.AnchorValue())}
	if c.Size != nil {
		l.Width, l.Height, l.Fit = c.Size.Width, c.Size.Height, clip.Fit(c.Size.FitValue())
	}
	return l, true
}

// textStyle maps a text clip's style: sizes given as fractions (of the
// canvas height for the font, of the font size for padding and outline)
// become pixels; alignment defaults to centre as the format specifies.
func textStyle(p *Project, c *Clip) clip.TextStyle {
	st := c.Style
	px := int(math.Round(st.SizeValue() * float64(p.Format.Height)))
	ts := clip.TextStyle{Size: px, Color: st.ColorValue(), Align: DefaultAlign}
	if st == nil {
		return ts
	}
	if st.Font != nil {
		if st.Font.File != "" {
			ts.Font = p.Resolve(st.Font.File)
		}
		ts.FontFamily = st.Font.Family
	}
	if st.Box != nil {
		pad := DefaultBoxPadding
		if st.Box.Padding != nil {
			pad = *st.Box.Padding
		}
		ts.Background = st.Box.Color
		ts.BoxPadding = int(math.Round(pad * float64(px)))
	}
	if st.Outline != nil {
		w := DefaultOutlineW
		if st.Outline.Width != nil {
			w = *st.Outline.Width
		}
		ts.BorderWidth = max(1, int(math.Round(w*float64(px))))
		ts.BorderColor = st.Outline.Color
	}
	if st.Align != "" {
		ts.Align = st.Align
	}
	ts.Direction = st.Direction
	return ts
}

// videoEffect maps an effect on a video clip. speed uses SpeedUp, whose
// sound follows automatically unless affect_audio is false.
func videoEffect(e Effect) effects.Effect {
	if e.Type == EffectSpeed {
		if !e.AffectsAudio() {
			return effects.SpeedUp(e.Factor).KeepAudioSpeed()
		}
		return effects.SpeedUp(e.Factor)
	}
	return effects.NormalizeTo(e.TargetLUFSValue())
}

// audioEffect maps an effect on an audio clip: speed retimes the sound.
func audioEffect(e Effect) effects.Effect {
	if e.Type == EffectSpeed {
		return effects.AudioSpeed(e.Factor)
	}
	return effects.NormalizeTo(e.TargetLUFSValue())
}

// fader is implemented by every clip type's WithFadeIn/WithFadeOut pair.
type fader[T any] interface {
	clip.Clip
	WithFadeIn(time.Duration) T
	WithFadeOut(time.Duration) T
}

func withFades[T fader[T]](c T, pc *Clip) clip.Clip {
	if pc.FadeInMS > 0 {
		c = c.WithFadeIn(ms(pc.FadeInMS))
	}
	if pc.FadeOutMS > 0 {
		c = c.WithFadeOut(ms(pc.FadeOutMS))
	}
	return c
}

// renderProblems runs the render-time checks L8, L9 and L12 on a validated
// project (L13 has no checks left).
func renderProblems(p *Project) []Problem {
	r := &ruleRun{p: p}
	usedMedia := map[string]bool{}
	enabledClips := 0
	for ti := range p.Tracks {
		t := &p.Tracks[ti]
		if !t.IsEnabled() {
			continue
		}
		for ci := range t.Clips {
			c := &t.Clips[ci]
			enabledClips++
			if c.Media != "" {
				usedMedia[c.Media] = true
			}
			r.checkRenderableClip(ti, ci, c)
		}
	}
	// L12.
	if enabledClips == 0 {
		r.fail("", "project has no enabled clips to render")
	}
	// L8: media files used by enabled clips must exist.
	var missing []Problem
	for i := range p.Media {
		m := &p.Media[i]
		if !usedMedia[m.ID] {
			continue
		}
		if _, err := os.Stat(p.Resolve(m.Path)); err != nil {
			msg := fmt.Sprintf("media %q: file not found: %s", m.ID, m.Path)
			if !errors.Is(err, fs.ErrNotExist) {
				msg = fmt.Sprintf("media %q: cannot read file %s: %v", m.ID, m.Path, err)
			}
			missing = append(missing, Problem{Path: mediaPath(i), Message: msg})
		}
	}
	return append(missing, r.problems...)
}

// checkRenderableClip runs L9 (fonts). (L13, features the library can't
// render yet, has no checks left: the format has no known gaps.)
func (r *ruleRun) checkRenderableClip(ti, ci int, c *Clip) {
	path := clipPath(ti, ci)
	if c.Type != ClipText || c.Style == nil || c.Style.Font == nil {
		return
	}
	if f := c.Style.Font.File; f != "" {
		if _, err := os.Stat(r.p.Resolve(f)); err != nil {
			r.fail(path+"/style/font/file", "clip %q: font file not found: %s", c.ID, f)
		}
	}
	if fam := c.Style.Font.Family; fam != "" {
		switch ok, err := fontFamilyInstalled(fam); {
		case err != nil:
			r.fail(path+"/style/font/family", "clip %q: cannot check font family %q: %v (use style.font.file instead)", c.ID, fam, err)
		case !ok:
			r.fail(path+"/style/font/family", "clip %q: font family %q is not installed (fontconfig can't find it)", c.ID, fam)
		}
	}
}
