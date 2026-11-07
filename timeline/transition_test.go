package timeline

import (
	"strings"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/effects"
	"github.com/ahmedhodiani/gomontage/engine"
)

const ms = time.Millisecond

func src(path string, d time.Duration) *clip.VideoClip {
	return clip.NewVideoFromInfo(path, &engine.MediaInfo{
		Path: path, Duration: d,
		VideoStreams: []engine.VideoStreamInfo{{Width: 320, Height: 180, FPS: 30}},
		AudioStreams: []engine.AudioStreamInfo{{SampleRate: 48000, Channels: 2}},
	})
}

func TestTransition_BeforeAfter(t *testing.T) {
	for _, c := range []struct{ d, before, after time.Duration }{
		{1000 * ms, 500 * ms, 500 * ms},
		{1001 * ms, 500 * ms, 501 * ms}, // odd: the extra millisecond is after the cut
		{1 * ms, 0, 1 * ms},
		{3 * ms, 1 * ms, 2 * ms},
	} {
		tr := Crossfade(c.d)
		if tr.Before() != c.before || tr.After() != c.after {
			t.Errorf("Crossfade(%s): before %s after %s, want %s %s", c.d, tr.Before(), tr.After(), c.before, c.after)
		}
	}
}

func TestTransition_SourceNeed(t *testing.T) {
	// 10 s at 3x lasts 3.333333333 s, so the measured rate is a hair above 3.
	rate3 := sourceRate(src("a.mp4", 20*time.Second).Trim(0, 10*time.Second).WithEffect(effects.SpeedUp(3)))
	for _, c := range []struct {
		d    time.Duration
		rate float64
		want time.Duration
	}{
		{500 * ms, 1, 500 * ms},
		{500 * ms, 4, 2000 * ms},
		{500 * ms, 0.5, 250 * ms},
		{501 * ms, 0.5, 251 * ms}, // 250.5 rounds up
		{500 * ms, 3, 1500 * ms},
		{500 * ms, rate3, 1500 * ms}, // not 1501 ms
	} {
		if got := sourceNeed(c.d, c.rate); got != c.want {
			t.Errorf("sourceNeed(%s, %g) = %s, want %s", c.d, c.rate, got, c.want)
		}
	}
}

