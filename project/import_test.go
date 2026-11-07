package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Imports of real media files are tested in e2e/project_test.go (they need
// ffprobe); these tests cover the parts that don't.

func TestImportMedia_MissingFile(t *testing.T) {
	p := New(t.TempDir(), Format{Width: 320, Height: 180, FPS: 30})
	_, err := ImportMedia(p, filepath.Join(p.Dir(), "nope.mp4"))
	if err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("err = %v, want file not found", err)
	}
	if len(p.Media) != 0 {
		t.Error("project changed on error")
	}
}

func TestImportMedia_NotMedia(t *testing.T) {
	p := New(t.TempDir(), Format{Width: 320, Height: 180, FPS: 30})
	f := filepath.Join(p.Dir(), "notes.mp4")
	if err := os.WriteFile(f, []byte("not a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ImportMedia(p, f)
	if err == nil || !strings.Contains(err.Error(), "cannot read media") {
		t.Errorf("err = %v, want cannot read media", err)
	}
	if _, err := ImportMedia(p, p.Dir()); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("dir: err = %v", err)
	}
}

func TestMediaIDs(t *testing.T) {
	for in, want := range map[string]string{
		"/a/Interview Take 2.mov": "m_Interview_Take_2",
		"logo.png":                "m_logo",
		"قريباً.mp4":              "m_media",
		"__x__.wav":               "m_x",
		"a.b-c.mp3":               "m_a.b-c",
	} {
		if got := mediaIDBase(in); got != want {
			t.Errorf("mediaIDBase(%q) = %q, want %q", in, got, want)
		}
	}
	if got := mediaIDBase(strings.Repeat("x", 100) + ".mp4"); len(got) > 64 {
		t.Errorf("id too long: %d", len(got))
	}
	p := New("", Format{})
	p.Media = []Media{{ID: "m_logo"}}
	p.Tracks = []Track{{ID: "m_logo_2", Clips: []Clip{{ID: "m_logo_3"}}}}
	if got := uniqueID(p, "m_logo"); got != "m_logo_4" {
		t.Errorf("uniqueID = %q, want m_logo_4 (ids are unique across media, tracks and clips)", got)
	}
}
