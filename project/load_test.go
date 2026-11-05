package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// problemsOf returns the problems of a *ValidationError (fails otherwise).
func problemsOf(t *testing.T, err error) []Problem {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	return ve.Problems
}

func TestLoad_LoaderFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "schema", "testdata", "loader", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no loader fixtures: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			var fx struct {
				Meta struct {
					ExpectError string `json:"expect_error"`
				} `json:"meta"`
			}
			data, _ := os.ReadFile(f)
			if err := json.Unmarshal(data, &fx); err != nil {
				t.Fatal(err)
			}
			_, _, err := Load(f)
			if err == nil {
				t.Fatalf("expected error %q, got none", fx.Meta.ExpectError)
			}
			probs := problemsOf(t, err)
			if len(probs) != 1 || probs[0].Message != fx.Meta.ExpectError {
				t.Errorf("problems:\n%v\nwant exactly:\n%s", err, fx.Meta.ExpectError)
			}
		})
	}
}

func TestLoad_ExamplesLoadWithoutErrors(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "examples", "*.json"))
	if len(files) == 0 {
		t.Fatal("no examples")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			p, warnings, err := Load(f)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			// The examples' media files don't exist in the repo: one
			// "file not found" warning per media entry, nothing else.
			if len(warnings) != len(p.Media) {
				t.Errorf("warnings = %v, want one missing-file warning per media entry", warnings)
			}
			for _, w := range warnings {
				if !strings.Contains(w.Message, "file not found") {
					t.Errorf("unexpected warning %s", w)
				}
			}
		})
	}
}

// base is a small valid project used by the rule tests.
const base = `{
  "version": 1,
  "format": {"width": 320, "height": 180, "fps": 30},
  "media": [
    {"id": "m_v", "kind": "video", "path": "v.mp4",
     "probe": {"duration_ms": 6000, "has_video": true, "has_audio": true, "width": 320, "height": 180, "fps": 30}},
    {"id": "m_silent", "kind": "video", "path": "s.mp4",
     "probe": {"duration_ms": 6000, "has_video": true, "has_audio": false, "width": 320, "height": 180, "fps": 30}},
    {"id": "m_a", "kind": "audio", "path": "a.wav", "probe": {"duration_ms": 6000, "has_audio": true}},
    {"id": "m_i", "kind": "image", "path": "i.png", "probe": {"width": 64, "height": 64}}
  ],
  "tracks": [
    {"id": "t_v", "kind": "video", "clips": [
      {"id": "c1", "type": "video", "media": "m_v", "at_ms": 0, "in_ms": 1000, "out_ms": 3000}
    ]},
    {"id": "t_a", "kind": "audio", "clips": [
      {"id": "c2", "type": "audio", "media": "m_a", "at_ms": 0}
    ]}
  ],
  "output": {"preset": "youtube_1080p", "path": "out/final.mp4"}
}`

// parseWith parses base after applying f to its generic form.
func parseWith(t *testing.T, f func(p map[string]any)) (*Project, []Warning, error) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(base), &doc); err != nil {
		t.Fatal(err)
	}
	if f != nil {
		f(doc)
	}
	data, _ := json.Marshal(doc)
	return Parse(data, t.TempDir())
}

func videoClips(p map[string]any) []any {
	return p["tracks"].([]any)[0].(map[string]any)["clips"].([]any)
}

func setVideoClips(p map[string]any, clips ...map[string]any) {
	list := make([]any, len(clips))
	for i, c := range clips {
		list[i] = c
	}
	p["tracks"].([]any)[0].(map[string]any)["clips"] = list
}

func vclip(id string, at, in, out int) map[string]any {
	return map[string]any{"id": id, "type": "video", "media": "m_v", "at_ms": at, "in_ms": in, "out_ms": out}
}

func TestParse_BaseIsValid(t *testing.T) {
	if _, _, err := parseWith(t, nil); err != nil {
		t.Fatal(err)
	}
}

