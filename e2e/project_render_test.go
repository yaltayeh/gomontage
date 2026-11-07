//go:build e2e

package e2e

import (
	"bytes"
	"image"
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

	// A project that can't be rendered (a render blocker: the font file is
	// missing): nothing on stdout, problems on stderr.
	p.Tracks[0].Clips = append(p.Tracks[0].Clips, project.Clip{ID: "c2", Type: project.ClipText, Text: "x",
		AtMS: 1000, DurationMS: 500, Style: &project.TextStyle{Font: &project.Font{File: "fonts/missing.ttf"}}})
	project.Save(file, p)
	cmd = exec.Command(bin, "render", file)
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("expected render to fail")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `/tracks/0/clips/1/style/font/file: clip "c2": font file not found: fonts/missing.ttf`) {
		t.Errorf("stdout %q\nstderr %s", stdout.String(), stderr.String())
	}
}

// examples/reel-arabic-overlay.json — overlays, layout, Arabic text with a
// box and an outline, cover, music — has no render blockers and renders as
// the file describes (generated media at the example's paths).
func TestProject_RenderReelExample(t *testing.T) {
	font := testFont(t) // also makes the family "El Messiri" resolvable
	requireEncoder(t, "libmp3lame")
	path := copyExample(t, "reel-arabic-overlay.json")
	dir := filepath.Dir(path)
	// 9:16 street footage (green) with sound, a logo (opaque blue left half,
	// transparent right half), music, and the font file.
	moveInto(t, genVideo(t, VideoSpec{W: 216, H: 384, FPS: 30, Color: "0x00C000", ToneHz: 330, Dur: 17 * time.Second, FileName: "street.mov"}), dir, "media/street.mov")
	moveInto(t, genAlphaPNG(t, 80, 80, 0, 0, 255), dir, "brand/logo.png")
	music := filepath.Join(dir, "media", "music.mp3")
	runFFmpeg(t, "-f", "lavfi", "-i", "sine=f=550:sample_rate=44100:d=20", "-c:a", "libmp3lame", music)
	data, _ := os.ReadFile(font)
	os.MkdirAll(filepath.Join(dir, "fonts"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "fonts", "ElMessiri.ttf"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	r, p, err := project.CheckFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() {
		t.Fatalf("project errors %v, render blockers %v", r.ProjectErrors, r.RenderBlockers)
	}
	tl, err := project.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	reel, out, err := project.RenderTarget(p, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Fast encoder at the reel preset's size and fit.
	prof := testProfile()
	prof.Width, prof.Height, prof.Fit = reel.Width, reel.Height, reel.Fit
	os.MkdirAll(filepath.Dir(out), 0o755)
	if err := tl.ExportSilent(prof, out); err != nil {
		t.Fatal(err)
	}

	m := probe(t, out)
	v, a := m.Video(), m.Audio()
	if v == nil || a == nil || v.Width != 1080 || v.Height != 1920 {
		t.Fatalf("streams %+v", m.Streams)
	}
	assertDuration(t, "video", v.Duration, 12*time.Second, 30)

	// The footage fills the frame (size 1x1 cover on a 9:16 canvas).
	assertPixel(t, out, 5*time.Second, 540, 900, RGB{0, 192, 0}, colorTol)
	// Logo: 0.18 of the width (194 px), top-right anchor at (0.95, 0.04):
	// x 832-1026, y 77-271. Opaque left half blue; transparent right half
	// shows the footage.
	assertPixel(t, out, 5*time.Second, 870, 170, Blue, colorTol)
	assertPixel(t, out, 5*time.Second, 1000, 170, RGB{0, 192, 0}, colorTol)
	assertPixel(t, out, 5*time.Second, 790, 170, RGB{0, 192, 0}, colorTol) // left of the logo
	// Title "قريباً" 1.5-7.5 s: a translucent dark box whose bottom edge sits
	// at 0.85 of the height (1632 px), with white glyphs in it.
	frame := frameAt(t, out, 4*time.Second)
	boxColor := RGB{0, 64, 0} // #000000AA over green
	box := bbox(frame, boxColor, 30)
	if box.Empty() || abs(box.Max.Y-1632) > 3 || abs((box.Min.X+box.Max.X)/2-540) > 3 {
		t.Errorf("title box at %v, want bottom edge at y=1632, centred on x=540", box)
	}
	if n := countNear(frame, box, RGB{255, 255, 255}, 40); n < 500 {
		t.Errorf("title box contains only %d white glyph pixels", n)
	}
	// Outside the title's time the box is gone.
	if b := bbox(frameAt(t, out, 7900*time.Millisecond), boxColor, 30); !b.Empty() {
		t.Errorf("title box still visible at 7.9 s: %v", b)
	}
	// Date text 8-12 s: white glyphs with a black outline around the centre.
	frame = frameAt(t, out, 10*time.Second)
	centre := image.Rect(240, 840, 840, 1080)
	if countNear(frame, centre, RGB{255, 255, 255}, 40) < 300 || countNear(frame, centre, Black, 40) < 300 {
		t.Error("date text (white with black outline) not found at the centre")
	}
	// Music (0.6 volume, faded) is audible; the footage's own sound is off.
	if l := audioLevel(t, out, 5*time.Second, time.Second); l.MeanDB < -40 {
		t.Errorf("music not audible: %.1f dB", l.MeanDB)
	}
}

// examples/effects-transitions.json — speed effects, crossfades on the main
// track and the music track, fades, normalize — has no render blockers and
// renders: generated media at the example's paths (small, with the probed
// durations), each source a solid color so the transitions can be seen.
func TestProject_RenderTransitionsExample(t *testing.T) {
	requireFilter(t, "loudnorm")
	path := copyExample(t, "effects-transitions.json")
	dir := filepath.Dir(path)
	moveInto(t, genVideo(t, VideoSpec{FPS: 25, Color: "0xFF0000", ToneHz: 330, Dur: 60 * time.Second, FileName: "drive.mp4"}), dir, "clips/drive.mp4")
	moveInto(t, genVideo(t, VideoSpec{FPS: 50, Color: "0x0000FF", ToneHz: 660, Dur: 20 * time.Second, FileName: "beach.mp4"}), dir, "clips/beach.mp4")
	moveInto(t, genVideo(t, VideoSpec{FPS: 24, Color: "0x00FF00", Dur: 15 * time.Second, FileName: "sunset.mov"}), dir, "clips/sunset.mov")
	song := filepath.Join(dir, "audio", "song.flac")
	os.MkdirAll(filepath.Dir(song), 0o755)
	runFFmpeg(t, "-f", "lavfi", "-i", "sine=f=440:sample_rate=48000:d=180", "-ac", "2", "-c:a", "flac", song)

	r, p, err := project.CheckFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() {
		t.Fatalf("project errors %v, render blockers %v", r.ProjectErrors, r.RenderBlockers)
	}
	tl, err := project.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "travel.mp4")
	prof := testProfile()
	prof.Width, prof.Height = 320, 180
	if err := tl.ExportSilent(prof, out); err != nil {
		t.Fatal(err)
	}
	m := probe(t, out)
	assertDuration(t, "video", m.Video().Duration, 27*time.Second, 25)
	assertDuration(t, "audio", m.Audio().Duration, 27*time.Second, 25)

	// Drive (red, 4x) 1-11 s, beach (blue) 11-19 s, sunset (green) 19-27 s;
	// crossfades 10.5-11.5 s and 18.5-19.5 s.
	assertPixel(t, out, 10300*time.Millisecond, cx, cy, Red, colorTol)
	assertPixel(t, out, 11*time.Second, cx, cy, RGB{128, 0, 128}, colorTol)
	assertPixel(t, out, 11700*time.Millisecond, cx, cy, Blue, colorTol)
	assertPixel(t, out, 19*time.Second, cx, cy, RGB{0, 128, 128}, colorTol)
	assertPixel(t, out, 20*time.Second, cx, cy, Green, colorTol)
	// The music plays throughout, also across its 2 s crossfade at 15 s.
	for _, at := range []time.Duration{5 * time.Second, 15 * time.Second, 22 * time.Second} {
		if l := audioLevel(t, out, at, 500*time.Millisecond); l.MeanDB < -40 {
			t.Errorf("no sound at %s: %.1f dB", at, l.MeanDB)
		}
	}
}
