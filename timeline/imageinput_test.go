package timeline

import (
	"strings"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/export"
)

func TestDryRun_SameImageDifferentDurationsGetOwnInputs(t *testing.T) {
	tl := New(Config{Width: 320, Height: 180, FPS: 30})
	tl.AddVideoTrack("v").AddSequence(
		clip.NewImage("card.png").WithDuration(2*time.Second),
		clip.NewImage("card.png").WithDuration(500*time.Millisecond),
		clip.NewImage("card.png").WithDuration(2*time.Second),
	)
	cmd, err := tl.DryRun(export.YouTube720p(), "out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	var durations []string
	for i, a := range cmd.Args {
		if a == "-t" && i+1 < len(cmd.Args) {
			durations = append(durations, cmd.Args[i+1])
		}
	}
	if got := strings.Join(durations, ","); got != "2.000,0.500" {
		t.Errorf("image input -t values = %q, want \"2.000,0.500\" (one input per distinct duration)", got)
	}
}
