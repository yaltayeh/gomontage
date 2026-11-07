package project

import (
	"os"
	"os/exec"
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

// useTestFonts points fontconfig (FONTCONFIG_FILE) at testdata/fonts, so the
// font family "El Messiri" resolves on any machine.
func useTestFonts(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fc-list"); err != nil {
		t.Skip("fc-list not found (fontconfig): can't check font families")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "testdata", "fonts"))
	conf := filepath.Join(t.TempDir(), "fonts.conf")
	xml := `<?xml version="1.0"?><fontconfig><dir>` + dir + `</dir><cachedir>` + t.TempDir() + `</cachedir></fontconfig>`
	if err := os.WriteFile(conf, []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FONTCONFIG_FILE", conf)
}

// The reel example (overlays, layout, Arabic text with box/outline, cover)
// builds with no render blockers.
func TestBuild_ReelExample(t *testing.T) {
	useTestFonts(t)
	p := loadExampleWithFiles(t, "reel-arabic-overlay.json")
	if r := Check(p); !r.OK() {
		t.Fatalf("render blockers: %v", r.RenderBlockers)
	}
	tl, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.VideoTracks()) != 3 || len(tl.AudioTracks()) != 1 {
		t.Fatalf("tracks: %d video, %d audio", len(tl.VideoTracks()), len(tl.AudioTracks()))
	}
	// Layout and text style are mapped.
	logo := tl.VideoTracks()[1].Entries()[0].Clip
	if l, ok := logo.(*clip.ImageClip).Layout(); !ok || l.X != 0.95 || l.Anchor != clip.AnchorTopRight || l.Width != 0.18 {
		t.Errorf("logo layout %+v", l)
	}
	title := tl.VideoTracks()[2].Entries()[0].Clip.(*clip.TextClip)
	st := title.Style
	if st.Size != 134 || st.FontFamily != "El Messiri" || st.Background != "#000000AA" || st.BoxPadding != 40 || st.Align != "center" {
		t.Errorf("title style %+v (size 0.07*1920=134, padding 0.3*134=40)", st)
	}
	date := tl.VideoTracks()[2].Entries()[1].Clip.(*clip.TextClip)
	if date.Style.BorderWidth != 7 || date.Style.Font != p.Resolve("fonts/ElMessiri.ttf") {
		t.Errorf("date style %+v (outline 0.08*86=7)", date.Style)
	}
	if _, err := tl.DryRun(export.Reel(), filepath.Join(p.Dir(), "out.mp4")); err != nil {
		t.Errorf("DryRun: %v", err)
	}
}

// The format has no known gaps left: effects-transitions.json builds, and
// its transitions map to timeline transitions (handle model).
func TestBuild_Transitions(t *testing.T) {
	p := loadExampleWithFiles(t, "effects-transitions.json")
	tl, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Duration() != 27*time.Second {
		t.Errorf("duration %s, want 27s (transitions never change it)", tl.Duration())
	}
	main := tl.VideoTracks()[0].Entries()
	want := []*timeline.Transition{nil, nil, {Type: timeline.TransitionCrossfade, Duration: time.Second}, {Type: timeline.TransitionCrossfade, Duration: time.Second}}
	for i, e := range main {
		if !reflect.DeepEqual(e.TransitionIn, want[i]) {
			t.Errorf("main clip %d transition %+v, want %+v", i, e.TransitionIn, want[i])
		}
	}
	music := tl.AudioTracks()[0].Entries()
	if tr := music[1].TransitionIn; tr == nil || tr.Duration != 2*time.Second || music[0].TransitionIn != nil {
		t.Errorf("music transitions %+v %+v", music[0].TransitionIn, music[1].TransitionIn)
	}
	cmd, err := tl.DryRun(export.YouTube1080p(), filepath.Join(p.Dir(), "out.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	// c_drive -> c_beach at 11 s and c_beach -> c_sunset at 19 s (1 s each);
	// the music crossfades over 2 s.
	for _, s := range []string{"xfade=duration=1.000:offset=10.500:transition=fade", "xfade=duration=1.000:offset=18.500:transition=fade", "acrossfade=c1=tri:c2=tri:d=2.000"} {
		if !strings.Contains(cmd.String(), s) {
			t.Errorf("command lacks %q", s)
		}
	}
}

func TestBuild_FontFamilyMustBeInstalled(t *testing.T) {
	useTestFonts(t)
	p := New(t.TempDir(), Format{Width: 320, Height: 180, FPS: 30})
	p.Tracks = []Track{{ID: "t1", Kind: KindVideo, Clips: []Clip{
		{ID: "c1", Type: ClipText, Text: "hi", DurationMS: 1000, Style: &TextStyle{Font: &Font{Family: "No Such Font"}}},
		{ID: "c2", Type: ClipText, Text: "hi", AtMS: 1000, DurationMS: 1000, Style: &TextStyle{Font: &Font{Family: "el messiri"}}},
	}}}
	_, err := Build(p)
	if got := problemLines(t, err); len(got) != 1 || got[0] != `/tracks/0/clips/0/style/font/family: clip "c1": font family "No Such Font" is not installed (fontconfig can't find it)` {
		t.Errorf("problems: %v", got)
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

	t.Run("color with transparency builds", func(t *testing.T) {
		p := newP()
		p.Tracks = []Track{{ID: "t1", Kind: KindVideo, Clips: []Clip{
			{ID: "c1", Type: ClipColor, Color: "#00000080", DurationMS: 1000},
		}}}
		if _, err := Build(p); err != nil {
			t.Error(err)
		}
	})

	t.Run("text font file missing", func(t *testing.T) {
		p := newP()
		p.Tracks = []Track{{ID: "t1", Kind: KindVideo, Clips: []Clip{
			{ID: "c1", Type: ClipText, Text: "hi", DurationMS: 1000, Style: &TextStyle{Font: &Font{File: "fonts/x.ttf"}, Align: "left"}},
		}}}
		_, err := Build(p)
		if got := problemLines(t, err); len(got) != 1 || got[0] != `/tracks/0/clips/0/style/font/file: clip "c1": font file not found: fonts/x.ttf` {
			t.Errorf("problems: %v", got)
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
