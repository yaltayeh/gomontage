//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ahmedhodiani/gomontage/mcpserver"
)

// mcpSession connects an in-memory MCP client to a new GoMontage server.
func mcpSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := mcpserver.New(mcpserver.Options{})
	st, ct := mcp.NewInMemoryTransports()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Run(ctx, st)
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		cancel()
		<-done // Run cancels running renders before returning
	})
	return cs
}

type toolResult struct {
	text   string
	images [][]byte
	isErr  bool
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) toolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var r toolResult
	r.isErr = res.IsError
	var texts []string
	for _, c := range res.Content {
		switch c := c.(type) {
		case *mcp.TextContent:
			texts = append(texts, c.Text)
		case *mcp.ImageContent:
			if c.MIMEType != "image/jpeg" {
				t.Errorf("%s: image MIME type %q", name, c.MIMEType)
			}
			r.images = append(r.images, c.Data)
		}
	}
	r.text = strings.Join(texts, "\n")
	return r
}

func mustTool(t *testing.T, cs *mcp.ClientSession, name string, args any) toolResult {
	t.Helper()
	r := callTool(t, cs, name, args)
	if r.isErr {
		t.Fatalf("%s failed:\n%s", name, r.text)
	}
	return r
}

func opsJSON(t *testing.T, s string) []any {
	t.Helper()
	var ops []any
	if err := json.Unmarshal([]byte(s), &ops); err != nil {
		t.Fatalf("ops JSON: %v", err)
	}
	return ops
}

var jobIDRE = regexp.MustCompile(`job (r\d+) started`)

func startedJob(t *testing.T, r toolResult) string {
	t.Helper()
	m := jobIDRE.FindStringSubmatch(r.text)
	if m == nil {
		t.Fatalf("no job ID in: %s", r.text)
	}
	return m[1]
}

func jpegPixel(t *testing.T, img image.Image, x, y int) RGB {
	t.Helper()
	r, g, b, _ := img.At(x, y).RGBA()
	return RGB{int(r >> 8), int(g >> 8), int(b >> 8)}
}

// redBlueProject creates a project (640x360) with a red clip, a blue clip
// joined by a 1000 ms crossfade at 2000 ms, and a tone on an audio track,
// through the MCP tools. It returns the project path.
func redBlueProject(t *testing.T, cs *mcp.ClientSession) string {
	t.Helper()
	dir := t.TempDir()
	red := genVideo(t, VideoSpec{W: 320, H: 180, Dur: 3 * time.Second, Color: "0xFF0000", ToneHz: 440, FileName: "red.mp4"})
	blue := genVideo(t, VideoSpec{W: 320, H: 180, Dur: 3 * time.Second, Color: "0x0000FF", ToneHz: 880, FileName: "blue.mp4"})
	tone := genTone(t, 220, 5*time.Second)
	path := filepath.Join(dir, "rb.gomontage.json")

	r := mustTool(t, cs, "project_create", map[string]any{"path": path, "width": 640, "height": 360, "fps": 30, "output_preset": "youtube_720p"})
	if !strings.Contains(r.text, "format 640x360 @ 30fps") {
		t.Fatalf("create: %s", r.text)
	}
	r = mustTool(t, cs, "media_import", map[string]any{"project": path, "paths": []string{red, blue, tone}})
	for _, w := range []string{"imported 3, failed 0", "+ m_red · video · 3000ms 320x180 30fps · +audio", "+ m_blue", "+ m_tone_220hz_5.000 · audio · 5000ms 48000Hz 2ch"} {
		if !strings.Contains(r.text, w) {
			t.Errorf("import result lacks %q:\n%s", w, r.text)
		}
	}
	r = mustTool(t, cs, "timeline_edit", map[string]any{"project": path, "ops": opsJSON(t, `[
		{"op": "add_track", "kind": "video", "name": "main", "new_id": "v1"},
		{"op": "add_track", "kind": "audio", "name": "music", "new_id": "a1"},
		{"op": "add_clip", "track": "v1", "new_id": "c_red", "clip": {"type": "video", "media": "m_red", "at_ms": 0, "out_ms": 2000}},
		{"op": "add_clip", "track": "v1", "new_id": "c_blue", "clip": {"type": "video", "media": "m_blue", "at_ms": 2000, "in_ms": 1000, "out_ms": 3000,
			"transition_in": {"type": "crossfade", "duration_ms": 1000}}},
		{"op": "add_clip", "track": "a1", "clip": {"type": "audio", "media": "m_tone_220hz_5.000", "at_ms": 0, "out_ms": 4000, "volume": 0.5}}
	]`)})
	if !strings.Contains(r.text, "saved · 5 ops · duration 4000 ms") || !strings.Contains(r.text, "check: ok (renderable)") {
		t.Fatalf("edit: %s", r.text)
	}
	return path
}

