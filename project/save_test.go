package project

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// copyFile copies src into dir and returns the new path.
func copyFile(t *testing.T, src, dir string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, filepath.Base(src))
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestSave_RoundTripAndByteStable(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "examples", "*.json"))
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			dir := t.TempDir()
			path := copyFile(t, f, dir)

			p1, _, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			out1 := filepath.Join(dir, "a.gomontage.json")
			if err := Save(out1, p1); err != nil {
				t.Fatal(err)
			}
			p2, _, err := Load(out1)
			if err != nil {
				t.Fatalf("reloading saved file: %v", err)
			}
			if !reflect.DeepEqual(p1, p2) {
				t.Errorf("Load→Save→Load changed the project:\nfirst  %+v\nsecond %+v", p1, p2)
			}

			out2 := filepath.Join(dir, "b.gomontage.json")
			if err := Save(out2, p2); err != nil {
				t.Fatal(err)
			}
			b1, _ := os.ReadFile(out1)
			b2, _ := os.ReadFile(out2)
			if !bytes.Equal(b1, b2) {
				t.Error("Save→Load→Save is not byte-identical")
			}
			if !bytes.HasSuffix(b1, []byte("}\n")) || bytes.HasSuffix(b1, []byte("\n\n")) {
				t.Error("saved file must end with exactly one newline")
			}
		})
	}
}

// The examples are kept in canonical form, so they double as golden files
// for Save's output format.
func TestSave_ExamplesAreCanonical(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "examples", "*.json"))
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			p, _, err := Load(f)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := os.ReadFile(f)
			if !bytes.Equal(got, want) {
				t.Errorf("%s is not in canonical form; regenerate it with Save. Canonical form:\n%s", f, got)
			}
		})
	}
}

func TestSave_OmitsDefaults(t *testing.T) {
	dir := t.TempDir()
	p := New(dir, Format{Width: 1920, Height: 1080, FPS: 30})
	one, tru, lufs, size := 1.0, true, -16.0, 0.05
	p.Media = []Media{{ID: "m1", Kind: KindVideo, Path: "a.mp4", Probe: Probe{DurationMS: 5000, HasVideo: true, HasAudio: false, Width: 1920, Height: 1080, FPS: 30}}}
	p.Tracks = []Track{
		{ID: "t1", Kind: KindVideo, Enabled: &tru, Clips: []Clip{
			{ID: "c1", Type: ClipVideo, Media: "m1", OutMS: 5000, Volume: &one, IncludeAudio: &tru,
				Anchor: "center", Position: &Position{X: 0.5, Y: 0.5}, Size: &Size{Width: 1, Height: 1, Fit: "contain"},
				Effects: []Effect{{Type: EffectNormalize, TargetLUFS: &lufs}}},
			{ID: "c2", Type: ClipText, Text: "R&D <ok> قريباً", AtMS: 5000, DurationMS: 1000,
				Style: &TextStyle{Size: &size, Color: "#ffffff", Align: "center", Direction: "auto"}},
		}},
	}
	p.Output = &Output{Preset: "youtube_1080p", Path: "out.mp4", Fit: "contain"}
	path := filepath.Join(dir, "p.gomontage.json")
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	for _, field := range []string{`"volume"`, `"include_audio"`, `"anchor"`, `"position"`, `"size"`, `"enabled"`,
		`"out_ms"`, `"in_ms"`, `"fade_in_ms"`, `"target_lufs"`, `"style"`, `"fit"`, `"label"`, `"meta"`, `"fingerprint"`} {
		if strings.Contains(s, field) {
			t.Errorf("saved file contains default-valued field %s:\n%s", field, s)
		}
	}
	// Required probe fields are kept even when false.
	if !strings.Contains(s, `"has_audio": false`) {
		t.Errorf("has_audio: false must be written for video media:\n%s", s)
	}
	// No HTML escaping; Unicode written as is.
	if !strings.Contains(s, `"text": "R&D <ok> قريباً"`) {
		t.Errorf("text not written literally:\n%s", s)
	}
	if !strings.HasPrefix(s, "{\n  \"version\": 1,\n  \"format\": {\n    \"width\": 1920,") {
		t.Errorf("unexpected layout/order:\n%s", s)
	}
	// The saved file is valid and loads back to the canonical struct.
	if _, _, err := Load(path); err != nil {
		t.Fatalf("saved file does not load: %v", err)
	}
	// Save didn't modify p.
	if p.Tracks[0].Clips[0].Volume == nil || p.Output.Fit != "contain" {
		t.Error("Save modified its argument")
	}
}

func TestSave_RebasesPathsWhenSavedElsewhere(t *testing.T) {
	root := t.TempDir()
	p := New(filepath.Join(root, "proj"), Format{Width: 320, Height: 180, FPS: 30})
	p.Media = []Media{{ID: "m1", Kind: KindImage, Path: "media/logo.png", Probe: Probe{Width: 10, Height: 10}}}
	p.Output = &Output{Preset: "gif", Path: "out/a.gif"}
	p.Tracks = []Track{{ID: "t1", Kind: KindVideo, Clips: []Clip{
		{ID: "c1", Type: ClipText, Text: "x", DurationMS: 1000, Style: &TextStyle{Font: &Font{File: "fonts/a.ttf"}}},
	}}}
	if err := os.MkdirAll(filepath.Join(root, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "other", "copy.gomontage.json")
	if err := Save(dst, p); err != nil {
		t.Fatal(err)
	}
	q, _, err := Load(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Media[0].Path; got != "../proj/media/logo.png" {
		t.Errorf("media path = %q", got)
	}
	if got := q.Tracks[0].Clips[0].Style.Font.File; got != "../proj/fonts/a.ttf" {
		t.Errorf("font path = %q", got)
	}
	if got := q.Output.Path; got != "../proj/out/a.gif" {
		t.Errorf("output path = %q", got)
	}
	if q.Resolve(q.Media[0].Path) != p.Resolve(p.Media[0].Path) {
		t.Errorf("resolved paths differ: %s vs %s", q.Resolve(q.Media[0].Path), p.Resolve(p.Media[0].Path))
	}
}
