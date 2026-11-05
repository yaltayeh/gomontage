//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/export"
	"github.com/ahmedhodiani/gomontage/project"
)

// copyExample copies examples/<name> into a new temp dir and returns its path.
func copyExample(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// The example's media are replaced by small generated files at the same
// paths. Build uses the probe data stored in the project (no re-probing), so
// only the source windows the clips use have to exist.
func TestProject_RenderSimpleTrimConcat(t *testing.T) {
	requireFFmpeg(t)
	path := copyExample(t, "simple-trim-concat.json")
	dir := filepath.Dir(path)
	moveInto(t, genVideo(t, VideoSpec{FPS: 30, ToneHz: 440, Dur: 6 * time.Second}), dir, "media/intro.mp4")
	moveInto(t, genVideo(t, VideoSpec{FPS: 60, ToneHz: 660, Dur: 120 * time.Second, FileName: "interview.mov"}), dir, "media/interview.mov")
	moveInto(t, genVideo(t, VideoSpec{FPS: 30, Color: "0x0000FF", Dur: 4 * time.Second}), dir, "media/outro.mp4")

	p, warnings, err := project.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// The intro's fingerprint in the example describes the original file.
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, `media "m_intro": file changed since it was probed`) {
		t.Errorf("warnings = %v", warnings)
	}
	tl, err := project.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	// Render with the fast test profile at the canvas size (the example's
	// youtube_1080p preset has the same 1920x1080 size; its H.264 settings
	// are exercised by the CLI test below).
	out := filepath.Join(dir, "out.mp4")
	if err := tl.ExportSilent(testProfile(), out); err != nil {
		t.Fatal(err)
	}
	m := probe(t, out)
	v, a := m.Video(), m.Audio()
	if v == nil || a == nil || len(m.Streams) != 2 {
		t.Fatalf("streams: %+v", m.Streams)
	}
	if v.Width != 1920 || v.Height != 1080 || v.FPS < 29.9 || v.FPS > 30.1 {
		t.Errorf("video %dx%d @ %.2f fps, want 1920x1080 @ 30", v.Width, v.Height, v.FPS)
	}
	assertDuration(t, "video", v.Duration, 48250*time.Millisecond, 30)
	assertDuration(t, "audio", a.Duration, 48250*time.Millisecond, 30)
	// The outro (44.25s-48.25s) is the blue clip without sound.
	assertPixel(t, out, 46*time.Second, 960, 540, Blue, colorTol)
	if l := audioLevel(t, out, 45*time.Second, time.Second); l.MeanDB > -60 {
		t.Errorf("outro should be silent, got %.1f dB", l.MeanDB)
	}
	if l := audioLevel(t, out, 10*time.Second, time.Second); l.MeanDB < -40 {
		t.Errorf("interview should have sound, got %.1f dB", l.MeanDB)
	}
}

