package project

import (
	"math"
	"path/filepath"
	"strings"
)

// FormatVersion is the project file format version this package reads and
// writes.
const FormatVersion = 1

// FileExtension is the recommended file name suffix for project files.
const FileExtension = ".gomontage.json"

// Media kinds.
const (
	KindVideo = "video"
	KindAudio = "audio"
	KindImage = "image"
)

// Clip types.
const (
	ClipVideo = "video"
	ClipAudio = "audio"
	ClipImage = "image"
	ClipText  = "text"
	ClipColor = "color"
)

// Effect types.
const (
	EffectSpeed     = "speed"
	EffectNormalize = "normalize"
)

// Transition types.
const TransitionCrossfade = "crossfade"

// Fit modes (Size.Fit and Output.Fit).
const (
	FitContain = "contain"
	FitCover   = "cover"
	FitStretch = "stretch"
)

// Defaults from the specification (docs/project-format.md). In the Go types
// the zero value of a field always means "the default"; these constants give
// the resolved values.
const (
	DefaultVolume     = 1.0
	DefaultTargetLUFS = -16.0
	DefaultTextSize   = 0.05
	DefaultTextColor  = "#FFFFFF"
	DefaultAnchor     = "center"
	DefaultAlign      = "center"
	DefaultDirection  = "auto"
	DefaultBoxPadding = 0.25
	DefaultOutlineW   = 0.05
)

// DefaultPosition is the default clip position (canvas centre).
var DefaultPosition = Position{X: 0.5, Y: 0.5}

// Project is a GoMontage project file (format version 1). See
// docs/project-format.md for the meaning of every field.
//
// Throughout these types, a field's zero value means the specification's
// default (e.g. a nil Clip.Volume means 1, a nil Track.Enabled means true).
// Use the accessor methods (Clip.VolumeValue, Track.IsEnabled, ...) to read
// resolved values. Load canonicalizes explicitly written defaults to their
// zero value, and Save omits zero values, so files round-trip exactly.
type Project struct {
	Schema  string         `json:"$schema,omitempty"`
	Version int            `json:"version"`
	Name    string         `json:"name,omitempty"`
	Format  Format         `json:"format"`
	Media   []Media        `json:"media"`
	Tracks  []Track        `json:"tracks"`
	Output  *Output        `json:"output,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`

	// dir is the directory relative paths are resolved against: the
	// directory of the file the project was loaded from (or given to New).
	dir string
}

// Format is the composition canvas.
type Format struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    float64 `json:"fps"`
}

// Media is one entry in the media bin.
type Media struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	Path        string       `json:"path"`
	Probe       Probe        `json:"probe"`
	Fingerprint *Fingerprint `json:"fingerprint,omitempty"`
}

// Probe holds the stored ffprobe results of a media file. Which fields are
// meaningful depends on the media kind (see the specification, section 4).
type Probe struct {
	DurationMS int64   `json:"duration_ms"`
	HasVideo   bool    `json:"has_video"`
	HasAudio   bool    `json:"has_audio"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	SampleRate int     `json:"sample_rate"`
	Channels   int     `json:"channels"`
}

// Fingerprint records a media file's size and modification time at probe
// time, to detect files that changed since.
type Fingerprint struct {
	SizeBytes      *int64 `json:"size_bytes,omitempty"`
	ModifiedUnixMS *int64 `json:"modified_unix_ms,omitempty"`
}

// Track is a video or audio track.
type Track struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"` // nil = true
	Clips   []Clip `json:"clips"`
}

// IsEnabled reports whether the track is rendered.
func (t *Track) IsEnabled() bool { return t.Enabled == nil || *t.Enabled }

