//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/project"
)

// moveInto moves a generated fixture to dir/rel (creating directories).
func moveInto(t *testing.T, src, dir, rel string) string {
	t.Helper()
	dst := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestProject_ImportMedia(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	p := project.New(dir, project.Format{Width: 320, Height: 180, FPS: 30})

	video := moveInto(t, genVideo(t, VideoSpec{FPS: 25, ToneHz: 440, Dur: 2 * time.Second}), dir, "media/Take One.mp4")
	silent := moveInto(t, genVideo(t, VideoSpec{W: 240, H: 240, Dur: time.Second}), dir, "media/silent.mp4")
	tone := moveInto(t, genTone(t, 440, 1500*time.Millisecond), dir, "media/tone.wav")
	png := moveInto(t, genAlphaPNG(t, 64, 36, 0, 0, 255), dir, "img/logo.png")
	jpg := filepath.Join(dir, "img", "photo.jpg")
	runFFmpeg(t, "-f", "lavfi", "-i", "color=c=red:s=40x30:d=1", "-frames:v", "1", jpg)

	type want struct {
		id, kind, path string
		probe          project.Probe
	}
	cases := []struct {
		file string
		want want
	}{
		{video, want{"m_Take_One", project.KindVideo, "media/Take One.mp4",
			project.Probe{DurationMS: 2000, HasVideo: true, HasAudio: true, Width: 320, Height: 180, FPS: 25, SampleRate: 48000, Channels: 2}}},
		{silent, want{"m_silent", project.KindVideo, "media/silent.mp4",
			project.Probe{DurationMS: 1000, HasVideo: true, Width: 240, Height: 240, FPS: 30}}},
		{tone, want{"m_tone", project.KindAudio, "media/tone.wav",
			project.Probe{DurationMS: 1500, HasAudio: true, SampleRate: 48000, Channels: 2}}},
		{png, want{"m_logo", project.KindImage, "img/logo.png", project.Probe{Width: 64, Height: 36}}},
		{jpg, want{"m_photo", project.KindImage, "img/photo.jpg", project.Probe{Width: 40, Height: 30}}},
		{video, want{"m_Take_One_2", project.KindVideo, "media/Take One.mp4",
			project.Probe{DurationMS: 2000, HasVideo: true, HasAudio: true, Width: 320, Height: 180, FPS: 25, SampleRate: 48000, Channels: 2}}},
	}
	for _, tc := range cases {
		id, err := project.ImportMedia(p, tc.file)
		if err != nil {
			t.Fatalf("ImportMedia(%s): %v", tc.file, err)
		}
		m := p.MediaByID(id)
		got := want{id, m.Kind, m.Path, m.Probe}
		// AAC/MP4 durations are container-rounded; allow a frame.
		if d := got.probe.DurationMS - tc.want.probe.DurationMS; d >= -50 && d <= 50 {
			got.probe.DurationMS = tc.want.probe.DurationMS
		}
		if got != tc.want {
			t.Errorf("import %s:\n got %+v\nwant %+v", filepath.Base(tc.file), got, tc.want)
		}
		if m.Fingerprint == nil || m.Fingerprint.SizeBytes == nil || m.Fingerprint.ModifiedUnixMS == nil {
			t.Errorf("%s: no fingerprint", id)
		}
	}

	// The imported project saves, loads without warnings (files exist,
	// fingerprints match), and its media works on a timeline.
	p.Tracks = []project.Track{{ID: "t1", Kind: project.KindVideo, Clips: []project.Clip{
		{ID: "c1", Type: project.ClipVideo, Media: "m_Take_One"},
	}}}
	path := filepath.Join(dir, "p"+project.FileExtension)
	if err := project.Save(path, p); err != nil {
		t.Fatal(err)
	}
	q, warnings, err := project.Load(path)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Load: %v, warnings %v", err, warnings)
	}
	if _, err := project.Build(q); err != nil {
		t.Errorf("Build: %v", err)
	}

	// Touching a file makes its fingerprint stale.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(tone, later, later); err != nil {
		t.Fatal(err)
	}
	_, warnings, _ = project.Load(path)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, `media "m_tone": file changed since it was probed`) {
		t.Errorf("warnings after touching the file: %v", warnings)
	}
}

func TestProject_ImportMediaMissingFile(t *testing.T) {
	p := project.New(t.TempDir(), project.Format{Width: 320, Height: 180, FPS: 30})
	if _, err := project.ImportMedia(p, filepath.Join(p.Dir(), "missing.mov")); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("err = %v", err)
	}
}

// volume: 0 is an explicit value (muted), not "unset": it must render
// silent, not at the default volume 1, after a Save/Load round trip.
func TestProject_MutedClipsRenderSilent(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	p := project.New(dir, project.Format{Width: 320, Height: 180, FPS: 30})
	v, err := project.ImportMedia(p, moveInto(t, genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: 2 * time.Second}), dir, "v.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := project.ImportMedia(p, moveInto(t, genTone(t, 330, 2*time.Second), dir, "a.wav"))
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	p.Tracks = []project.Track{
		{ID: "t_v", Kind: project.KindVideo, Clips: []project.Clip{
			{ID: "c_muted", Type: project.ClipVideo, Media: v, OutMS: 1000, Volume: &zero},
			{ID: "c_loud", Type: project.ClipVideo, Media: v, AtMS: 1000, InMS: 1000},
		}},
		{ID: "t_music", Kind: project.KindAudio, Clips: []project.Clip{
			{ID: "c_music", Type: project.ClipAudio, Media: a, Volume: &zero},
		}},
	}
	path := filepath.Join(dir, "p"+project.FileExtension)
	if err := project.Save(path, p); err != nil {
		t.Fatal(err)
	}
	q, _, err := project.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	tl, err := project.Build(q)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp4")
	if err := tl.ExportSilent(testProfile(), out); err != nil {
		t.Fatal(err)
	}
	if l := audioLevel(t, out, 100*time.Millisecond, 800*time.Millisecond); l.MeanDB > -60 {
		t.Errorf("muted clip + muted music should be silent, got %.1f dB", l.MeanDB)
	}
	if l := audioLevel(t, out, 1100*time.Millisecond, 800*time.Millisecond); l.MeanDB < -40 {
		t.Errorf("un-muted clip should be audible, got %.1f dB", l.MeanDB)
	}
}
