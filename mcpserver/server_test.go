package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ahmedhodiani/gomontage/project"
)

// golden compares got with testdata/<name>; GOMONTAGE_UPDATE_GOLDEN=1
// rewrites it.
func golden(t *testing.T, name, got string) {
	t.Helper()
	file := filepath.Join("testdata", name)
	if os.Getenv("GOMONTAGE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run with GOMONTAGE_UPDATE_GOLDEN=1 to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file.\n--- got:\n%s\n--- want:\n%s", name, got, want)
	}
}

func TestTimelineGet_Golden(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	// A gap on the logo track, a disabled track and a render blocker
	// (missing media) make every part of the summary appear.
	if text, isErr := edit(t, cs, path, `[
		{"op": "add_clip", "track": "t_logo", "clip": {"type": "text", "text": "Line one\nand a long second line that gets shortened", "at_ms": 7000, "duration_ms": 2000, "label": "outro", "style": {"font": {"file": "fonts/missing.ttf"}, "color": "#FFCC00", "outline": {"color": "#000000"}}}},
		{"op": "add_track", "kind": "audio", "name": "sfx", "new_id": "t_sfx"},
		{"op": "add_clip", "track": "t_sfx", "clip": {"type": "audio", "media": "m_b", "at_ms": 2000, "in_ms": 500, "out_ms": 1500, "effects": [{"type": "normalize", "target_lufs": -14}]}},
		{"op": "update", "id": "t_sfx", "set": {"enabled": false}},
		{"op": "update", "id": "c2", "set": {"include_audio": false, "out_ms": 4500, "effects": [{"type": "speed", "factor": 1.5, "affect_audio": false}]}}
	]`); isErr {
		t.Fatal(text)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(path), "media", "logo.png")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	norm := func(s string) string { return strings.ReplaceAll(s, dir, "<dir>") }

	text := mustCall(t, cs, "timeline_get", map[string]any{"project": path})
	golden(t, "timeline_get.golden", norm(text))
	text = mustCall(t, cs, "timeline_get", map[string]any{"project": path, "detail": true})
	golden(t, "timeline_get_detail.golden", norm(text))
}

func TestProjectCreate(t *testing.T) {
	cs, _ := session(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "new", "reel"+project.FileExtension)
	text := mustCall(t, cs, "project_create", map[string]any{"path": path, "preset": "reel"})
	assertContains(t, text, "created "+path, "format 1080x1920 @ 30fps", "output preset reel → output/reel.mp4")
	p := load(t, path)
	if p.Format != (project.Format{Width: 1080, Height: 1920, FPS: 30}) || p.Name != "reel" {
		t.Errorf("format %+v name %q", p.Format, p.Name)
	}

	text, isErr := call(t, cs, "project_create", map[string]any{"path": path, "preset": "reel"})
	if !isErr || !strings.Contains(text, "already exists") {
		t.Errorf("second create: %s", text)
	}
	mustCall(t, cs, "project_create", map[string]any{"path": path, "width": 640, "height": 480, "fps": 25, "output_preset": "gif", "overwrite": true})
	if p := load(t, path); p.Format.Width != 640 || p.Output.Preset != "gif" || p.Output.Path != "output/reel.gif" {
		t.Errorf("overwritten project %+v %+v", p.Format, p.Output)
	}
	if len(snapshots(t, path)) != 1 {
		t.Error("overwrite didn't snapshot the old file")
	}

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"path": filepath.Join(dir, "x.json"), "preset": "reel"}, "must end in .gomontage.json"},
		{map[string]any{"path": filepath.Join(dir, "y.gomontage.json"), "preset": "tiktok"}, `unknown preset "tiktok"`},
		{map[string]any{"path": filepath.Join(dir, "z.gomontage.json")}, "give a preset"},
		{map[string]any{"path": filepath.Join(dir, "w.gomontage.json"), "width": 641, "height": 480}, "can't create the project"},
	} {
		if text, isErr := call(t, cs, "project_create", c.args); !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%v: %s (want %q)", c.args, text, c.want)
		}
	}
}

// media_import reports each file; a failed file doesn't stop the others,
// and nothing is saved when every file fails. (Probing real files is
// covered by the e2e tests.)
func TestMediaImport_Errors(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	before := readFile(t, path)
	dir := filepath.Dir(path)
	text, isErr := call(t, cs, "media_import", map[string]any{"project": path, "paths": []string{
		filepath.Join(dir, "nope.mp4"), filepath.Join(dir, "media"), filepath.Join(dir, "media", "a.mp4"),
	}})
	if !isErr {
		t.Errorf("all-failed import not flagged as an error:\n%s", text)
	}
	assertContains(t, text, "imported 0, failed 2, already present 1",
		"✗ "+filepath.Join(dir, "nope.mp4")+": file not found",
		"is a directory", "= m_a · video")
	if readFile(t, path) != before {
		t.Error("project changed although nothing was imported")
	}
}

