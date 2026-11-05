// Package schema holds the JSON Schema for GoMontage project files
// (project.v1.schema.json). This test keeps the schema and the examples in
// examples/ in sync: every example must validate, and a set of invalid
// projects must be rejected with a useful message.
package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaFile = "project.v1.schema.json"

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	sch, err := c.Compile(schemaFile)
	if err != nil {
		t.Fatalf("schema does not compile: %v", err)
	}
	return sch
}

func validateBytes(t *testing.T, sch *jsonschema.Schema, data []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return sch.Validate(inst)
}

func TestExamplesValidate(t *testing.T) {
	sch := compileSchema(t)
	files, err := filepath.Glob(filepath.Join("..", "examples", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no examples found in ../examples")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateBytes(t, sch, data); err != nil {
				t.Errorf("%s does not validate:\n%v", f, err)
			}
		})
	}
}

// minimal is a small valid project used as the base for invalid cases.
const minimal = `{
  "version": 1,
  "format": {"width": 1920, "height": 1080, "fps": 30},
  "media": [
    {"id": "m1", "kind": "video", "path": "a.mp4",
     "probe": {"duration_ms": 5000, "has_video": true, "has_audio": true, "width": 1920, "height": 1080, "fps": 30}},
    {"id": "m2", "kind": "audio", "path": "b.wav", "probe": {"duration_ms": 5000, "has_audio": true}}
  ],
  "tracks": [
    {"id": "t1", "kind": "video", "clips": [
      {"id": "c1", "type": "video", "media": "m1", "at_ms": 0, "effects": [{"type": "speed", "factor": 2}]}
    ]},
    {"id": "t2", "kind": "audio", "clips": [
      {"id": "c2", "type": "audio", "media": "m2", "at_ms": 0, "volume": 0.5}
    ]}
  ]
}`