func TestProject_RenderPodcast(t *testing.T) {
	requireFFmpeg(t)
	path := copyExample(t, "podcast-audio.json")
	dir := filepath.Dir(path)
	// Compact sources: 8 kHz is enough for a level check; the project's
	// stored probe (48 kHz) is informational.
	moveInto(t, genTone(t, 440, 8*time.Second), dir, "audio/jingle.wav")
	host := filepath.Join(dir, "audio", "host-take2.wav")
	os.MkdirAll(filepath.Dir(host), 0o755)
	runFFmpeg(t, "-f", "lavfi", "-i", "sine=f=300:sample_rate=8000:d=1210", "-c:a", "pcm_s16le", host)
	guest := filepath.Join(dir, "recordings", "guest-call.mp4")
	os.MkdirAll(filepath.Dir(guest), 0o755)
	runFFmpeg(t, "-f", "lavfi", "-i", "color=c=blue:s=64x36:r=5:d=1205", "-f", "lavfi", "-i", "sine=f=500:sample_rate=8000:d=1205",
		"-c:v", "mpeg4", "-c:a", "aac", "-b:a", "32k", "-shortest", guest)
	// audio/music-bed.mp3 is only used by the disabled track: not needed.

	p, warnings, err := project.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].String(), `media "m_bed": file not found`) {
		t.Errorf("warnings = %v", warnings)
	}
	tl, err := project.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	profile, out, err := project.RenderTarget(p, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != export.WAV().Name || out != filepath.Join(dir, "output", "episode-12.wav") {
		t.Fatalf("render target %s %s", profile.Name, out)
	}
	if tl.Duration() != 1216*time.Second {
		t.Errorf("timeline duration %s, want 20m16s", tl.Duration())
	}
	os.MkdirAll(filepath.Dir(out), 0o755)

	// The full 20-minute render takes about a minute (loudnorm resamples to
	// 192 kHz internally), so by default only the first and last 30 s are
	// rendered: opening jingle + both voices, closing jingle. Set
	// GOMONTAGE_E2E_LONG=1 (make test-e2e-long) to render everything.
	checkWAV := func(file string, dur time.Duration, soundAt ...time.Duration) {
		t.Helper()
		m := probe(t, file)
		a := m.Audio()
		if a == nil || len(m.Streams) != 1 || a.Codec != "pcm_s16le" || a.SampleRate != 48000 || a.Channels != 2 {
			t.Fatalf("streams: %+v", m.Streams)
		}
		assertDuration(t, "audio", m.Duration, dur, 30)
		for _, at := range soundAt {
			if l := audioLevel(t, file, at, time.Second); l.MeanDB < -40 {
				t.Errorf("%s: expected sound at %s, got %.1f dB", filepath.Base(file), at, l.MeanDB)
			}
		}
	}
	if os.Getenv("GOMONTAGE_E2E_LONG") == "" {
		head := filepath.Join(dir, "output", "head.wav")
		if err := tl.ExportRangeSilent(0, 30*time.Second, profile, head); err != nil {
			t.Fatal(err)
		}
		checkWAV(head, 30*time.Second, 2*time.Second, 20*time.Second)
		tail := filepath.Join(dir, "output", "tail.wav")
		if err := tl.ExportRangeSilent(1186*time.Second, 1216*time.Second, profile, tail); err != nil {
			t.Fatal(err)
		}
		checkWAV(tail, 30*time.Second, 10*time.Second, 25*time.Second)
		return
	}
	start := time.Now()
	if err := tl.ExportSilent(profile, out); err != nil {
		t.Fatal(err)
	}
	t.Logf("rendered 20m16s of audio in %s", time.Since(start).Round(time.Millisecond))
	checkWAV(out, 1216*time.Second, 2*time.Second, 600*time.Second, 1210*time.Second)
}

// The CLI renders a project with its preset and prints only the output
// path on stdout.
func TestProject_CLIRender(t *testing.T) {
	requireFFmpeg(t)
	requireEncoder(t, "libx264")
	dir := t.TempDir()
	bin := filepath.Join(dir, "gomontage")
	build := exec.Command("go", "build", "-o", bin, "../cmd/gomontage")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	p := project.New(dir, project.Format{Width: 320, Height: 180, FPS: 30})
	src := moveInto(t, genVideo(t, VideoSpec{Color: "0xFF0000", ToneHz: 440, Dur: 2 * time.Second}), dir, "media/clip.mp4")
	id, err := project.ImportMedia(p, src)
	if err != nil {
		t.Fatal(err)
	}
	p.Tracks = []project.Track{{ID: "t1", Kind: project.KindVideo, Clips: []project.Clip{
		{ID: "c1", Type: project.ClipVideo, Media: id, InMS: 500, OutMS: 1500, FadeInMS: 200},
	}}}
	p.Output = &project.Output{Preset: "youtube_720p", Path: "renders/final.mp4"}
	file := filepath.Join(dir, "p"+project.FileExtension)
	if err := project.Save(file, p); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "render", file)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("render: %v\nstderr:\n%s", err, stderr.String())
	}
	want := filepath.Join(dir, "renders", "final.mp4")
	if stdout.String() != want+"\n" {
		t.Errorf("stdout = %q, want only the output path", stdout.String())
	}
	m := probe(t, want)
	if v := m.Video(); v == nil || v.Codec != "h264" || v.Width != 1280 || v.Height != 720 {
		t.Errorf("video: %+v", v)
	}
	assertDuration(t, "video", m.Video().Duration, time.Second, 30)

	// -o and --preset override the project's output section.
	cmd = exec.Command(bin, "render", file, "-o", filepath.Join(dir, "sound.wav"), "--preset", "wav")
	stdout.Reset()
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("render -o: %v\n%s", err, stderr.String())
	}
	if m := probe(t, filepath.Join(dir, "sound.wav")); len(m.Streams) != 1 || m.Audio() == nil {
		t.Errorf("wav override: %+v", m.Streams)
	}

	// A project that can't be rendered: nothing on stdout, problems on stderr.
	p.Tracks[0].Clips[0].TransitionIn = nil
	p.Tracks[0].Clips = append(p.Tracks[0].Clips, project.Clip{ID: "c2", Type: project.ClipText, Text: "hi", AtMS: 1000, DurationMS: 500})
	project.Save(file, p)
	cmd = exec.Command(bin, "render", file)
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("expected render to fail")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `/tracks/0/clips/1: clip "c2": text clips are not supported yet`) {
		t.Errorf("stdout %q\nstderr %s", stdout.String(), stderr.String())
	}
}
