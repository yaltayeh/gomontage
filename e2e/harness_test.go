//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/export"
	"github.com/ahmedhodiani/gomontage/timeline"
)

// knownBugsEnv enables tests that assert the correct behavior for bugs that
// are known to exist today. They are skipped by default so the suite is green.
const knownBugsEnv = "GOMONTAGE_KNOWN_BUGS"

// ---------------------------------------------------------------------------
// Environment / capability checks
// ---------------------------------------------------------------------------

// requireFFmpeg skips the test if ffmpeg or ffprobe is not on PATH.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("e2e: %s not found on PATH — install FFmpeg to run end-to-end tests", bin)
		}
	}
}

// knownBug skips the test unless GOMONTAGE_KNOWN_BUGS is set. Call it first
// in every TestKnownBug_* test.
func knownBug(t *testing.T, n int, desc string) {
	t.Helper()
	if os.Getenv(knownBugsEnv) == "" {
		t.Skipf("known bug #%d: %s (set %s=1 to run)", n, desc, knownBugsEnv)
	}
	requireFFmpeg(t)
}

var (
	capsOnce sync.Once
	encoders string
	filters  string
)

func loadCaps() {
	capsOnce.Do(func() {
		if out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output(); err == nil {
			encoders = string(out)
		}
		if out, err := exec.Command("ffmpeg", "-hide_banner", "-filters").Output(); err == nil {
			filters = string(out)
		}
	})
}

func capListHas(list, name string) bool {
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == name {
			return true
		}
	}
	return false
}

// hasEncoder reports whether the local ffmpeg build has the named encoder.
func hasEncoder(name string) bool { loadCaps(); return capListHas(encoders, name) }

// hasFilter reports whether the local ffmpeg build has the named filter.
func hasFilter(name string) bool { loadCaps(); return capListHas(filters, name) }

func requireEncoder(t *testing.T, name string) {
	t.Helper()
	if !hasEncoder(name) {
		t.Skipf("e2e: local ffmpeg has no %q encoder (rebuild ffmpeg with it to run this test)", name)
	}
}

func requireFilter(t *testing.T, name string) {
	t.Helper()
	if !hasFilter(name) {
		t.Skipf("e2e: local ffmpeg has no %q filter (rebuild ffmpeg with it to run this test)", name)
	}
}

// ---------------------------------------------------------------------------
// Running ffmpeg
// ---------------------------------------------------------------------------

