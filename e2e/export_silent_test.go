//go:build e2e

package e2e

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/engine"
	"github.com/ahmedhodiani/gomontage/timeline"
)

// captureOutput redirects os.Stdout and os.Stderr while f runs and returns
// what was written to them.
func captureOutput(t *testing.T, f func()) (stdout, stderr string) {
	t.Helper()
	read := func(r *os.File, dst *string, wg *sync.WaitGroup) {
		b, _ := io.ReadAll(r)
		*dst = string(b)
		wg.Done()
	}
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	var wg sync.WaitGroup
	wg.Add(2)
	go read(outR, &stdout, &wg)
	go read(errR, &stderr, &wg)
	defer func() {
		os.Stdout, os.Stderr = oldOut, oldErr
	}()
	f()
	outW.Close()
	errW.Close()
	wg.Wait()
	return stdout, stderr
}

// ExportSilentContext prints nothing and reports progress up to the end.
func TestExportSilentContext_ProgressWithoutOutput(t *testing.T) {
	requireFFmpeg(t)
	src := genVideo(t, VideoSpec{Dur: 3 * time.Second, ToneHz: 440})
	tl := timeline.New(timeline.Config{Width: 320, Height: 180, FPS: 30})
	tl.AddVideoTrack("main").Add(clip.NewVideo(src), 0)
	out := filepath.Join(t.TempDir(), "out.mp4")

	var mu sync.Mutex
	var last engine.Progress
	calls := 0
	var err error
	stdout, stderr := captureOutput(t, func() {
		err = tl.ExportSilentContext(context.Background(), testProfile(), out, func(p engine.Progress) {
			mu.Lock()
			last, calls = p, calls+1
			mu.Unlock()
		})
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("ExportSilentContext printed: stdout=%q stderr=%q", stdout, stderr)
	}
	if calls == 0 {
		t.Fatal("progress callback never called")
	}
	if last.Percent < 90 {
		t.Errorf("last progress = %.1f%%, want close to 100", last.Percent)
	}
	assertDuration(t, "output", probe(t, out).Duration, 3*time.Second, 30)
}

// Cancelling the context stops FFmpeg and returns the context error.
func TestExportSilentContext_Cancel(t *testing.T) {
	requireFFmpeg(t)
	src := genVideo(t, VideoSpec{W: 1280, H: 720, Dur: 20 * time.Second})
	tl := timeline.New(timeline.Config{Width: 1280, Height: 720, FPS: 30})
	tl.AddVideoTrack("main").Add(clip.NewVideo(src), 0)
	out := filepath.Join(t.TempDir(), "out.mp4")

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	start := time.Now()
	err := tl.ExportSilentContext(ctx, testProfile(), out, func(engine.Progress) { once.Do(cancel) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("cancel took %v", d)
	}
}
