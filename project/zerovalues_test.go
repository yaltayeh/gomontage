package project

import (
	"encoding/json"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/effects"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Fields whose format default is NOT Go's zero value must keep an explicit
// zero (false, 0, {0,0}) through Load → Save → Load and must not be treated
// as "unset". These are all pointer fields in the Go types; canonicalization
// only clears values equal to the *format* default.
//
// Fields with a non-zero default whose zero value the schema rejects
// (target_lufs, style.size, outline.width, out_ms, and the enum/color strings
// anchor, size.fit, output.fit, style.color/align/direction) can't hold an
// explicit zero; TestZeroValues_NotRepresentable checks the schema rejects it.

const zeroBase = `{
  "version": 1,
  "format": {"width": 320, "height": 180, "fps": 30},
  "media": [
    {"id": "m_v", "kind": "video", "path": "v.mp4",
     "probe": {"duration_ms": 4000, "has_video": true, "has_audio": true, "width": 320, "height": 180, "fps": 30}},
    {"id": "m_a", "kind": "audio", "path": "a.wav", "probe": {"duration_ms": 4000, "has_audio": true}}
  ],
  "tracks": [
    {"id": "t_v", "kind": "video", "clips": [
      {"id": "c_v", "type": "video", "media": "m_v", "at_ms": 0}
    ]},
    {"id": "t_a", "kind": "audio", "clips": [
      {"id": "c_a", "type": "audio", "media": "m_a", "at_ms": 0}
    ]},
    {"id": "t_txt", "kind": "video", "clips": [
      {"id": "c_t", "type": "text", "text": "hi", "at_ms": 0, "duration_ms": 1000,
       "style": {"box": {"color": "#000000"}}}
    ]}
  ]
}`

type zeroCase struct {
	name  string
	edit  func(doc map[string]any)
	saved string                // must appear in the saved file
	check func(p *Project) bool // the loaded struct holds the explicit zero
}

func zeroCases() []zeroCase {
	clipAt := func(doc map[string]any, t, c int) map[string]any {
		return doc["tracks"].([]any)[t].(map[string]any)["clips"].([]any)[c].(map[string]any)
	}
	return []zeroCase{
		{"track enabled false",
			func(d map[string]any) { d["tracks"].([]any)[2].(map[string]any)["enabled"] = false },
			`"enabled": false`,
			func(p *Project) bool { e := p.Tracks[2].Enabled; return e != nil && !*e && !p.Tracks[2].IsEnabled() }},
		{"video clip volume 0",
			func(d map[string]any) { clipAt(d, 0, 0)["volume"] = 0 },
			`"volume": 0`,
			func(p *Project) bool { v := p.Tracks[0].Clips[0].Volume; return v != nil && *v == 0 }},
		{"audio clip volume 0",
			func(d map[string]any) { clipAt(d, 1, 0)["volume"] = 0 },
			`"volume": 0`,
			func(p *Project) bool { c := p.Tracks[1].Clips[0]; return c.Volume != nil && c.VolumeValue() == 0 }},
		{"include_audio false",
			func(d map[string]any) { clipAt(d, 0, 0)["include_audio"] = false },
			`"include_audio": false`,
			func(p *Project) bool { c := p.Tracks[0].Clips[0]; return c.IncludeAudio != nil && !c.IncludesAudio() }},
		{"position 0,0",
			func(d map[string]any) { clipAt(d, 0, 0)["position"] = map[string]any{"x": 0, "y": 0} },
			`"position": {`,
			func(p *Project) bool {
				pos := p.Tracks[0].Clips[0].Position
				return pos != nil && *pos == Position{} && p.Tracks[0].Clips[0].PositionValue() == Position{}
			}},
		{"speed affect_audio false",
			func(d map[string]any) {
				clipAt(d, 0, 0)["effects"] = []any{map[string]any{"type": "speed", "factor": 2, "affect_audio": false}}
			},
			`"affect_audio": false`,
			func(p *Project) bool {
				e := p.Tracks[0].Clips[0].Effects[0]
				return e.AffectAudio != nil && !e.AffectsAudio()
			}},
		{"box padding 0",
			func(d map[string]any) {
				clipAt(d, 2, 0)["style"].(map[string]any)["box"].(map[string]any)["padding"] = 0
			},
			`"padding": 0`,
			func(p *Project) bool { pd := p.Tracks[2].Clips[0].Style.Box.Padding; return pd != nil && *pd == 0 }},
	}
}

func parseZero(t *testing.T, dir string, edit func(map[string]any)) *Project {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(zeroBase), &doc); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(doc)
	}
	data, _ := json.Marshal(doc)
	p, _, err := Parse(data, dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestZeroValues_SurviveLoadSaveLoad(t *testing.T) {
	for _, tc := range zeroCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p1 := parseZero(t, dir, tc.edit)
			if !tc.check(p1) {
				t.Fatal("explicit zero lost on load")
			}
			path := filepath.Join(dir, "p.gomontage.json")
			if err := Save(path, p1); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			if !strings.Contains(string(data), tc.saved) {
				t.Errorf("saved file lacks %s:\n%s", tc.saved, data)
			}
			p2, _, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.check(p2) || !reflect.DeepEqual(p1, p2) {
				t.Errorf("explicit zero changed by Save → Load:\nfirst  %+v\nsecond %+v", p1.Tracks, p2.Tracks)
			}
		})
	}
}

