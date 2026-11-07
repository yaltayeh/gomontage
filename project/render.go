package project

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ahmedhodiani/gomontage/export"
)

var presetProfiles = map[string]func() *export.Profile{
	"youtube_720p":  export.YouTube720p,
	"youtube_1080p": export.YouTube1080p,
	"youtube_4k":    export.YouTube4K,
	"reel":          export.Reel,
	"h265":          export.H265,
	"prores":        export.ProRes,
	"gif":           export.GIF,
	"mp3":           export.MP3,
	"wav":           export.WAV,
	"flac":          export.FLAC,
}

// Presets returns the output preset names, sorted.
func Presets() []string {
	names := make([]string, 0, len(presetProfiles))
	for n := range presetProfiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// PresetProfile returns the export profile for an output preset name
// (specification, section 11).
func PresetProfile(preset string) (*export.Profile, error) {
	f, ok := presetProfiles[preset]
	if !ok {
		return nil, fmt.Errorf("unknown output preset %q (known: %s)", preset, strings.Join(Presets(), ", "))
	}
	return f(), nil
}

// RenderTarget resolves the export profile and output file for rendering p.
//
// preset and path override p.Output when non-empty; path is used as given
// (relative to the working directory), while p.Output.Path is resolved
// against p.Dir(). It returns an error if no preset or path is known, the
// extension doesn't suit the preset (L7), or the project asks for an output
// fit the library can't do yet (output.fit "cover", L13).
func RenderTarget(p *Project, preset, path string) (*export.Profile, string, error) {
	var out Output
	if p.Output != nil {
		out = *p.Output
	}
	if out.FitValue() == FitCover {
		return nil, "", &ValidationError{Problems: []Problem{{Path: "/output/fit", Message: `output: fit "cover" is not supported yet`}}}
	}
	if preset == "" {
		preset = out.Preset
	}
	if preset == "" {
		return nil, "", fmt.Errorf("no output preset: set output.preset in the project or pass a preset (one of %s)", strings.Join(Presets(), ", "))
	}
	if path == "" && out.Path != "" {
		path = p.Resolve(out.Path)
	}
	if path == "" {
		return nil, "", fmt.Errorf("no output path: set output.path in the project or pass an output file")
	}
	profile, err := PresetProfile(preset)
	if err != nil {
		return nil, "", err
	}
	if ok, exts := extensionAllowed(preset, path); !ok {
		return nil, "", fmt.Errorf("output file %q: extension %q doesn't suit preset %q (use %s)",
			path, strings.ToLower(filepath.Ext(path)), preset, strings.Join(exts, " or "))
	}
	return profile, path, nil
}
