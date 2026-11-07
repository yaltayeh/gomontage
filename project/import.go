package project

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmedhodiani/gomontage/engine"
)

// imageFormats are ffprobe format names of still-image files.
var imageFormats = map[string]bool{
	"image2": true, "png_pipe": true, "jpeg_pipe": true, "webp_pipe": true,
	"bmp_pipe": true, "tiff_pipe": true, "gif_pipe": true, "pgm_pipe": true,
	"ppm_pipe": true, "qoi_pipe": true, "svg_pipe": true, "jpegxl_pipe": true,
}

// ImportMedia probes the file at path once and adds it to p's media bin with
// a new unique ID, which it returns. path may be absolute or relative to the
// working directory; it is stored relative to p.Dir() with '/' separators.
//
// The kind is detected from the probe: still images (PNG, JPEG, ...) become
// "image", files with a picture stream "video", files with only sound
// "audio". The probe results and a fingerprint (size, modification time) are
// stored. Errors are returned for missing files, files ffprobe can't read,
// files with neither picture nor sound, and video/audio files with no
// duration; p is left unchanged on error.
//
// IDs are "m_" plus the file name without extension, reduced to the allowed
// characters (e.g. "Interview Take 2.mov" -> "m_Interview_Take_2"), with
// "_2", "_3", ... appended if needed to stay unique across the project.
func ImportMedia(p *Project, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("import media %q: %w", path, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("import media %q: file not found", path)
		}
		return "", fmt.Errorf("import media %q: %w", path, err)
	}
	if st.IsDir() {
		return "", fmt.Errorf("import media %q: is a directory", path)
	}
	info, err := engine.Probe(abs)
	if err != nil {
		return "", fmt.Errorf("import media %q: cannot read media (is it a valid media file?): %w", path, err)
	}

	m := Media{Probe: Probe{}}
	switch {
	case info.HasVideo() && imageFormats[info.Format]:
		m.Kind = KindImage
		m.Probe.Width = info.VideoStreams[0].Width
		m.Probe.Height = info.VideoStreams[0].Height
	case info.HasVideo():
		m.Kind = KindVideo
		vs := info.VideoStreams[0]
		m.Probe.HasVideo = true
		m.Probe.Width, m.Probe.Height = vs.Width, vs.Height
		m.Probe.FPS = math.Round(vs.FPS*1000) / 1000
	case info.HasAudio():
		m.Kind = KindAudio
	default:
		return "", fmt.Errorf("import media %q: file has no video or audio stream", path)
	}
	if m.Kind != KindImage {
		m.Probe.DurationMS = info.Duration.Milliseconds()
		if m.Probe.DurationMS <= 0 {
			return "", fmt.Errorf("import media %q: duration is 0", path)
		}
		if info.HasAudio() {
			as := info.AudioStreams[0]
			m.Probe.HasAudio = true
			m.Probe.SampleRate, m.Probe.Channels = as.SampleRate, as.Channels
		}
		if m.Kind == KindVideo && m.Probe.FPS <= 0 {
			return "", fmt.Errorf("import media %q: cannot determine the frame rate", path)
		}
	}

	base := p.dir
	if base == "" {
		if base, err = os.Getwd(); err != nil {
			return "", fmt.Errorf("import media %q: %w", path, err)
		}
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return "", fmt.Errorf("import media %q: cannot store it relative to %q: %w", path, base, err)
	}
	m.Path = filepath.ToSlash(rel)

	size, mod := st.Size(), st.ModTime().UnixMilli()
	m.Fingerprint = &Fingerprint{SizeBytes: &size, ModifiedUnixMS: &mod}
	m.ID = uniqueID(p, mediaIDBase(abs))
	p.Media = append(p.Media, m)
	return m.ID, nil
}

// mediaIDBase derives an ID stem from a file name: "m_" + the name without
// extension, keeping [A-Za-z0-9_.-] and replacing other runs with '_'.
func mediaIDBase(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var b strings.Builder
	under := false
	for _, r := range name {
		ok := r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_')
		if ok {
			b.WriteRune(r)
			under = r == '_'
		} else if !under {
			b.WriteByte('_')
			under = true
		}
	}
	stem := strings.Trim(b.String(), "_.-")
	if stem == "" {
		stem = "media"
	}
	if len(stem) > 50 {
		stem = stem[:50]
	}
	return "m_" + stem
}

// uniqueID returns base, or base_2, base_3, ... — the first not used by any
// media entry, track or clip in p.
func uniqueID(p *Project, base string) string {
	used := map[string]bool{}
	for _, m := range p.Media {
		used[m.ID] = true
	}
	for _, t := range p.Tracks {
		used[t.ID] = true
		for _, c := range t.Clips {
			used[c.ID] = true
		}
	}
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		if id := fmt.Sprintf("%s_%d", base, n); !used[id] {
			return id
		}
	}
}