func runFFmpeg(t *testing.T, args ...string) []byte {
	t.Helper()
	full := append([]string{"-hide_banner", "-nostdin", "-y"}, args...)
	cmd := exec.Command("ffmpeg", full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffmpeg %s\nerror: %v\nstderr:\n%s", strings.Join(full, " "), err, tail(stderr.String(), 30))
	}
	return out
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Rendering through GoMontage
// ---------------------------------------------------------------------------

// testProfile returns an MP4 profile that only uses encoders built into every
// FFmpeg (mpeg4 + native aac), so the suite runs on minimal FFmpeg builds.
// High quality (q:v 2) keeps pixel colors close to the source.
func testProfile() *export.Profile {
	return export.NewProfile().
		WithName("e2e mpeg4").
		WithCodec("mpeg4").
		WithExtra("-q:v", "2").
		WithPixelFormat("yuv420p").
		WithAudioCodec("aac").
		WithAudioBitrate("192k").
		WithAudioSampleRate(48000).
		WithAudioChannels(2).
		Build()
}

// render exports tl with the given profile into a fresh temp file and returns
// its path. Fails the test on export error.
func render(t *testing.T, tl *timeline.Timeline, profile *export.Profile, ext string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out"+ext)
	if err := tl.ExportSilent(profile, out); err != nil {
		t.Fatalf("export failed: %v", err)
	}
	return out
}

// renderMP4 renders with testProfile into an .mp4.
func renderMP4(t *testing.T, tl *timeline.Timeline) string {
	t.Helper()
	return render(t, tl, testProfile(), ".mp4")
}

// ---------------------------------------------------------------------------
// Fixtures (generated with lavfi; nothing binary is committed)
// ---------------------------------------------------------------------------

// fixtureDir returns a per-test directory for fixtures.
func fixtureDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// VideoSpec describes a generated video fixture.
type VideoSpec struct {
	W, H     int
	FPS      int
	Dur      time.Duration
	Color    string // hex like "0xFF0000"; empty → testsrc2 pattern
	ToneHz   int    // audio sine frequency; 0 → no audio stream
	FileName string // optional; defaults to an auto name
}

func secs(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }

// genVideo generates a video fixture (mpeg4 in mp4, optional aac audio).
func genVideo(t *testing.T, s VideoSpec) string {
	t.Helper()
	if s.W == 0 {
		s.W, s.H = 320, 180
	}
	if s.FPS == 0 {
		s.FPS = 30
	}
	if s.Dur == 0 {
		s.Dur = 2 * time.Second
	}
	name := s.FileName
	if name == "" {
		name = fmt.Sprintf("v_%dx%d_%dfps_%s_%s_%dhz.mp4", s.W, s.H, s.FPS, secs(s.Dur), strings.TrimPrefix(s.Color, "0x"), s.ToneHz)
	}
	out := filepath.Join(fixtureDir(t), name)

	var src string
	if s.Color == "" {
		src = fmt.Sprintf("testsrc2=s=%dx%d:r=%d:d=%s", s.W, s.H, s.FPS, secs(s.Dur))
	} else {
		src = fmt.Sprintf("color=c=%s:s=%dx%d:r=%d:d=%s", s.Color, s.W, s.H, s.FPS, secs(s.Dur))
	}
	args := []string{"-f", "lavfi", "-i", src}
	if s.ToneHz > 0 {
		args = append(args, "-f", "lavfi", "-i",
			fmt.Sprintf("sine=f=%d:sample_rate=48000:d=%s", s.ToneHz, secs(s.Dur)))
	}
	args = append(args, "-c:v", "mpeg4", "-q:v", "2", "-pix_fmt", "yuv420p")
	if s.ToneHz > 0 {
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2", "-shortest")
	}
	args = append(args, out)
	runFFmpeg(t, args...)
	return out
}

// genColorSequence generates a video that is one solid color per second,
// in order, e.g. ["0xFF0000","0x00FF00","0x0000FF"] → red/green/blue thirds.
func genColorSequence(t *testing.T, w, h, fps int, colors []string) string {
	t.Helper()
	out := filepath.Join(fixtureDir(t), "colorseq.mp4")
	var parts, labels []string
	for i, c := range colors {
		parts = append(parts, fmt.Sprintf("color=c=%s:s=%dx%d:r=%d:d=1[c%d]", c, w, h, fps, i))
		labels = append(labels, fmt.Sprintf("[c%d]", i))
	}
	fc := strings.Join(parts, ";") + ";" + strings.Join(labels, "") +
		fmt.Sprintf("concat=n=%d:v=1:a=0[v]", len(colors))
	runFFmpeg(t, "-filter_complex", fc, "-map", "[v]",
		"-c:v", "mpeg4", "-q:v", "2", "-pix_fmt", "yuv420p", out)
	return out
}

// genTone generates a stereo 48kHz PCM WAV sine tone.
func genTone(t *testing.T, hz int, dur time.Duration) string {
	t.Helper()
	out := filepath.Join(fixtureDir(t), fmt.Sprintf("tone_%dhz_%s.wav", hz, secs(dur)))
	runFFmpeg(t, "-f", "lavfi", "-i",
		fmt.Sprintf("sine=f=%d:sample_rate=48000:d=%s", hz, secs(dur)),
		"-ac", "2", "-c:a", "pcm_s16le", out)
	return out
}

// genAlphaPNG generates a w×h RGBA PNG whose left half is opaque (r,g,b) and
// whose right half is fully transparent.
func genAlphaPNG(t *testing.T, w, h int, r, g, b int) string {
	t.Helper()
	out := filepath.Join(fixtureDir(t), fmt.Sprintf("alpha_%dx%d.png", w, h))
	src := fmt.Sprintf("color=c=black:s=%dx%d:d=1,format=rgba,geq=r=%d:g=%d:b=%d:a='if(lt(X,W/2),255,0)'",
		w, h, r, g, b)
	runFFmpeg(t, "-f", "lavfi", "-i", src, "-frames:v", "1", "-c:v", "png", "-pix_fmt", "rgba", out)
	return out
}

// genOpaquePNG generates a fully opaque solid-color w×h PNG.
func genOpaquePNG(t *testing.T, w, h int, color string) string {
	t.Helper()
	out := filepath.Join(fixtureDir(t), fmt.Sprintf("solid_%dx%d_%s.png", w, h, strings.TrimPrefix(color, "0x")))
	runFFmpeg(t, "-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:s=%dx%d:d=1", color, w, h),
		"-frames:v", "1", "-c:v", "png", "-pix_fmt", "rgb24", out)
	return out
}

