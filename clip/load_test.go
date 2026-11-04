package clip

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadFunctions_MissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.mp4")
	if _, err := LoadVideo(missing); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("LoadVideo(missing) error = %v, want 'file not found'", err)
	}
	if _, err := LoadAudio(missing); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("LoadAudio(missing) error = %v, want 'file not found'", err)
	}
	if _, err := LoadImage(missing); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("LoadImage(missing) error = %v, want 'file not found'", err)
	}
}

func TestSourceProblem(t *testing.T) {
	// Clips with unknown source length are never flagged.
	if err := SourceProblem(NewVideoWithDuration("x.mp4", 10*time.Second).Trim(0, 20*time.Second)); err != nil {
		t.Errorf("NewVideoWithDuration: unexpected %v", err)
	}
	if err := SourceProblem(NewImage("x.png").WithDuration(time.Hour)); err != nil {
		t.Errorf("NewImage: unexpected %v", err)
	}
	if err := SourceProblem(NewColor("#000", 1, 1)); err != nil {
		t.Errorf("NewColor: unexpected %v", err)
	}

	// A probed source length is enforced.
	v := NewVideoWithDuration("x.mp4", 2*time.Second)
	v.sourceDuration = 2 * time.Second
	if err := SourceProblem(v.Trim(0, 2*time.Second)); err != nil {
		t.Errorf("trim to exact end: unexpected %v", err)
	}
	if err := SourceProblem(v.Trim(time.Second, 5*time.Second)); err == nil {
		t.Error("trim past end: expected an error")
	}

	// A recorded probe error is reported (NewVideo on a missing file).
	missing := filepath.Join(t.TempDir(), "nope.mp4")
	c := NewVideo(missing)
	if err := SourceProblem(c); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("NewVideo(missing): SourceProblem = %v, want 'file not found'", err)
	}
}