func TestParse_SchemaErrorsHaveJSONPointers(t *testing.T) {
	_, _, err := parseWith(t, func(p map[string]any) {
		videoClips(p)[0].(map[string]any)["volume"] = 20
		videoClips(p)[0].(map[string]any)["effects"] = []any{map[string]any{"type": "blur"}}
	})
	probs := problemsOf(t, err)
	want := map[string]bool{"/tracks/0/clips/0/volume": false, "/tracks/0/clips/0/effects/0/type": false}
	for _, pr := range probs {
		if _, ok := want[pr.Path]; ok {
			want[pr.Path] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("no problem reported at %s; got:\n%v", path, err)
		}
	}
	if !strings.Contains(err.Error(), "/tracks/0/clips/0/effects/0/type: value must be one of 'speed', 'normalize'") {
		t.Errorf("error lines should be '<pointer>: <message>':\n%v", err)
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	_, _, err := Parse([]byte(`{"version": 1,`), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("err = %v", err)
	}
}

func TestParse_Rules(t *testing.T) {
	cases := []struct {
		name string
		edit func(p map[string]any)
		path string
		msg  string
	}{
		{"L1 duplicate id", func(p map[string]any) {
			videoClips(p)[0].(map[string]any)["id"] = "m_v"
		}, "/tracks/0/clips/0", `duplicate id "m_v" (already used at /media/0)`},
		{"L2 missing media", func(p map[string]any) {
			videoClips(p)[0].(map[string]any)["media"] = "nope"
		}, "/tracks/0/clips/0", `clip "c1": media "nope" does not exist`},
		{"L2 video clip on audio media", func(p map[string]any) {
			videoClips(p)[0].(map[string]any)["media"] = "m_a"
		}, "/tracks/0/clips/0", `clip "c1": media "m_a" is of kind "audio"; video clips need media of kind "video"`},
		{"L2 audio clip on silent video", func(p map[string]any) {
			c := p["tracks"].([]any)[1].(map[string]any)["clips"].([]any)[0].(map[string]any)
			c["media"] = "m_silent"
		}, "/tracks/1/clips/0", `clip "c2": media "m_silent" has no audio`},
		{"L2 image clip on video media", func(p map[string]any) {
			setVideoClips(p, map[string]any{"id": "c1", "type": "image", "media": "m_v", "at_ms": 0, "duration_ms": 1000})
		}, "/tracks/0/clips/0", `clip "c1": media "m_v" is of kind "video"; image clips need media of kind "image"`},
		{"L3 out past end", func(p map[string]any) {
			videoClips(p)[0].(map[string]any)["out_ms"] = 7000
		}, "/tracks/0/clips/0", `clip "c1": out_ms (7000) is past the end of media "m_v" (6000ms)`},
		{"L3 in after out", func(p map[string]any) {
			videoClips(p)[0].(map[string]any)["in_ms"] = 3000
		}, "/tracks/0/clips/0", `clip "c1": in_ms (3000) must be less than out_ms (3000)`},
		{"L3 in after source end", func(p map[string]any) {
			c := videoClips(p)[0].(map[string]any)
			delete(c, "out_ms")
			c["in_ms"] = 6000
		}, "/tracks/0/clips/0", `clip "c1": in_ms (6000) must be less than the end of media "m_v" (6000ms)`},
		{"L4 fades too long", func(p map[string]any) {
			c := videoClips(p)[0].(map[string]any)
			c["fade_in_ms"], c["fade_out_ms"] = 1500, 1000
		}, "/tracks/0/clips/0", `clip "c1": fade_in_ms + fade_out_ms (2500ms) exceed the clip's duration (2000ms)`},
		{"L4 fades use the sped-up duration", func(p map[string]any) {
			c := videoClips(p)[0].(map[string]any)
			c["fade_in_ms"] = 1500
			c["effects"] = []any{map[string]any{"type": "speed", "factor": 2}}
		}, "/tracks/0/clips/0", `clip "c1": fade_in_ms + fade_out_ms (1500ms) exceed the clip's duration (1000ms)`},
		{"L5 overlap on video track", func(p map[string]any) {
			setVideoClips(p, vclip("c1", 0, 0, 3000), vclip("c3", 2500, 0, 1000))
		}, "/tracks/0/clips/1", `clip "c3": overlaps clip "c1" on video track "t_v" by 500ms ("c1" ends at 3000ms, "c3" starts at 2500ms)`},
		{"L5 overlap with an earlier long clip", func(p map[string]any) {
			setVideoClips(p, vclip("c1", 0, 0, 5000), vclip("c3", 1000, 0, 1000), vclip("c4", 3000, 0, 1000))
		}, "/tracks/0/clips/2", `clip "c4": overlaps clip "c1" on video track "t_v" by 2000ms ("c1" ends at 5000ms, "c4" starts at 3000ms)`},
		{"L6 affect_audio false on audio clip", func(p map[string]any) {
			c := p["tracks"].([]any)[1].(map[string]any)["clips"].([]any)[0].(map[string]any)
			c["effects"] = []any{map[string]any{"type": "speed", "factor": 2, "affect_audio": false}}
		}, "/tracks/1/clips/0/effects/0", `clip "c2": speed.affect_audio false is only allowed on video clips`},
		{"L7 output extension", func(p map[string]any) {
			p["output"].(map[string]any)["path"] = "out/final.wav"
		}, "/output/path", `output: path "out/final.wav" has extension ".wav"; preset "youtube_1080p" needs .mp4 or .mov`},
		{"L7 output without extension", func(p map[string]any) {
			p["output"] = map[string]any{"preset": "wav", "path": "out/final"}
		}, "/output/path", `output: path "out/final" has no extension; preset "wav" needs .wav`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseWith(t, tc.edit)
			if err == nil {
				t.Fatal("expected an error")
			}
			probs := problemsOf(t, err)
			for _, pr := range probs {
				if pr.Path == tc.path && pr.Message == tc.msg {
					return
				}
			}
			t.Errorf("want problem\n%s: %s\ngot:\n%v", tc.path, tc.msg, err)
		})
	}
}