// ---------------------------------------------------------------------------
// Probing (independent of engine.Probe, so we don't trust the code under test)
// ---------------------------------------------------------------------------

// StreamInfo is a subset of ffprobe stream output.
type StreamInfo struct {
	Type       string
	Codec      string
	Width      int
	Height     int
	FPS        float64 // avg_frame_rate
	Duration   time.Duration
	SampleRate int
	Channels   int
	Frames     int
}

// MediaInfo is a subset of ffprobe output.
type MediaInfo struct {
	Duration time.Duration
	Format   string
	Streams  []StreamInfo
}

func (m MediaInfo) Video() *StreamInfo { return m.first("video") }
func (m MediaInfo) Audio() *StreamInfo { return m.first("audio") }

func (m MediaInfo) first(kind string) *StreamInfo {
	for i := range m.Streams {
		if m.Streams[i].Type == kind {
			return &m.Streams[i]
		}
	}
	return nil
}

func (m MediaInfo) count(kind string) int {
	n := 0
	for _, s := range m.Streams {
		if s.Type == kind {
			n++
		}
	}
	return n
}

func probe(t *testing.T, path string) MediaInfo {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", "-count_frames", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var raw struct {
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			CodecType    string `json:"codec_type"`
			CodecName    string `json:"codec_name"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			AvgFrameRate string `json:"avg_frame_rate"`
			Duration     string `json:"duration"`
			SampleRate   string `json:"sample_rate"`
			Channels     int    `json:"channels"`
			NbReadFrames string `json:"nb_read_frames"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("parse ffprobe json: %v", err)
	}
	info := MediaInfo{Duration: parseSecs(raw.Format.Duration), Format: raw.Format.FormatName}
	for _, s := range raw.Streams {
		si := StreamInfo{
			Type: s.CodecType, Codec: s.CodecName, Width: s.Width, Height: s.Height,
			FPS: parseFrac(s.AvgFrameRate), Duration: parseSecs(s.Duration), Channels: s.Channels,
		}
		si.SampleRate, _ = strconv.Atoi(s.SampleRate)
		si.Frames, _ = strconv.Atoi(s.NbReadFrames)
		info.Streams = append(info.Streams, si)
	}
	return info
}