// The whole loop through the MCP tools: create, import, edit, inspect,
// preview, render, follow the job; the output is checked with ffprobe.
func TestMCP_FullLoop(t *testing.T) {
	requireFFmpeg(t)
	requireEncoder(t, "libx264")
	cs := mcpSession(t)
	path := redBlueProject(t, cs)

	r := mustTool(t, cs, "timeline_get", map[string]any{"project": path})
	for _, w := range []string{
		"c_red · video m_red · 0–2000 · src 0–2000",
		"c_blue · video m_blue · 2000–4000 · src 1000–3000 · crossfade_in 1000",
		"vol 0.5", "check: ok (renderable)",
	} {
		if !strings.Contains(r.text, w) {
			t.Errorf("timeline_get lacks %q:\n%s", w, r.text)
		}
	}

	// Mid-crossfade: half red, half blue.
	r = mustTool(t, cs, "preview_frame", map[string]any{"project": path, "time_ms": 2000})
	if len(r.images) != 1 {
		t.Fatalf("preview returned %d images", len(r.images))
	}
	img, err := jpeg.Decode(bytes.NewReader(r.images[0]))
	if err != nil {
		t.Fatalf("preview is not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 480 || b.Dy() != 270 {
		t.Errorf("preview size %v, want 480x270", b.Size())
	}
	if got := jpegPixel(t, img, 240, 135); !colorClose(got, RGB{128, 0, 128}, 30) {
		t.Errorf("mid-crossfade pixel %s, want about rgb(128,0,128)", got)
	}
	if strings.Contains(r.text, "cached") {
		t.Error("first preview reported as cached")
	}

	job := startedJob(t, mustTool(t, cs, "render", map[string]any{"project": path}))
	r = mustTool(t, cs, "render_job", map[string]any{"job": job, "wait_ms": 60000})
	if !strings.Contains(r.text, job+" done · 100%") {
		t.Fatalf("render_job: %s", r.text)
	}
	out := filepath.Join(filepath.Dir(path), "output", "rb.mp4")
	info := probe(t, out)
	assertDuration(t, "rendered output", info.Duration, 4*time.Second, 30)
	if v := info.Video(); v == nil || v.Width != 1280 || v.Height != 720 || v.Codec != "h264" {
		t.Errorf("video stream %+v, want h264 1280x720", v)
	}
	if info.Audio() == nil {
		t.Error("rendered output has no audio")
	}
	assertPixel(t, out, 1*time.Second, 640, 360, Red, colorTol)
	assertPixel(t, out, 3500*time.Millisecond, 640, 360, Blue, colorTol)
	if entries, _ := os.ReadDir(filepath.Dir(out)); len(entries) != 1 {
		t.Errorf("output dir holds %d files, want only the render (no temp files left)", len(entries))
	}
}

// preview_frame: right width, valid JPEG, exact frame next to fades, and a
// cache hit for an unchanged project; an edit invalidates the cache.
func TestMCP_PreviewFrame(t *testing.T) {
	requireFFmpeg(t)
	cs := mcpSession(t)
	path := redBlueProject(t, cs)

	r := mustTool(t, cs, "preview_frame", map[string]any{"project": path, "time_ms": 500, "width": 320})
	img, err := jpeg.Decode(bytes.NewReader(r.images[0]))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 320 || b.Dy() != 180 {
		t.Errorf("size %v, want 320x180", b.Size())
	}
	if got := jpegPixel(t, img, 160, 90); !colorClose(got, Red, colorTol) {
		t.Errorf("pixel %s, want red", got)
	}
	start := time.Now()
	r2 := mustTool(t, cs, "preview_frame", map[string]any{"project": path, "time_ms": 500, "width": 320})
	if !strings.Contains(r2.text, "cached") || !bytes.Equal(r2.images[0], r.images[0]) {
		t.Errorf("repeat preview not served from the cache: %s", r2.text)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("cached preview took %v", d)
	}

	// A fade-in on c_red: at 500 ms of a 1000 ms fade the frame is half
	// dark. The preview window starts at the clip start so SubRange doesn't
	// restart the fade at 500 ms.
	mustTool(t, cs, "timeline_edit", map[string]any{"project": path, "ops": opsJSON(t, `[{"op": "update", "id": "c_red", "set": {"fade_in_ms": 1000}}]`)})
	r3 := mustTool(t, cs, "preview_frame", map[string]any{"project": path, "time_ms": 500, "width": 320})
	if strings.Contains(r3.text, "cached") {
		t.Error("preview after an edit served from the cache")
	}
	img, _ = jpeg.Decode(bytes.NewReader(r3.images[0]))
	if got := jpegPixel(t, img, 160, 90); !colorClose(got, RGB{128, 0, 0}, 30) {
		t.Errorf("mid-fade pixel %s, want about rgb(128,0,0)", got)
	}
	// After the fade: full red (the fade is dropped, not restarted).
	r4 := mustTool(t, cs, "preview_frame", map[string]any{"project": path, "time_ms": 1500, "width": 320})
	img, _ = jpeg.Decode(bytes.NewReader(r4.images[0]))
	if got := jpegPixel(t, img, 160, 90); !colorClose(got, Red, colorTol) {
		t.Errorf("after-fade pixel %s, want red", got)
	}
}

// A long render reports increasing progress, finishes at 100, and can be
// cancelled (no output file left behind).
func TestMCP_RenderProgressAndCancel(t *testing.T) {
	requireFFmpeg(t)
	requireEncoder(t, "libx264")
	cs := mcpSession(t)
	dir := t.TempDir()
	src := genVideo(t, VideoSpec{W: 1280, H: 720, Dur: 12 * time.Second, ToneHz: 440})
	path := filepath.Join(dir, "long.gomontage.json")
	mustTool(t, cs, "project_create", map[string]any{"path": path, "preset": "youtube_720p"})
	r := mustTool(t, cs, "media_import", map[string]any{"project": path, "paths": []string{src}})
	id := regexp.MustCompile(`\+ (m_\S+)`).FindStringSubmatch(r.text)[1]
	mustTool(t, cs, "timeline_edit", map[string]any{"project": path, "ops": []any{
		map[string]any{"op": "add_track", "kind": "video", "new_id": "v"},
		map[string]any{"op": "add_clip", "track": "v", "clip": map[string]any{"type": "video", "media": id, "at_ms": 0}},
	}})

	pctRE := regexp.MustCompile(`running · (\d+)%`)
	job := startedJob(t, mustTool(t, cs, "render", map[string]any{"project": path}))
	var seen []int
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		r = mustTool(t, cs, "render_job", map[string]any{"job": job})
		if m := pctRE.FindStringSubmatch(r.text); m != nil {
			p, _ := strconv.Atoi(m[1])
			seen = append(seen, p)
		}
		if !strings.Contains(r.text, "running") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(r.text, job+" done · 100%") {
		t.Fatalf("final status: %s", r.text)
	}
	mid := false
	for i, p := range seen {
		if p > 0 && p < 100 {
			mid = true
		}
		if i > 0 && p < seen[i-1] {
			t.Errorf("progress went backwards: %v", seen)
		}
	}
	if !mid {
		t.Errorf("no intermediate progress seen: %v", seen)
	}

	// Cancel a second render once it is under way.
	out := filepath.Join(dir, "cancelled.mp4")
	job = startedJob(t, mustTool(t, cs, "render", map[string]any{"project": path, "output": out}))
	for i := 0; i < 100; i++ {
		r = mustTool(t, cs, "render_job", map[string]any{"job": job})
		if m := pctRE.FindStringSubmatch(r.text); m != nil && m[1] != "0" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	r = mustTool(t, cs, "render_job", map[string]any{"job": job, "action": "cancel"})
	if !strings.Contains(r.text, job+" cancelled") {
		t.Fatalf("cancel: %s", r.text)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "cancelled*"))
	if len(matches) != 0 {
		t.Errorf("cancelled render left files: %v", matches)
	}
}

// A render that FFmpeg rejects is summarized: the relevant lines, the media
// and clips involved, and a hint; not the whole log.
func TestMCP_RenderErrorSummary(t *testing.T) {
	requireFFmpeg(t)
	requireEncoder(t, "libx264")
	cs := mcpSession(t)
	path := redBlueProject(t, cs)
	// Corrupt the blue file after import (render blockers only check that
	// files exist).
	blue := filepath.Join(filepath.Dir(path), mustMediaPath(t, path, "m_blue"))
	if err := os.WriteFile(blue, []byte("this is not a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := startedJob(t, mustTool(t, cs, "render", map[string]any{"project": path}))
	r := mustTool(t, cs, "render_job", map[string]any{"job": job, "wait_ms": 60000})
	for _, w := range []string{job + " failed", "ffmpeg failed", "involves media m_blue", "used by c_blue on v1", "hint: a media file is damaged"} {
		if !strings.Contains(r.text, w) {
			t.Errorf("error summary lacks %q:\n%s", w, r.text)
		}
	}
	if n := strings.Count(r.text, "\n"); n > 10 {
		t.Errorf("error summary is %d lines (a raw dump?):\n%s", n, r.text)
	}
}

func mustMediaPath(t *testing.T, projectPath, id string) string {
	t.Helper()
	var doc struct {
		Media []struct{ ID, Path string }
	}
	data, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, m := range doc.Media {
		if m.ID == id {
			return filepath.FromSlash(m.Path)
		}
	}
	t.Fatalf("media %q not found", id)
	return ""
}

// The gomontage-mcp binary writes nothing but JSON-RPC messages to stdout,
// even while it imports, previews and renders (FFmpeg runs underneath).
func TestMCP_StdoutIsProtocolOnly(t *testing.T) {
	requireFFmpeg(t)
	requireEncoder(t, "libx264")
	bin := filepath.Join(t.TempDir(), "gomontage-mcp")
	build := exec.Command("go", "build", "-o", bin, "../cmd/gomontage-mcp")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	src := genVideo(t, VideoSpec{W: 320, H: 180, Dur: 2 * time.Second, ToneHz: 440})
	path := filepath.Join(t.TempDir(), "p.gomontage.json")

	cmd := exec.Command(bin, "-log-level", "debug")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	lines := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var all []string
	id := 0
	send := func(method string, params any) map[string]any {
		t.Helper()
		id++
		msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := fmt.Fprintf(stdin, "%s\n", msg); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("server closed stdout; stderr:\n%s", stderr.String())
				}
				all = append(all, l)
				var m map[string]any
				if err := json.Unmarshal([]byte(l), &m); err != nil {
					t.Fatalf("stdout line is not JSON: %q", l)
				}
				if m["id"] == float64(id) {
					return m
				}
			case <-time.After(90 * time.Second):
				t.Fatalf("no answer to %s", method)
			}
		}
	}
	tool := func(name string, args any) string {
		t.Helper()
		m := send("tools/call", map[string]any{"name": name, "arguments": args})
		res, _ := m["result"].(map[string]any)
		if res == nil || res["isError"] == true {
			t.Fatalf("%s: %v", name, m)
		}
		var texts []string
		for _, c := range res["content"].([]any) {
			if s, ok := c.(map[string]any)["text"].(string); ok {
				texts = append(texts, s)
			}
		}
		return strings.Join(texts, "\n")
	}

	send("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "raw", "version": "1"}})
	fmt.Fprintln(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	tool("project_create", map[string]any{"path": path, "preset": "youtube_720p"})
	imp := tool("media_import", map[string]any{"project": path, "paths": []string{src}})
	mid := regexp.MustCompile(`\+ (m_\S+)`).FindStringSubmatch(imp)[1]
	tool("timeline_edit", map[string]any{"project": path, "ops": []any{
		map[string]any{"op": "add_track", "kind": "video", "new_id": "v"},
		map[string]any{"op": "add_clip", "track": "v", "clip": map[string]any{"type": "video", "media": mid, "at_ms": 0}},
	}})
	tool("timeline_get", map[string]any{"project": path})
	tool("preview_frame", map[string]any{"project": path, "time_ms": 1000})
	job := jobIDRE.FindStringSubmatch(tool("render", map[string]any{"project": path}))[1]
	if s := tool("render_job", map[string]any{"job": job, "wait_ms": 60000}); !strings.Contains(s, "done") {
		t.Fatalf("render: %s", s)
	}
	stdin.Close()
	for l := range lines { // drain until the server exits
		all = append(all, l)
	}
	_ = cmd.Wait()

	for _, l := range all {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil || m["jsonrpc"] != "2.0" {
			t.Errorf("non-protocol stdout line: %q", l)
		}
	}
	if !strings.Contains(stderr.String(), "tool call") {
		t.Errorf("logs didn't go to stderr; stderr:\n%s", stderr.String())
	}
}