func TestParse_ReportsAllProblems(t *testing.T) {
	_, _, err := parseWith(t, func(p map[string]any) {
		videoClips(p)[0].(map[string]any)["out_ms"] = 9000
		p["output"].(map[string]any)["path"] = "x.gif"
	})
	if n := len(problemsOf(t, err)); n != 2 {
		t.Errorf("got %d problems, want 2:\n%v", n, err)
	}
}

func TestParse_Warnings(t *testing.T) {
	// No media files exist in the temp dir: one L8 warning per media entry.
	_, warnings, err := parseWith(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 4 || warnings[0].String() != `/media/0: media "m_v": file not found: v.mp4` {
		t.Errorf("warnings = %v", warnings)
	}

	// L10: fingerprint mismatch. L11: volume/normalize on a clip without sound.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "v.mp4"), []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal([]byte(base), &doc)
	doc["media"].([]any)[0].(map[string]any)["fingerprint"] = map[string]any{"size_bytes": 99}
	c := videoClips(doc)[0].(map[string]any)
	c["include_audio"] = false
	c["volume"] = 0.5
	c["effects"] = []any{map[string]any{"type": "normalize"}}
	data, _ := json.Marshal(doc)
	_, warnings, err = Parse(data, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`/tracks/0/clips/0: clip "c1": volume has no effect: the clip has no sound (include_audio is false)`,
		`/tracks/0/clips/0: clip "c1": normalize has no effect: the clip has no sound (include_audio is false)`,
		`/media/0: media "m_v": file changed since it was probed; re-import it`,
	}
	got := map[string]bool{}
	for _, w := range warnings {
		got[w.String()] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing warning %s\ngot %v", w, warnings)
		}
	}

	// Unchanged fingerprint: no L10 warning.
	info, _ := os.Stat(filepath.Join(dir, "v.mp4"))
	size, mod := info.Size(), info.ModTime().UnixMilli()
	doc["media"].([]any)[0].(map[string]any)["fingerprint"] = map[string]any{"size_bytes": size, "modified_unix_ms": mod}
	data, _ = json.Marshal(doc)
	_, warnings, _ = Parse(data, dir)
	for _, w := range warnings {
		if strings.Contains(w.Message, "changed since") {
			t.Errorf("unexpected stale warning for an unchanged file: %s", w)
		}
	}
}

func TestParse_CanonicalizesDefaults(t *testing.T) {
	p, _, err := parseWith(t, func(p map[string]any) {
		c := videoClips(p)[0].(map[string]any)
		c["out_ms"] = 6000 // = media duration
		c["in_ms"] = 0
		c["volume"] = 1
		c["include_audio"] = true
		c["anchor"] = "center"
		c["position"] = map[string]any{"x": 0.5, "y": 0.5}
		c["size"] = map[string]any{"width": 1, "height": 1, "fit": "contain"}
		c["effects"] = []any{map[string]any{"type": "speed", "factor": 2, "affect_audio": true}}
		p["tracks"].([]any)[0].(map[string]any)["enabled"] = true
		p["output"].(map[string]any)["fit"] = "contain"
	})
	if err != nil {
		t.Fatal(err)
	}
	c := p.Tracks[0].Clips[0]
	want := Clip{ID: "c1", Type: ClipVideo, Media: "m_v", Effects: []Effect{{Type: EffectSpeed, Factor: 2}}}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("clip not canonical:\n got %+v\nwant %+v", c, want)
	}
	if p.Tracks[0].Enabled != nil || p.Output.Fit != "" {
		t.Errorf("enabled %v fit %q not canonical", p.Tracks[0].Enabled, p.Output.Fit)
	}
	if !p.Tracks[0].IsEnabled() || c.VolumeValue() != 1 || !c.IncludesAudio() || c.AnchorValue() != "center" || c.PositionValue() != DefaultPosition {
		t.Error("accessors must return the defaults")
	}
	if d := p.ClipDuration(&c); d != 3000 {
		t.Errorf("ClipDuration = %d, want 6000/2 = 3000", d)
	}
}
