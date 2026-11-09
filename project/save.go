package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Save writes p to path in canonical form:
//
//   - keys in a fixed order (the order of the Go struct fields, which follows
//     the schema), 2-space indent, trailing newline, no HTML escaping (so
//     text such as "قريباً" or "R&D" is written as is);
//   - fields equal to their default are omitted;
//   - relative paths use '/'. If path is in a different directory than
//     p.Dir(), media, font and output paths are rewritten to stay correct
//     relative to the new location.
//
// Saving the same project twice produces byte-identical files, and Load of
// the result yields a struct identical to the canonical form of p. The file
// is written atomically (temp file + rename). Save does not modify p.
func Save(path string, p *Project) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("project: save: %w", err)
	}
	data, err := marshal(p, filepath.Dir(abs))
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".gomontage-*.tmp")
	if err != nil {
		return fmt.Errorf("project: save: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("project: save: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("project: save: %w", err)
	}
	if err := os.Rename(tmp.Name(), abs); err != nil {
		return fmt.Errorf("project: save: %w", err)
	}
	return nil
}

// Marshal returns the canonical JSON encoding of p (as Save writes it), with
// paths relative to p.Dir().
func Marshal(p *Project) ([]byte, error) {
	return marshal(p, p.dir)
}

func marshal(p *Project, dir string) ([]byte, error) {
	c, err := clone(p)
	if err != nil {
		return nil, err
	}
	if c.Version == 0 {
		c.Version = FormatVersion
	}
	canonicalize(c)
	if err := rebase(c, p.dir, dir); err != nil {
		return nil, err
	}
	return encode(c)
}

// Clone returns a deep copy of p with the same Dir. Edits to the copy never
// affect p (and the other way round).
func (p *Project) Clone() *Project {
	c, err := clone(p)
	if err != nil {
		// clone only re-decodes what encode produced from the same types.
		panic(fmt.Sprintf("project: clone: %v", err))
	}
	return c
}

// clone deep-copies p through its JSON form (lossless: see Media.MarshalJSON).
func clone(p *Project) (*Project, error) {
	data, err := encode(p)
	if err != nil {
		return nil, err
	}
	var c Project
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("project: copy: %w", err)
	}
	c.dir = p.dir
	return &c, nil
}

func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("project: encode: %w", err)
	}
	return buf.Bytes(), nil // Encode ends with a newline
}

// rebase rewrites relative paths written for fromDir so they are correct
// relative to toDir, and normalizes separators to '/'.
func rebase(p *Project, fromDir, toDir string) error {
	move := func(rel string) (string, error) {
		if rel == "" {
			return rel, nil
		}
		native := filepath.FromSlash(rel)
		if fromDir == "" || toDir == "" || filepath.Clean(fromDir) == filepath.Clean(toDir) {
			return filepath.ToSlash(native), nil
		}
		target := filepath.Join(fromDir, native)
		out, err := filepath.Rel(toDir, target)
		if err != nil {
			return "", fmt.Errorf("project: save: cannot make %q relative to %q: %w", target, toDir, err)
		}
		return filepath.ToSlash(out), nil
	}
	var err error
	for i := range p.Media {
		if p.Media[i].Path, err = move(p.Media[i].Path); err != nil {
			return err
		}
	}
	for ti := range p.Tracks {
		for ci := range p.Tracks[ti].Clips {
			if st := p.Tracks[ti].Clips[ci].Style; st != nil && st.Font != nil {
				if st.Font.File, err = move(st.Font.File); err != nil {
					return err
				}
			}
		}
	}
	if p.Output != nil {
		if p.Output.Path, err = move(p.Output.Path); err != nil {
			return err
		}
	}
	return nil
}

// probeJSON is the encoded form of Probe: pointer fields so each field's
// presence is controlled explicitly.
type probeJSON struct {
	DurationMS *int64   `json:"duration_ms,omitempty"`
	HasVideo   *bool    `json:"has_video,omitempty"`
	HasAudio   *bool    `json:"has_audio,omitempty"`
	Width      *int     `json:"width,omitempty"`
	Height     *int     `json:"height,omitempty"`
	FPS        *float64 `json:"fps,omitempty"`
	SampleRate *int     `json:"sample_rate,omitempty"`
	Channels   *int     `json:"channels,omitempty"`
}

// MarshalJSON encodes a media entry, writing the probe fields the schema
// requires for its kind (even when false or 0, e.g. has_audio: false for a
// silent video) plus every other non-zero probe field. It is lossless.
func (m Media) MarshalJSON() ([]byte, error) {
	pr := m.Probe
	var req map[string]bool
	switch m.Kind {
	case KindVideo:
		req = map[string]bool{"duration_ms": true, "has_video": true, "has_audio": true, "width": true, "height": true, "fps": true}
	case KindAudio:
		req = map[string]bool{"duration_ms": true, "has_audio": true}
	case KindImage:
		req = map[string]bool{"width": true, "height": true}
	}
	var pj probeJSON
	if req["duration_ms"] || pr.DurationMS != 0 {
		pj.DurationMS = &pr.DurationMS
	}
	if req["has_video"] || pr.HasVideo {
		pj.HasVideo = &pr.HasVideo
	}
	if req["has_audio"] || pr.HasAudio {
		pj.HasAudio = &pr.HasAudio
	}
	if req["width"] || pr.Width != 0 {
		pj.Width = &pr.Width
	}
	if req["height"] || pr.Height != 0 {
		pj.Height = &pr.Height
	}
	if req["fps"] || pr.FPS != 0 {
		pj.FPS = &pr.FPS
	}
	if pr.SampleRate != 0 {
		pj.SampleRate = &pr.SampleRate
	}
	if pr.Channels != 0 {
		pj.Channels = &pr.Channels
	}
	return encodeCompact(struct {
		ID          string       `json:"id"`
		Kind        string       `json:"kind"`
		Path        string       `json:"path"`
		Probe       probeJSON    `json:"probe"`
		Fingerprint *Fingerprint `json:"fingerprint,omitempty"`
	}{m.ID, m.Kind, m.Path, pj, m.Fingerprint})
}

func encodeCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