// The explicit zeros also reach the timeline: muted clips are built with
// volume 0 (not 1), disabled tracks are not built, include_audio false drops
// the sound. (Rendering a muted clip is checked in e2e/project_test.go.)
func TestZeroValues_Build(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"v.mp4", "a.wav"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The text track is a known gap (text clips); disable it so the rest
	// builds, which also exercises enabled: false.
	p := parseZero(t, dir, func(d map[string]any) {
		d["tracks"].([]any)[2].(map[string]any)["enabled"] = false
		clips := func(i int) map[string]any {
			return d["tracks"].([]any)[i].(map[string]any)["clips"].([]any)[0].(map[string]any)
		}
		clips(0)["volume"] = 0
		clips(1)["volume"] = 0
	})
	tl, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(tl.VideoTracks()); n != 1 {
		t.Errorf("%d video tracks built, want 1 (the disabled one skipped)", n)
	}
	if v := tl.VideoTracks()[0].Entries()[0].Clip.Volume(); v != 0 {
		t.Errorf("video clip volume = %g, want 0", v)
	}
	if v := tl.AudioTracks()[0].Entries()[0].Clip.Volume(); v != 0 {
		t.Errorf("audio clip volume = %g, want 0", v)
	}

	p = parseZero(t, dir, func(d map[string]any) {
		d["tracks"].([]any)[2].(map[string]any)["enabled"] = false
		d["tracks"].([]any)[0].(map[string]any)["clips"].([]any)[0].(map[string]any)["include_audio"] = false
	})
	tl, err = Build(p)
	if err != nil {
		t.Fatal(err)
	}
	if tl.VideoTracks()[0].Entries()[0].Clip.HasAudio() {
		t.Error("include_audio false: built clip still has audio")
	}

	// position {0,0} and affect_audio false reach the timeline (phase 4: no
	// longer known gaps) instead of being treated as defaults.
	p = parseZero(t, dir, func(d map[string]any) {
		d["tracks"].([]any)[2].(map[string]any)["enabled"] = false
		c := d["tracks"].([]any)[0].(map[string]any)["clips"].([]any)[0].(map[string]any)
		c["position"] = map[string]any{"x": 0, "y": 0}
		c["effects"] = []any{map[string]any{"type": "speed", "factor": 2, "affect_audio": false}}
	})
	tl, err = Build(p)
	if err != nil {
		t.Fatal(err)
	}
	vc := tl.VideoTracks()[0].Entries()[0].Clip.(*clip.VideoClip)
	if l, ok := vc.Layout(); !ok || l.X != 0 || l.Y != 0 || l.Anchor != clip.AnchorCenter {
		t.Errorf("position {0,0}: layout %+v %v", l, ok)
	}
	if sp, ok := vc.Effects()[0].(*effects.SpeedEffect); !ok || sp.AffectsAudio() {
		t.Errorf("affect_audio false: effect %+v", vc.Effects()[0])
	}
}

// Go code that sets a pointer field to a zero value keeps it too.
func TestZeroValues_ProgrammaticPointers(t *testing.T) {
	dir := t.TempDir()
	p := parseZero(t, dir, nil)
	zero, off := 0.0, false
	p.Tracks[0].Clips[0].Volume = &zero
	p.Tracks[2].Enabled = &off
	path := filepath.Join(dir, "p.gomontage.json")
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	q, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if q.Tracks[0].Clips[0].VolumeValue() != 0 || q.Tracks[2].IsEnabled() {
		t.Errorf("volume %g enabled %v", q.Tracks[0].Clips[0].VolumeValue(), q.Tracks[2].IsEnabled())
	}
}

// Zero values the schema rejects can't be confused with "unset".
func TestZeroValues_NotRepresentable(t *testing.T) {
	cases := map[string]func(d map[string]any){
		"target_lufs 0": func(d map[string]any) {
			d["tracks"].([]any)[0].(map[string]any)["clips"].([]any)[0].(map[string]any)["effects"] =
				[]any{map[string]any{"type": "normalize", "target_lufs": 0}}
		},
		"style size 0": func(d map[string]any) {
			d["tracks"].([]any)[2].(map[string]any)["clips"].([]any)[0].(map[string]any)["style"].(map[string]any)["size"] = 0
		},
		"outline width 0": func(d map[string]any) {
			d["tracks"].([]any)[2].(map[string]any)["clips"].([]any)[0].(map[string]any)["style"].(map[string]any)["outline"] =
				map[string]any{"color": "#000000", "width": 0}
		},
		"out_ms 0": func(d map[string]any) {
			d["tracks"].([]any)[0].(map[string]any)["clips"].([]any)[0].(map[string]any)["out_ms"] = 0
		},
		"empty anchor": func(d map[string]any) {
			d["tracks"].([]any)[0].(map[string]any)["clips"].([]any)[0].(map[string]any)["anchor"] = ""
		},
		"empty text color": func(d map[string]any) {
			d["tracks"].([]any)[2].(map[string]any)["clips"].([]any)[0].(map[string]any)["style"].(map[string]any)["color"] = ""
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			_ = json.Unmarshal([]byte(zeroBase), &doc)
			edit(doc)
			data, _ := json.Marshal(doc)
			if _, _, err := Parse(data, t.TempDir()); err == nil {
				t.Error("schema should reject this zero value")
			}
		})
	}
}
