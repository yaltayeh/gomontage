package project

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sort"
	"strings"
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
// font files, no enabled clips, features the library can't render yet —
// specification section 14) are returned in a *ValidationError, so a project
// is never rendered differently from what the file says. output.fit is not
// checked here (it doesn't affect the timeline); RenderTarget and Check do.
//
// Disabled tracks are skipped. Media paths in the timeline are resolved
// against p.Dir().
func Build(p *Project) (*timeline.Timeline, error) {
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
		if !t.IsEnabled() {
			continue
		}
		name := t.Name
		if name == "" {
			name = t.ID
		}
		var add func(clip.Clip, time.Duration)
		if t.Kind == KindVideo {
			add = tl.AddVideoTrack(name).Add
		} else {
			add = tl.AddAudioTrack(name).Add
		}
		for _, c := range sortedClips(t.Clips) {
			gc, err := buildClip(q, c)
			if err != nil {
				return nil, err
			}
			add(gc, ms(c.AtMS))
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
		return withFades(v, c), nil

	case ClipImage:
		m := p.MediaByID(c.Media)
		return withFades(clip.NewImage(p.Resolve(m.Path)).WithDuration(ms(c.DurationMS)), c), nil

	case ClipColor:
		return withFades(clip.NewColor(c.Color, 0, 0).WithDuration(ms(c.DurationMS)), c), nil

	case ClipText:
		style := clip.TextStyle{
			Size:  int(math.Round(c.Style.SizeValue() * float64(p.Format.Height))),
			Color: c.Style.ColorValue(),
		}
		if c.Style != nil && c.Style.Font != nil && c.Style.Font.File != "" {
			style.Font = p.Resolve(c.Style.Font.File)
		}
		return withFades(clip.NewText(c.Text, style).WithDuration(ms(c.DurationMS)), c), nil
	}
	return nil, fmt.Errorf("project: clip %q: unknown type %q", c.ID, c.Type)
}

// videoEffect maps an effect on a video clip. speed uses SpeedUp, whose
// sound follows automatically (affect_audio false is rejected by L13).
func videoEffect(e Effect) effects.Effect {
	if e.Type == EffectSpeed {
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

// renderProblems runs the render-time checks L8, L9, L12 and L13 on a
// validated project.
func renderProblems(p *Project) []Problem {
	r := &ruleRun{p: p}
	usedMedia := map[string]bool{}
	enabledClips := 0
	videoTracks := 0
	for ti := range p.Tracks {
		t := &p.Tracks[ti]
		if !t.IsEnabled() {
			continue
		}
		if t.Kind == KindVideo {
			videoTracks++
			// L13: overlay tracks (known bug #1).
			if videoTracks > 1 && len(t.Clips) > 0 {
				r.fail(trackPath(ti), "track %q: overlay tracks are not supported yet (known bug #1)", t.ID)
			}
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

// checkRenderableClip runs L9 and the clip-level L13 checks.
func (r *ruleRun) checkRenderableClip(ti, ci int, c *Clip) {
	path := clipPath(ti, ci)
	if c.TransitionIn != nil {
		r.fail(path+"/transition_in", "clip %q: transition_in is not supported yet", c.ID)
	}
	for ei := range c.Effects {
		if e := &c.Effects[ei]; e.Type == EffectSpeed && !e.AffectsAudio() {
			r.fail(effectPath(ti, ci, ei)+"/affect_audio", "clip %q: speed.affect_audio false is not supported yet", c.ID)
		}
	}
	if c.Type == ClipColor && len(c.Color) == 9 && !strings.EqualFold(c.Color[7:], "FF") {
		r.fail(path+"/color", "clip %q: color %q has transparency, which is not supported yet", c.ID, c.Color)
	}
	if c.Type == ClipText {
		// drawtext is given no x/y, so text always lands in the top-left
		// corner, while the format's default is the centre (known bug #2).
		r.fail(path, "clip %q: text clips are not supported yet: text is always drawn at the top-left corner (known bug #2)", c.ID)
		if st := c.Style; st != nil {
			if st.Font != nil && st.Font.Family != "" {
				r.fail(path+"/style/font/family", "clip %q: style.font.family is not supported yet (use style.font.file)", c.ID)
			}
			if st.Font != nil && st.Font.File != "" {
				if _, err := os.Stat(r.p.Resolve(st.Font.File)); err != nil {
					r.fail(path+"/style/font/file", "clip %q: font file not found: %s", c.ID, st.Font.File) // L9
				}
			}
			if st.Box != nil {
				r.fail(path+"/style/box", "clip %q: style.box is not supported yet", c.ID)
			}
			if st.Outline != nil {
				r.fail(path+"/style/outline", "clip %q: style.outline is not supported yet", c.ID)
			}
			if st.Align != "" {
				r.fail(path+"/style/align", "clip %q: style.align %q is not supported yet", c.ID, st.Align)
			}
			if st.Direction != "" {
				r.fail(path+"/style/direction", "clip %q: style.direction %q is not supported yet", c.ID, st.Direction)
			}
		}
		return
	}
	// Layout with non-default values (known bug #2). Canonicalization has
	// already cleared default values.
	var fields []string
	if c.Position != nil {
		fields = append(fields, "position")
	}
	if c.Anchor != "" {
		fields = append(fields, "anchor")
	}
	if c.Size != nil {
		fields = append(fields, "size")
	}
	if len(fields) > 0 {
		r.fail(path, "clip %q: %s not supported yet (known bug #2)", c.ID, joinFields(fields))
	}
}

func joinFields(f []string) string {
	if len(f) == 1 {
		return f[0] + " is"
	}
	return strings.Join(f[:len(f)-1], ", ") + " and " + f[len(f)-1] + " are"
}