func parseSecs(s string) time.Duration {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

func parseFrac(s string) float64 {
	var a, b float64
	if n, _ := fmt.Sscanf(s, "%f/%f", &a, &b); n == 2 && b != 0 {
		return a / b
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// ---------------------------------------------------------------------------
// Frame / pixel helpers
// ---------------------------------------------------------------------------

// RGB is an 8-bit color.
type RGB struct{ R, G, B int }

func (c RGB) String() string { return fmt.Sprintf("rgb(%d,%d,%d)", c.R, c.G, c.B) }

var (
	Red   = RGB{255, 0, 0}
	Green = RGB{0, 255, 0}
	Blue  = RGB{0, 0, 255}
	Black = RGB{0, 0, 0}
)

// colorTol is the default per-channel tolerance for pixel comparisons
// (covers yuv420p round-trips and mpeg4 quantization).
const colorTol = 24

// frameAt decodes the frame at time ts from path.
func frameAt(t *testing.T, path string, ts time.Duration) image.Image {
	t.Helper()
	out := runFFmpeg(t, "-v", "error", "-ss", secs(ts), "-i", path,
		"-frames:v", "1", "-f", "image2pipe", "-c:v", "png", "-pix_fmt", "rgb24", "-")
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode frame at %s from %s: %v", ts, path, err)
	}
	return img
}

// pixelAt returns the RGB value at (x,y) of the frame at ts.
func pixelAt(t *testing.T, path string, ts time.Duration, x, y int) RGB {
	t.Helper()
	img := frameAt(t, path, ts)
	r, g, b, _ := img.At(x, y).RGBA()
	return RGB{int(r >> 8), int(g >> 8), int(b >> 8)}
}

func colorClose(a, b RGB, tol int) bool {
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	return abs(a.R-b.R) <= tol && abs(a.G-b.G) <= tol && abs(a.B-b.B) <= tol
}

// assertPixel checks the pixel at (x,y) of the frame at ts is want ± tol.
func assertPixel(t *testing.T, path string, ts time.Duration, x, y int, want RGB, tol int) {
	t.Helper()
	got := pixelAt(t, path, ts, x, y)
	if !colorClose(got, want, tol) {
		t.Errorf("pixel at t=%s (%d,%d) = %s, want %s ±%d", ts, x, y, got, want, tol)
	}
}

// ---------------------------------------------------------------------------
// Audio helpers
// ---------------------------------------------------------------------------

var (
	meanVolRe = regexp.MustCompile(`mean_volume:\s*(-?[\d.]+|-inf) dB`)
	maxVolRe  = regexp.MustCompile(`max_volume:\s*(-?[\d.]+|-inf) dB`)
)

// AudioLevel is the result of volumedetect over a window.
type AudioLevel struct{ MeanDB, MaxDB float64 }

// silenceDB is what we report for digital silence / no audio in the window.
const silenceDB = -120.0

// audioLevel measures the first audio stream of path over [start, start+dur).
func audioLevel(t *testing.T, path string, start, dur time.Duration) AudioLevel {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-ss", secs(start), "-t", secs(dur),
		"-i", path, "-map", "0:a:0", "-af", "volumedetect", "-f", "null", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("volumedetect on %s: %v\n%s", path, err, tail(stderr.String(), 20))
	}
	parse := func(re *regexp.Regexp) float64 {
		m := re.FindStringSubmatch(stderr.String())
		if m == nil || m[1] == "-inf" {
			return silenceDB
		}
		v, _ := strconv.ParseFloat(m[1], 64)
		if v <= -91 { // volumedetect floor for pure zeros
			return silenceDB
		}
		return v
	}
	return AudioLevel{MeanDB: parse(meanVolRe), MaxDB: parse(maxVolRe)}
}

// ---------------------------------------------------------------------------
// Duration helpers
// ---------------------------------------------------------------------------

// assertDuration checks got ≈ want within 1.5 frames at fps (min 40ms, to
// absorb AAC priming / container rounding).
func assertDuration(t *testing.T, what string, got, want time.Duration, fps float64) {
	t.Helper()
	tol := time.Duration(1.5 / fps * float64(time.Second))
	if tol < 40*time.Millisecond {
		tol = 40 * time.Millisecond
	}
	if math.Abs(float64(got-want)) > float64(tol) {
		t.Errorf("%s duration = %s, want %s ±%s", what, got, want, tol)
	}
}