// Clip is one clip on a track. Type selects which fields apply.
type Clip struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`

	// video, audio, image clips
	Media string `json:"media,omitempty"`
	// text clips
	Text string `json:"text,omitempty"`
	// color clips
	Color string `json:"color,omitempty"`

	AtMS int64 `json:"at_ms"`
	// video, audio: source window. OutMS 0 = the end of the source.
	InMS  int64 `json:"in_ms,omitempty"`
	OutMS int64 `json:"out_ms,omitempty"`
	// image, text, color
	DurationMS int64 `json:"duration_ms,omitempty"`

	IncludeAudio *bool    `json:"include_audio,omitempty"` // video; nil = true
	Volume       *float64 `json:"volume,omitempty"`        // video, audio; nil = 1
	FadeInMS     int64    `json:"fade_in_ms,omitempty"`
	FadeOutMS    int64    `json:"fade_out_ms,omitempty"`

	TransitionIn *Transition `json:"transition_in,omitempty"`
	Effects      []Effect    `json:"effects,omitempty"` // video, audio

	Style *TextStyle `json:"style,omitempty"` // text

	// Layout: video, image, color, text (text has no Size).
	Position *Position `json:"position,omitempty"` // nil = centre
	Anchor   string    `json:"anchor,omitempty"`   // "" = "center"
	Size     *Size     `json:"size,omitempty"`     // nil = whole canvas, contain
}

// IncludesAudio reports whether a video clip plays its media's sound
// (include_audio, default true). It does not check that the media has sound.
func (c *Clip) IncludesAudio() bool { return c.IncludeAudio == nil || *c.IncludeAudio }

// VolumeValue returns the clip's volume (default 1).
func (c *Clip) VolumeValue() float64 {
	if c.Volume == nil {
		return DefaultVolume
	}
	return *c.Volume
}

// PositionValue returns the clip's position (default the canvas centre).
func (c *Clip) PositionValue() Position {
	if c.Position == nil {
		return DefaultPosition
	}
	return *c.Position
}

// AnchorValue returns the clip's anchor (default "center").
func (c *Clip) AnchorValue() string {
	if c.Anchor == "" {
		return DefaultAnchor
	}
	return c.Anchor
}

// Speed returns the clip's speed effect, or nil.
func (c *Clip) Speed() *Effect {
	for i := range c.Effects {
		if c.Effects[i].Type == EffectSpeed {
			return &c.Effects[i]
		}
	}
	return nil
}

// SpeedFactor returns the speed effect's factor, or 1.
func (c *Clip) SpeedFactor() float64 {
	if s := c.Speed(); s != nil {
		return s.Factor
	}
	return 1
}

// Normalize returns the clip's normalize effect, or nil.
func (c *Clip) Normalize() *Effect {
	for i := range c.Effects {
		if c.Effects[i].Type == EffectNormalize {
			return &c.Effects[i]
		}
	}
	return nil
}

// HasSourceWindow reports whether the clip plays a window of a media file
// (video and audio clips).
func (c *Clip) HasSourceWindow() bool { return c.Type == ClipVideo || c.Type == ClipAudio }

// Transition is a clip's transition_in.
type Transition struct {
	Type       string `json:"type"`
	DurationMS int64  `json:"duration_ms"`
}

// Before is the part of the transition before the cut: floor(d/2) ms.
func (t *Transition) Before() int64 { return t.DurationMS / 2 }

// After is the part of the transition after the cut: ceil(d/2) ms.
func (t *Transition) After() int64 { return t.DurationMS - t.Before() }

// Effect is one entry of a clip's effects list. Type selects which fields
// apply: speed uses Factor and AffectAudio; normalize uses TargetLUFS.
type Effect struct {
	Type        string   `json:"type"`
	Factor      float64  `json:"factor,omitempty"`
	AffectAudio *bool    `json:"affect_audio,omitempty"` // nil = true
	TargetLUFS  *float64 `json:"target_lufs,omitempty"`  // nil = -16
}

// AffectsAudio reports whether a speed effect retimes the sound (default true).
func (e *Effect) AffectsAudio() bool { return e.AffectAudio == nil || *e.AffectAudio }

// TargetLUFSValue returns a normalize effect's target (default -16).
func (e *Effect) TargetLUFSValue() float64 {
	if e.TargetLUFS == nil {
		return DefaultTargetLUFS
	}
	return *e.TargetLUFS
}

// Position is a point on the canvas in fractions of its width and height.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Size is a clip size in fractions of the canvas. Width or Height may be 0
// (not given). Fit only applies when both are given; "" = "contain".
type Size struct {
	Width  float64 `json:"width,omitempty"`
	Height float64 `json:"height,omitempty"`
	Fit    string  `json:"fit,omitempty"`
}

// FitValue returns the fit mode (default "contain").
func (s *Size) FitValue() string {
	if s.Fit == "" {
		return FitContain
	}
	return s.Fit
}

// TextStyle is a text clip's style. Every field is optional.
type TextStyle struct {
	Font      *Font        `json:"font,omitempty"`
	Size      *float64     `json:"size,omitempty"`  // nil = 0.05
	Color     string       `json:"color,omitempty"` // "" = "#FFFFFF"
	Box       *TextBox     `json:"box,omitempty"`
	Outline   *TextOutline `json:"outline,omitempty"`
	Align     string       `json:"align,omitempty"`     // "" = "center"
	Direction string       `json:"direction,omitempty"` // "" = "auto"
}

// SizeValue returns the font size as a fraction of canvas height.
func (s *TextStyle) SizeValue() float64 {
	if s == nil || s.Size == nil {
		return DefaultTextSize
	}
	return *s.Size
}

// ColorValue returns the text color (default "#FFFFFF").
func (s *TextStyle) ColorValue() string {
	if s == nil || s.Color == "" {
		return DefaultTextColor
	}
	return s.Color
}

// Font selects a font by installed family name or by file. Exactly one of
// the two is set.
type Font struct {
	Family string `json:"family,omitempty"`
	File   string `json:"file,omitempty"`
}

// TextBox is a background box behind text.
type TextBox struct {
	Color   string   `json:"color"`
	Padding *float64 `json:"padding,omitempty"` // nil = 0.25
}

// TextOutline is an outline around letters.
type TextOutline struct {
	Color string   `json:"color"`
	Width *float64 `json:"width,omitempty"` // nil = 0.05
}

// Output holds the default render settings.
type Output struct {
	Preset string `json:"preset"`
	Path   string `json:"path"`
	Fit    string `json:"fit,omitempty"` // "" = "contain"
}

// FitValue returns the output fit mode (default "contain").
func (o *Output) FitValue() string {
	if o.Fit == "" {
		return FitContain
	}
	return o.Fit
}

// New returns an empty project with the given canvas. Relative paths (media,
// fonts, output) are resolved against dir; use the directory the project file
// will be saved in.
func New(dir string, format Format) *Project {
	return &Project{Version: FormatVersion, Format: format, Media: []Media{}, Tracks: []Track{}, dir: dir}
}

// Dir returns the directory relative paths are resolved against.
func (p *Project) Dir() string { return p.dir }

// Resolve turns a project-relative path (with '/' separators) into a path
// usable on this system, relative to Dir.
func (p *Project) Resolve(rel string) string {
	return filepath.Join(p.dir, filepath.FromSlash(rel))
}

// MediaByID returns the media entry with the given ID, or nil.
func (p *Project) MediaByID(id string) *Media {
	for i := range p.Media {
		if p.Media[i].ID == id {
			return &p.Media[i]
		}
	}
	return nil
}

// ClipDuration returns the clip's duration on the timeline in milliseconds:
// round((out - in) / speed factor) for video and audio clips (out defaults
// to the media's duration), duration_ms otherwise. Rounding is half away from
// zero. It returns 0 for a video/audio clip whose media doesn't exist.
func (p *Project) ClipDuration(c *Clip) int64 {
	if !c.HasSourceWindow() {
		return c.DurationMS
	}
	out, ok := p.ClipOut(c)
	if !ok {
		return 0
	}
	return int64(math.Round(float64(out-c.InMS) / c.SpeedFactor()))
}

// ClipOut returns the clip's resolved out_ms (the media's duration when
// OutMS is 0). ok is false if the media doesn't exist.
func (p *Project) ClipOut(c *Clip) (out int64, ok bool) {
	if c.OutMS != 0 {
		return c.OutMS, true
	}
	m := p.MediaByID(c.Media)
	if m == nil {
		return 0, false
	}
	return m.Probe.DurationMS, true
}

// presetExtensions lists the output file extensions each preset accepts
// (specification, section 11).
var presetExtensions = map[string][]string{
	"youtube_720p":  {".mp4", ".mov"},
	"youtube_1080p": {".mp4", ".mov"},
	"youtube_4k":    {".mp4", ".mov"},
	"reel":          {".mp4", ".mov"},
	"h265":          {".mp4", ".mov", ".mkv"},
	"prores":        {".mov"},
	"gif":           {".gif"},
	"mp3":           {".mp3"},
	"wav":           {".wav"},
	"flac":          {".flac"},
}

func extensionAllowed(preset, path string) (bool, []string) {
	exts := presetExtensions[preset]
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range exts {
		if e == ext {
			return true, exts
		}
	}
	return false, exts
}
