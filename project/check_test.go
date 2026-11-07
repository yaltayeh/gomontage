package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lines(ps []Problem) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func TestCheckFile_ProjectErrors(t *testing.T) {
	r, p, err := CheckFile(filepath.Join("..", "schema", "testdata", "loader", "transition-across-gap.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p != nil || r.OK() || r.RenderChecked || len(r.RenderBlockers) != 0 {
		t.Errorf("result %+v, project %v", r, p)
	}
	if got := lines(r.ProjectErrors); len(got) != 1 || !strings.Contains(got[0], `transition_in needs the previous clip to end exactly at at_ms`) {
		t.Errorf("project errors %v", got)
	}
	var ve *ValidationError
	if !errors.As(r.Err(), &ve) || len(ve.Problems) != 1 {
		t.Errorf("Err() = %v", r.Err())
	}
}

func TestCheckFile_RenderBlockersAndNoDuplicateWarnings(t *testing.T) {
	r, p, err := CheckFile(filepath.Join("..", "examples", "simple-trim-concat.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || !r.RenderChecked || len(r.ProjectErrors) != 0 || r.OK() {
		t.Fatalf("result %+v", r)
	}
	want := []string{
		`/media/0: media "m_intro": file not found: media/intro.mp4`,
		`/media/1: media "m_interview": file not found: media/interview.mov`,
		`/media/2: media "m_outro": file not found: media/outro.mp4`,
	}
	if got := lines(r.RenderBlockers); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("render blockers:\n%s", strings.Join(got, "\n"))
	}
	if len(r.Warnings) != 0 {
		t.Errorf("missing media must not be repeated as warnings: %v", r.Warnings)
	}
}

func TestCheck_KnownGapsAreRenderBlockers(t *testing.T) {
	p := loadExampleWithFiles(t, "effects-transitions.json")
	r := Check(p)
	if len(r.ProjectErrors) != 0 || len(r.RenderBlockers) != 3 || r.OK() {
		t.Errorf("result: errors %v blockers %v", r.ProjectErrors, lines(r.RenderBlockers))
	}
	// output.fit "cover" is supported (phase 4): not a blocker.
	p.Output.Fit = FitCover
	if r = Check(p); len(r.RenderBlockers) != 3 {
		t.Errorf("render blockers %v", lines(r.RenderBlockers))
	}
}

func TestCheck_OK(t *testing.T) {
	p := loadExampleWithFiles(t, "podcast-audio.json")
	r := Check(p)
	if !r.OK() || r.Err() != nil {
		t.Errorf("result: %+v", r)
	}
	// Remove the file used only by the disabled music-bed track: still OK,
	// because only media used by enabled clips is required. It stays a load
	// warning.
	if err := os.Remove(p.Resolve("audio/music-bed.mp3")); err != nil {
		t.Fatal(err)
	}
	r = Check(p)
	if !r.OK() || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0].Message, `media "m_bed": file not found`) {
		t.Errorf("result: %+v", r)
	}
}

func TestCheck_InvalidInMemoryProject(t *testing.T) {
	p := New(t.TempDir(), Format{Width: 320, Height: 180, FPS: 30})
	p.Tracks = []Track{{ID: "x", Kind: KindVideo, Clips: []Clip{{ID: "x", Type: ClipColor, Color: "#000000", DurationMS: 1000}}}}
	r := Check(p)
	if r.RenderChecked || len(r.ProjectErrors) != 1 || !strings.Contains(r.ProjectErrors[0].Message, `duplicate id "x"`) {
		t.Errorf("result %+v", r)
	}
}

func TestCheckFile_Unreadable(t *testing.T) {
	if _, _, err := CheckFile(filepath.Join(t.TempDir(), "missing.gomontage.json")); err == nil {
		t.Error("expected an error for a missing file")
	}
}