func TestTransition_Validate(t *testing.T) {
	sec := time.Second
	cf := Crossfade(sec)
	cases := []struct {
		name  string
		build func(v *VideoTrack)
		want  string // "" = valid
	}{
		{"ok", func(v *VideoTrack) {
			v.Add(src("a.mp4", 10*sec).Trim(0, 4*sec), 0)
			v.AddWithTransition(src("b.mp4", 10*sec).Trim(1*sec, 5*sec), 4*sec, cf)
		}, ""},
		{"first clip", func(v *VideoTrack) {
			v.AddWithTransition(src("a.mp4", 10*sec).Trim(1*sec, 4*sec), 0, cf)
		}, "transition on the first clip of the track"},
		{"gap", func(v *VideoTrack) {
			v.Add(src("a.mp4", 10*sec).Trim(0, 3*sec), 0)
			v.AddWithTransition(src("b.mp4", 10*sec).Trim(1*sec, 5*sec), 3500*ms, cf)
		}, "transition needs the previous clip to end exactly at 3.500s; the nearest previous clip ends at 3.000s (0.500s gap)"},
		{"incoming handle", func(v *VideoTrack) {
			v.Add(src("a.mp4", 10*sec).Trim(0, 4*sec), 0)
			v.AddWithTransition(src("b.mp4", 10*sec).Trim(100*ms, 5*sec), 4*sec, cf)
		}, "transition needs 0.500s of source before the clip's trim start (0.100s); only 0.100s available"},
		{"incoming handle, slowed", func(v *VideoTrack) {
			v.Add(src("a.mp4", 10*sec).Trim(0, 4*sec), 0)
			v.AddWithTransition(src("b.mp4", 10*sec).Trim(250*ms, 2*sec).WithEffect(effects.SlowDown(2)), 4*sec, cf)
		}, ""},
		{"outgoing handle, sped up", func(v *VideoTrack) {
			v.Add(src("a.mp4", 10*sec).Trim(0, 9*sec).WithEffect(effects.SpeedUp(4)), 0) // needs 2 s after 9 s
			v.AddWithTransition(src("b.mp4", 10*sec).Trim(1*sec, 5*sec), 2250*ms, cf)
		}, "transition needs 2.000s of source after the trim end (9.000s) of the previous clip; only 1.000s available"},
		{"overlap", func(v *VideoTrack) {
			v.Add(src("a.mp4", 10*sec).Trim(0, 4*sec), 0)
			v.AddWithTransition(src("b.mp4", 10*sec).Trim(1*sec, 1800*ms), 4*sec, cf) // 0.8 s long
			v.AddWithTransition(src("c.mp4", 10*sec).Trim(1*sec, 5*sec), 4800*ms, cf)
		}, "transitions overlap: its transition in uses its first 0.500s and the next clip's transition uses its last 0.500s, but the clip is only 0.800s long"},
		{"generated clips have unlimited handles", func(v *VideoTrack) {
			v.Add(clip.NewColor("red", 320, 180).WithDuration(sec), 0)
			v.AddWithTransition(clip.NewImage("x.png").WithDuration(sec), sec, Crossfade(2*sec))
		}, ""},
		{"bad type", func(v *VideoTrack) {
			v.Add(src("a.mp4", 10*sec).Trim(0, 4*sec), 0)
			v.AddWithTransition(src("b.mp4", 10*sec).Trim(1*sec, 5*sec), 4*sec, Transition{Type: "wipe", Duration: sec})
		}, `unknown transition type "wipe"`},
		{"zero duration", func(v *VideoTrack) {
			v.Add(src("a.mp4", 10*sec).Trim(0, 4*sec), 0)
			v.AddWithTransition(src("b.mp4", 10*sec).Trim(1*sec, 5*sec), 4*sec, Crossfade(0))
		}, "transition duration must be positive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tl := New(Config{Width: 320, Height: 180, FPS: 30})
			c.build(tl.AddVideoTrack("main"))
			err := tl.Validate()
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("error = %v, want it to contain %q", err, c.want)
			}
		})
	}

	// Audio tracks: two clips ending at the cut make it ambiguous.
	tl := New(Config{Width: 320, Height: 180, FPS: 30})
	a := tl.AddAudioTrack("music")
	a.Add(clip.NewAudioWithDuration("a.wav", 4*sec), 0)
	a.Add(clip.NewAudioWithDuration("b.wav", 2*sec), 2*sec)
	a.AddWithTransition(clip.NewAudioWithDuration("c.wav", 4*sec).Trim(sec, 3*sec), 4*sec, cf)
	if err := tl.Validate(); err == nil || !strings.Contains(err.Error(), "transition is ambiguous: 2 clips end at 4.000s") {
		t.Errorf("ambiguous: %v", err)
	}
}

func TestTransition_SubRange(t *testing.T) {
	sec := time.Second
	tl := New(Config{Width: 320, Height: 180, FPS: 30})
	v := tl.AddVideoTrack("main")
	v.Add(src("a.mp4", 20*sec).Trim(0, 4*sec), 0)
	v.AddWithTransition(src("b.mp4", 20*sec).Trim(2*sec, 6*sec), 4*sec, Crossfade(sec))
	v.AddWithTransition(src("c.mp4", 20*sec).Trim(2*sec, 6*sec), 8*sec, Crossfade(2*sec))

	// 3-8.5 s holds the whole first window (3.5-4.5) but cuts the second
	// (7-9) at its end.
	sub := tl.SubRange(3*sec, 8500*ms)
	if err := sub.Validate(); err != nil {
		t.Fatal(err)
	}
	e := sub.VideoTracks()[0].Entries()
	if len(e) != 3 || e[1].TransitionIn == nil || e[2].TransitionIn != nil {
		t.Fatalf("entries %+v", e)
	}
	if e[1].StartAt != sec || e[0].Clip.Duration() != sec {
		t.Errorf("shifted wrong: %+v", e)
	}
	// 3.6-10 s cuts the first window at its start.
	if e := tl.SubRange(3600*ms, 10*sec).VideoTracks()[0].Entries(); e[1].TransitionIn != nil {
		t.Error("a transition cut by the range start was kept")
	}
}
