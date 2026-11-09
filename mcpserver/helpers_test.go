package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ahmedhodiani/gomontage/project"
)

// fixtureJSON is a small project: two video tracks (main with a
// crossfade, a logo overlay) and a music track. Its media files are empty
// placeholders (writeFixture creates them): nothing here runs FFmpeg.
const fixtureJSON = `{
  "version": 1,
  "name": "fixture",
  "format": {"width": 1280, "height": 720, "fps": 30},
  "media": [
    {"id": "m_a", "kind": "video", "path": "media/a.mp4", "probe": {"duration_ms": 10000, "has_video": true, "has_audio": true, "width": 640, "height": 360, "fps": 30, "sample_rate": 48000, "channels": 2}},
    {"id": "m_b", "kind": "video", "path": "media/b.mp4", "probe": {"duration_ms": 8000, "has_video": true, "has_audio": true, "width": 640, "height": 360, "fps": 30, "sample_rate": 48000, "channels": 2}},
    {"id": "m_logo", "kind": "image", "path": "media/logo.png", "probe": {"width": 200, "height": 100}},
    {"id": "m_song", "kind": "audio", "path": "media/song.wav", "probe": {"duration_ms": 30000, "has_audio": true, "sample_rate": 48000, "channels": 2}}
  ],
  "tracks": [
    {"id": "t_main", "kind": "video", "name": "main", "clips": [
      {"id": "c1", "type": "video", "media": "m_a", "at_ms": 0, "in_ms": 1000, "out_ms": 5000, "fade_in_ms": 500},
      {"id": "c2", "type": "video", "media": "m_b", "at_ms": 4000, "out_ms": 3000},
      {"id": "c3", "type": "video", "media": "m_a", "at_ms": 7000, "in_ms": 6000, "out_ms": 9000, "transition_in": {"type": "crossfade", "duration_ms": 500}}
    ]},
    {"id": "t_logo", "kind": "video", "name": "logo", "clips": [
      {"id": "c_logo", "type": "image", "media": "m_logo", "at_ms": 1000, "duration_ms": 5000, "position": {"x": 0.95, "y": 0.05}, "anchor": "top-right", "size": {"width": 0.15}}
    ]},
    {"id": "t_music", "kind": "audio", "name": "music", "clips": [
      {"id": "c_song", "type": "audio", "media": "m_song", "at_ms": 0, "out_ms": 9000, "volume": 0.3, "fade_out_ms": 1000}
    ]}
  ],
  "output": {"preset": "youtube_720p", "path": "output/fixture.mp4"}
}`

// writeFixture writes the fixture project (canonical) and its placeholder
// media into a temp dir and returns the project path.
func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p, _, err := project.Parse([]byte(fixtureJSON), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Media {
		f := p.Resolve(m.Path)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "fixture"+project.FileExtension)
	if err := project.Save(path, p); err != nil {
		t.Fatal(err)
	}
	return path
}

// session connects an in-memory client to a new server.
func session(t *testing.T) (*mcp.ClientSession, *Server) {
	t.Helper()
	s := New(Options{})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := s.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Close()
		s.jobs.cancelAll()
	})
	return cs, s
}

// call runs a tool and returns its text content and error flag.
func call(t *testing.T, cs *mcp.ClientSession, name string, args any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n"), res.IsError
}

// mustCall fails the test if the tool reports an error.
func mustCall(t *testing.T, cs *mcp.ClientSession, name string, args any) string {
	t.Helper()
	text, isErr := call(t, cs, name, args)
	if isErr {
		t.Fatalf("%s failed:\n%s", name, text)
	}
	return text
}

// edit runs timeline_edit with ops given as JSON.
func edit(t *testing.T, cs *mcp.ClientSession, path, ops string) (string, bool) {
	t.Helper()
	var o []any
	if err := json.Unmarshal([]byte(ops), &o); err != nil {
		t.Fatalf("bad ops JSON: %v", err)
	}
	return call(t, cs, "timeline_edit", map[string]any{"project": path, "ops": o})
}

func load(t *testing.T, path string) *project.Project {
	t.Helper()
	p, _, err := project.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func clipByID(t *testing.T, p *project.Project, id string) *project.Clip {
	t.Helper()
	ti, ci := findClip(p, id)
	if ti < 0 {
		t.Fatalf("clip %q not in project", id)
	}
	return &p.Tracks[ti].Clips[ci]
}

func snapshots(t *testing.T, path string) []string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(path), filepath.FromSlash(HistoryDir), filepath.Base(path))
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertContains(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("missing %q in:\n%s", w, text)
		}
	}
}