// mutate parses minimal, applies f, and re-encodes.
func mutate(t *testing.T, f func(p map[string]any)) []byte {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal([]byte(minimal), &p); err != nil {
		t.Fatal(err)
	}
	f(p)
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func clip0(p map[string]any) map[string]any {
	return p["tracks"].([]any)[0].(map[string]any)["clips"].([]any)[0].(map[string]any)
}

func TestMinimalValidates(t *testing.T) {
	if err := validateBytes(t, compileSchema(t), []byte(minimal)); err != nil {
		t.Fatalf("minimal project should validate: %v", err)
	}
}

type invalidCase struct {
	name    string
	mutate  func(p map[string]any)
	wantErr string // substring expected somewhere in the error output
}

func invalidCases() []invalidCase {
	return []invalidCase{
		{"wrong version", func(p map[string]any) { p["version"] = 2 }, "/version"},
		{"unknown top-level field", func(p map[string]any) { p["foo"] = 1 }, "foo"},
		{"odd canvas width", func(p map[string]any) { p["format"].(map[string]any)["width"] = 1921 }, "/format/width"},
		{"float time", func(p map[string]any) { clip0(p)["at_ms"] = 1.5 }, "/at_ms"},
		{"negative time", func(p map[string]any) { clip0(p)["at_ms"] = -1 }, "/at_ms"},
		{"absolute media path", func(p map[string]any) {
			p["media"].([]any)[0].(map[string]any)["path"] = "/Users/me/a.mp4"
		}, "/path"},
		{"windows absolute path", func(p map[string]any) {
			p["media"].([]any)[0].(map[string]any)["path"] = `C:\video\a.mp4`
		}, "/path"},
		{"bad id", func(p map[string]any) { clip0(p)["id"] = "has space" }, "/id"},
		{"video media without probe fields", func(p map[string]any) {
			p["media"].([]any)[0].(map[string]any)["probe"] = map[string]any{"duration_ms": 1}
		}, "/probe"},
		{"unknown clip type", func(p map[string]any) { clip0(p)["type"] = "sticker" }, "/type"},
		{"unknown clip field", func(p map[string]any) { clip0(p)["opacity"] = 0.5 }, "opacity"},
		{"video clip with duration_ms", func(p map[string]any) { clip0(p)["duration_ms"] = 1000 }, "duration_ms"},
		{"audio clip on video track", func(p map[string]any) { clip0(p)["type"] = "audio" }, "/type"},
		{"text clip on audio track", func(p map[string]any) {
			tr := p["tracks"].([]any)[1].(map[string]any)
			tr["clips"] = []any{map[string]any{"id": "x", "type": "text", "text": "hi", "at_ms": 0, "duration_ms": 1000}}
		}, "/type"},
		{"layout on audio clip", func(p map[string]any) {
			c := p["tracks"].([]any)[1].(map[string]any)["clips"].([]any)[0].(map[string]any)
			c["position"] = map[string]any{"x": 0.5, "y": 0.5}
		}, "position"},
		{"position out of range", func(p map[string]any) { clip0(p)["position"] = map[string]any{"x": 1.5, "y": 0} }, "/x"},
		{"unknown anchor", func(p map[string]any) { clip0(p)["anchor"] = "middle" }, "/anchor"},
		{"empty size", func(p map[string]any) { clip0(p)["size"] = map[string]any{} }, "/size"},
		{"unknown effect type", func(p map[string]any) {
			clip0(p)["effects"] = []any{map[string]any{"type": "blur", "radius": 3}}
		}, "/effects/0/type"},
		{"speed without factor", func(p map[string]any) {
			clip0(p)["effects"] = []any{map[string]any{"type": "speed"}}
		}, "factor"},
		{"speed factor out of range", func(p map[string]any) {
			clip0(p)["effects"] = []any{map[string]any{"type": "speed", "factor": 50}}
		}, "/factor"},
		{"unknown effect param", func(p map[string]any) {
			clip0(p)["effects"] = []any{map[string]any{"type": "normalize", "db": 3}}
		}, "db"},
		{"param of another effect type", func(p map[string]any) {
			clip0(p)["effects"] = []any{map[string]any{"type": "normalize", "factor": 2}}
		}, "factor"},
		{"volume as an effect (it is a clip field)", func(p map[string]any) {
			clip0(p)["effects"] = []any{map[string]any{"type": "volume", "gain": 0.5}}
		}, "/effects/0/type"},
		{"volume out of range", func(p map[string]any) { clip0(p)["volume"] = 11 }, "/volume"},
		{"volume on a text clip", func(p map[string]any) {
			tr := p["tracks"].([]any)[0].(map[string]any)
			tr["clips"] = []any{map[string]any{"id": "x", "type": "text", "text": "hi", "at_ms": 0, "duration_ms": 1000, "volume": 0.5}}
		}, "volume"},
		{"duplicate effect type", func(p map[string]any) {
			clip0(p)["effects"] = []any{
				map[string]any{"type": "speed", "factor": 2},
				map[string]any{"type": "speed", "factor": 3},
			}
		}, "/effects"},
		{"separate audio speed effect", func(p map[string]any) {
			clip0(p)["effects"] = []any{map[string]any{"type": "audio_speed", "factor": 2}}
		}, "/effects/0/type"},
		{"fade effect instead of fade fields", func(p map[string]any) {
			clip0(p)["effects"] = []any{map[string]any{"type": "fade_in", "duration_ms": 500}}
		}, "/effects/0/type"},
		{"unknown transition type", func(p map[string]any) {
			clip0(p)["transition_in"] = map[string]any{"type": "spin", "duration_ms": 500}
		}, "/transition_in/type"},
		{"text font with family and file", func(p map[string]any) {
			tr := p["tracks"].([]any)[0].(map[string]any)
			tr["clips"] = []any{map[string]any{"id": "x", "type": "text", "text": "hi", "at_ms": 0, "duration_ms": 1000,
				"style": map[string]any{"font": map[string]any{"family": "A", "file": "a.ttf"}}}}
		}, "/font"},
		{"bad color", func(p map[string]any) {
			tr := p["tracks"].([]any)[0].(map[string]any)
			tr["clips"] = []any{map[string]any{"id": "x", "type": "color", "color": "red", "at_ms": 0, "duration_ms": 1000}}
		}, "/color"},
		{"unknown output preset", func(p map[string]any) {
			p["output"] = map[string]any{"preset": "tiktok", "path": "out.mp4"}
		}, "/output/preset"},
		{"unknown output fit", func(p map[string]any) {
			p["output"] = map[string]any{"preset": "reel", "path": "out.mp4", "fit": "stretch"}
		}, "/output/fit"},
	}
}

func TestInvalidProjectsRejected(t *testing.T) {
	sch := compileSchema(t)
	for _, tc := range invalidCases() {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBytes(t, sch, mutate(t, tc.mutate))
			if err == nil {
				t.Fatal("expected a validation error, got none")
			}
			// %#v gives the detailed, nested error list with instance locations.
			if msg := detailed(err); !strings.Contains(msg, tc.wantErr) {
				t.Errorf("error does not mention %q:\n%s", tc.wantErr, msg)
			}
		})
	}
}

// detailed returns the full nested error list (the "%#v" form of a
// jsonschema.ValidationError), which includes every instance location.
func detailed(err error) string {
	return fmt.Sprintf("%#v", err)
}