func TestRender_DryRunAndSafety(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	dir := filepath.Dir(path)

	text := mustCall(t, cs, "render", map[string]any{"project": path, "dry_run": true})
	assertContains(t, text, "dry run (nothing rendered) · preset youtube_720p · output "+filepath.Join(dir, "output", "fixture.mp4"),
		"ffmpeg -y", "xfade=", "-c:v libx264")
	if _, err := os.Stat(filepath.Join(dir, "output")); !os.IsNotExist(err) {
		t.Error("dry run created the output directory")
	}
	text = mustCall(t, cs, "render", map[string]any{"project": path, "dry_run": true, "preset": "mp3", "output": filepath.Join(dir, "x.mp3")})
	assertContains(t, text, "preset mp3", "libmp3lame")

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"project": path, "output": filepath.Join(dir, "media", "a.mp4")}, `is the source file of media "m_a"`},
		{map[string]any{"project": path, "output": filepath.Join(dir, "media", "..", "media", "b.mp4")}, `is the source file of media "m_b"`},
		{map[string]any{"project": path, "output": path, "preset": "wav"}, "extension"},
		{map[string]any{"project": path, "output": filepath.Join(dir, "x.gif")}, `doesn't suit preset "youtube_720p"`},
		{map[string]any{"project": path, "preset": "vhs"}, `unknown output preset "vhs"`},
	} {
		if text, isErr := call(t, cs, "render", c.args); !isErr || !strings.Contains(text, c.want) {
			t.Errorf("render %v:\n got %s\nwant %q", c.args, text, c.want)
		}
	}

	// A project with render blockers can't start a render.
	if err := os.Remove(filepath.Join(dir, "media", "b.mp4")); err != nil {
		t.Fatal(err)
	}
	text, isErr := call(t, cs, "render", map[string]any{"project": path})
	if !isErr || !strings.Contains(text, `media "m_b": file not found`) {
		t.Errorf("render with a missing file: %s", text)
	}
}

// The output may not overwrite the project file either.
func TestRenderTarget_RefusesProjectFile(t *testing.T) {
	path := writeFixture(t)
	p := load(t, path)
	p.Output = &project.Output{Preset: "youtube_720p", Path: "fixture.gomontage.json"}
	// RenderTarget rejects the extension first; check the guard directly.
	if _, _, err := renderTarget(path, p, "", ""); err == nil {
		t.Error("output = project file accepted")
	}
	if !sameFile(path, filepath.Join(filepath.Dir(path), ".", filepath.Base(path))) {
		t.Error("sameFile misses an equivalent path")
	}
}

func TestRenderJob_Errors(t *testing.T) {
	cs, _ := session(t)
	text := mustCall(t, cs, "render_job", map[string]any{})
	assertContains(t, text, "no render jobs")
	if text, isErr := call(t, cs, "render_job", map[string]any{"job": "r42"}); !isErr || !strings.Contains(text, `job "r42" not found`) {
		t.Errorf("unknown job: %s", text)
	}
	if text, isErr := call(t, cs, "render_job", map[string]any{"action": "pause", "job": "r1"}); !isErr || !strings.Contains(text, "unknown action") {
		t.Errorf("unknown action: %s", text)
	}
}

func TestPreview_OutOfRange(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	if text, isErr := call(t, cs, "preview_frame", map[string]any{"project": path, "time_ms": 10000}); !isErr || !strings.Contains(text, "outside the timeline (0 to 9999 ms") {
		t.Errorf("preview past the end: %s", text)
	}
	if text, isErr := call(t, cs, "preview_frame", map[string]any{"project": path, "time_ms": 0, "width": 5000}); !isErr || !strings.Contains(text, "out of range") {
		t.Errorf("huge preview: %s", text)
	}
}

// The preview window is about one frame, widened only to keep fades and
// transitions whole.
func TestPreviewWindow(t *testing.T) {
	p := load(t, writeFixture(t))
	total := projectDuration(p)
	cases := []struct {
		t, start, end int64
	}{
		{2000, 2000, 2068},  // plain frame
		{200, 0, 268},       // inside c1's fade-in: from the clip's start
		{6900, 6750, 7250},  // inside the c2→c3 crossfade window (7000 ± 250)
		{6740, 6740, 7250},  // window reaches into the crossfade
		{9990, 9990, 10000}, // last frame
	}
	for _, c := range cases {
		s, e := previewWindow(p, c.t, total)
		if s != c.start || e != c.end {
			t.Errorf("t=%d: window %d–%d, want %d–%d", c.t, s, e, c.start, c.end)
		}
	}
}

func TestToolsAndResources(t *testing.T) {
	cs, _ := session(t)
	ctx := context.Background()
	if got := cs.InitializeResult().Instructions; got != Instructions {
		t.Error("server instructions not sent")
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
		if !strings.Contains(strings.ToLower(tl.Description), "use ") {
			t.Errorf("%s: the description doesn't say when to use it", tl.Name)
		}
		if tl.Name == "timeline_edit" && !strings.HasPrefix(tl.Description, "Use for every timeline change; group all related operations into a single call.") {
			t.Errorf("timeline_edit description: %s", tl.Description)
		}
	}
	if strings.Join(names, ",") != "media_import,preview_frame,project_create,render,render_job,timeline_edit,timeline_get" {
		t.Errorf("tools %v", names)
	}
	for _, uri := range []string{ResourceSpec, ResourceSchema, ResourceMCP} {
		r, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil || len(r.Contents) != 1 || len(r.Contents[0].Text) < 100 {
			t.Errorf("resource %s: %v", uri, err)
		}
	}
}

// Field descriptions come from the project JSON Schema.
func TestEditSchema_DescriptionsFromProjectSchema(t *testing.T) {
	sc := editInputSchema()
	d := sc["$defs"].(map[string]any)
	if got := d["clip_volume"].(map[string]any)["description"]; got != "(video, audio clips) "+schemaDesc("volume") {
		t.Errorf("clip_volume description %q", got)
	}
	if got := d["clip_transition_in"].(map[string]any)["description"]; got != schemaDesc("videoClip", "transition_in") {
		t.Errorf("transition_in description %q", got)
	}
	media := d["clip_media"].(map[string]any)["description"].(string)
	for _, want := range []string{"video: ", "audio: ", "image: "} {
		if !strings.Contains(media, want) {
			t.Errorf("media description lacks %q: %s", want, media)
		}
	}
	if _, ok := d["videoClip"]; ok {
		t.Error("clip variant definitions copied (they're merged into clip_* fields)")
	}
}
