package project

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/export"
	"github.com/ahmedhodiani/gomontage/timeline"
)

// loadExampleWithFiles copies an example into a temp dir, creates empty
// placeholder files for its media (and fonts), and loads it. Build only
// checks that files exist; DryRun never reads them.
func loadExampleWithFiles(t *testing.T, name string) *Project {
	t.Helper()
	dir := t.TempDir()
	path := copyFile(t, filepath.Join("..", "examples", name), dir)
	p, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	touch := func(rel string) {
		f := p.Resolve(rel)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range p.Media {
		touch(m.Path)
	}
	for _, tr := range p.Tracks {
		for _, c := range tr.Clips {
			if c.Style != nil && c.Style.Font != nil && c.Style.Font.File != "" {
				touch(c.Style.Font.File)
			}
		}
	}
	return p
}

func problemLines(t *testing.T, err error) []string {
	t.Helper()
	var lines []string
	for _, p := range problemsOf(t, err) {
		lines = append(lines, p.String())
	}
	return lines
}

// Build of simple-trim-concat must compile to exactly the FFmpeg command of
// the same timeline written by hand with the Go API.
func TestBuild_SimpleTrimConcatEqualsHandWritten(t *testing.T) {
	p := loadExampleWithFiles(t, "simple-trim-concat.json")
	built, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}

	abs := func(rel string) string { return filepath.Join(p.Dir(), filepath.FromSlash(rel)) }
	hand := timeline.New(timeline.Config{Width: 1920, Height: 1080, FPS: 30})
	main := hand.AddVideoTrack("main")
	main.Add(clip.NewVideoWithDuration(abs("media/intro.mp4"), 6*time.Second).WithFadeIn(500*time.Millisecond), 0)
	interview := clip.NewVideoWithDuration(abs("media/interview.mov"), 312480*time.Millisecond)
	main.Add(interview.Trim(12*time.Second, 27500*time.Millisecond), 6*time.Second)
	main.Add(interview.Trim(95250*time.Millisecond, 118*time.Second), 21500*time.Millisecond)
	main.Add(clip.NewVideoWithDuration(abs("media/outro.mp4"), 4*time.Second).VideoOnly().WithFadeOut(time.Second), 44250*time.Millisecond)

	out := filepath.Join(p.Dir(), "output", "interview-cut.mp4")
	got, err := built.DryRun(export.YouTube1080p(), out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := hand.DryRun(export.YouTube1080p(), out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Args, want.Args) {
		t.Errorf("Build DryRun differs from hand-written timeline:\n got %s\nwant %s", got, want)
	}
	if built.Duration() != 48250*time.Millisecond {
		t.Errorf("duration %s, want 48.25s", built.Duration())
	}
}

func TestBuild_PodcastExample(t *testing.T) {
	p := loadExampleWithFiles(t, "podcast-audio.json")
	tl, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	// The disabled music bed track is not built.
	if n := len(tl.AudioTracks()); n != 2 || len(tl.VideoTracks()) != 0 {
		t.Fatalf("tracks: %d audio, %d video; want 2 audio, 0 video", n, len(tl.VideoTracks()))
	}
	guest := tl.AudioTracks()[1].Entries()[1].Clip
	if guest.HasVideo() || !guest.HasAudio() || guest.SourcePath() != p.Resolve("recordings/guest-call.mp4") {
		t.Errorf("guest clip: video %v audio %v path %s", guest.HasVideo(), guest.HasAudio(), guest.SourcePath())
	}
	if tl.Duration() != 1216*time.Second {
		t.Errorf("duration %s, want 20m16s", tl.Duration())
	}
	if _, err := tl.DryRun(export.WAV(), filepath.Join(p.Dir(), "out.wav")); err != nil {
		t.Errorf("DryRun: %v", err)
	}
}

func TestBuild_KnownGapErrors(t *testing.T) {
	cases := map[string][]string{
		"reel-arabic-overlay.json": {
			`/tracks/0/clips/0: clip "c_broll": size is not supported yet (known bug #2)`,
			`/tracks/1: track "t_logo": overlay tracks are not supported yet (known bug #1)`,
			`/tracks/1/clips/0: clip "c_logo": position, anchor and size are not supported yet (known bug #2)`,
			`/tracks/2: track "t_titles": overlay tracks are not supported yet (known bug #1)`,
			`/tracks/2/clips/0: clip "c_title": text clips are not supported yet: text is always drawn at the top-left corner (known bug #2)`,
			`/tracks/2/clips/0/style/font/family: clip "c_title": style.font.family is not supported yet (use style.font.file)`,
			`/tracks/2/clips/0/style/box: clip "c_title": style.box is not supported yet`,
			`/tracks/2/clips/1: clip "c_date": text clips are not supported yet: text is always drawn at the top-left corner (known bug #2)`,
			`/tracks/2/clips/1/style/outline: clip "c_date": style.outline is not supported yet`,
		},
		"effects-transitions.json": {
			`/tracks/0/clips/2/transition_in: clip "c_beach": transition_in is not supported yet`,
			`/tracks/0/clips/2/effects/0/affect_audio: clip "c_beach": speed.affect_audio false is not supported yet`,
			`/tracks/0/clips/3/transition_in: clip "c_sunset": transition_in is not supported yet`,
			`/tracks/1/clips/1/transition_in: clip "c_song_b": transition_in is not supported yet`,
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Build(loadExampleWithFiles(t, name))
			if err == nil {
				t.Fatal("expected Build to fail")
			}
			if got := problemLines(t, err); !reflect.DeepEqual(got, want) {
				t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

func TestBuild_MissingMediaIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := copyFile(t, filepath.Join("..", "examples", "simple-trim-concat.json"), dir)
	p, warnings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 3 {
		t.Errorf("Load: want 3 missing-file warnings, got %v", warnings)
	}
	_, err = Build(p)
	want := []string{
		`/media/0: media "m_intro": file not found: media/intro.mp4`,
		`/media/1: media "m_interview": file not found: media/interview.mov`,
		`/media/2: media "m_outro": file not found: media/outro.mp4`,
	}
	if got := problemLines(t, err); !reflect.DeepEqual(got, want) {
		t.Errorf("problems:\n%s", strings.Join(got, "\n"))
	}
}

func TestBuild_RenderChecks(t *testing.T) {
	dir := t.TempDir()
	newP := func() *Project { return New(dir, Format{Width: 320, Height: 180, FPS: 30}) }

	t.Run("no enabled clips", func(t *testing.T) {
		p := newP()
		off := false
		p.Tracks = []Track{
			{ID: "t1", Kind: KindVideo, Clips: []Clip{}},
			{ID: "t2", Kind: KindVideo, Enabled: &off, Clips: []Clip{{ID: "c1", Type: ClipColor, Color: "#000000", DurationMS: 1000}}},
		}
		_, err := Build(p)
		if got := problemLines(t, err); len(got) != 1 || got[0] != "/: project has no enabled clips to render" {
			t.Errorf("problems: %v", got)
		}
	})

	t.Run("color with transparency", func(t *testing.T) {
		p := newP()
		p.Tracks = []Track{{ID: "t1", Kind: KindVideo, Clips: []Clip{
			{ID: "c1", Type: ClipColor, Color: "#00000080", DurationMS: 1000},
			{ID: "c2", Type: ClipColor, Color: "#000000ff", AtMS: 1000, DurationMS: 1000},
		}}}
		_, err := Build(p)
		if got := problemLines(t, err); len(got) != 1 || got[0] != `/tracks/0/clips/0/color: clip "c1": color "#00000080" has transparency, which is not supported yet` {
			t.Errorf("problems: %v", got)
		}
	})

	t.Run("text font file missing", func(t *testing.T) {
		p := newP()
		p.Tracks = []Track{{ID: "t1", Kind: KindVideo, Clips: []Clip{
			{ID: "c1", Type: ClipText, Text: "hi", DurationMS: 1000, Style: &TextStyle{Font: &Font{File: "fonts/x.ttf"}, Align: "left"}},
		}}}
		_, err := Build(p)
		got := strings.Join(problemLines(t, err), "\n")
		for _, w := range []string{
			`/tracks/0/clips/0/style/font/file: clip "c1": font file not found: fonts/x.ttf`,
			`/tracks/0/clips/0/style/align: clip "c1": style.align "left" is not supported yet`,
		} {
			if !strings.Contains(got, w) {
				t.Errorf("missing %s in:\n%s", w, got)
			}
		}
	})

	t.Run("disabled tracks are not checked", func(t *testing.T) {
		p := newP()
		off := false
		p.Tracks = []Track{
			{ID: "t1", Kind: KindVideo, Clips: []Clip{{ID: "c1", Type: ClipColor, Color: "#000000", DurationMS: 1000}}},
			{ID: "t2", Kind: KindVideo, Enabled: &off, Clips: []Clip{{ID: "c2", Type: ClipText, Text: "x", DurationMS: 1000}}},
		}
		tl, err := Build(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(tl.VideoTracks()) != 1 {
			t.Errorf("%d video tracks built, want 1", len(tl.VideoTracks()))
		}
	})

	t.Run("programmatic projects are validated", func(t *testing.T) {
		p := newP()
		p.Tracks = []Track{{ID: "t1", Kind: KindVideo, Clips: []Clip{
			{ID: "t1", Type: ClipColor, Color: "#000000", DurationMS: 1000},
		}}}
		_, err := Build(p)
		if got := problemLines(t, err); len(got) != 1 || !strings.Contains(got[0], `duplicate id "t1"`) {
			t.Errorf("problems: %v", got)
		}
		p.Tracks[0].Clips[0].Color = "black"
		p.Tracks[0].Clips[0].ID = "c1"
		_, err = Build(p)
		if got := problemLines(t, err); len(got) != 1 || !strings.HasPrefix(got[0], "/tracks/0/clips/0/color: ") {
			t.Errorf("schema problem expected, got %v", got)
		}
	})
}
