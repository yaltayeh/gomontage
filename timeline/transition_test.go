package timeline

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
	"github.com/ahmedhodiani/gomontage/effects"
	"github.com/ahmedhodiani/gomontage/engine"
	"github.com/ahmedhodiani/gomontage/export"
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

// The worked example of docs/project-format.md §9.1: c_drive (out 40 s,
// 4x) into c_beach (in 5 s, 0.5x) with a 1 s crossfade.
func TestTransition_WindowsWorkedExample(t *testing.T) {
	drive := src("drive.mp4", 60*time.Second).Trim(0, 40*time.Second).WithEffect(effects.SpeedUp(4))
	beach := src("beach.mp4", 20*time.Second).Trim(5*time.Second, 9*time.Second).WithEffect(effects.SlowDown(2).KeepAudioSpeed())
	tr := Crossfade(time.Second)

	start, end := pictureWindow(drive, clipEdge{out: &tr})
	if start != 0 || end != 42*time.Second {
		t.Errorf("drive picture window %s-%s, want 0s-42s (2 s of source after out)", start, end)
	}
	start, end = pictureWindow(beach, clipEdge{in: &tr})
	if start != 4750*ms || end != 9*time.Second {
		t.Errorf("beach picture window %s-%s, want 4.75s-9s (250 ms before in)", start, end)
	}
	// The beach sound plays at normal speed: its head handle is 500 ms of
	// source, and with KeepAudioSpeed a slowed clip's sound ends at out_ms.
	s, e, lead := soundWindow(beach, clipEdge{in: &tr, out: &tr})
	if s != 4500*ms || e != 9*time.Second || lead != 0 {
		t.Errorf("beach sound window %s-%s lead %s, want 4.5s-9s lead 0", s, e, lead)
	}
	// A sound that follows the picture's speed scales its handles too.
	fast := src("a.mp4", 60*time.Second).Trim(10*time.Second, 20*time.Second).WithEffect(effects.SpeedUp(2))
	if s, e, _ := soundWindow(fast, clipEdge{in: &tr, out: &tr}); s != 9*time.Second || e != 21*time.Second {
		t.Errorf("2x sound window %s-%s, want 9s-21s", s, e)
	}
	// Not enough source before the trim start: the rest is silence in front.
	early := src("a.mp4", 60*time.Second).Trim(200*ms, 5*time.Second)
	if s, _, lead := soundWindow(early, clipEdge{in: &tr}); s != 0 || lead != 300*ms {
		t.Errorf("early sound window start %s lead %s, want 0s lead 300ms", s, lead)
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

func dryRun(t *testing.T, tl *Timeline) string {
	t.Helper()
	cmd, err := tl.DryRun(export.YouTube720p(), "out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	return cmd.String()
}

func TestTransition_BaseTrackGraph(t *testing.T) {
	sec := time.Second
	tl := New(Config{Width: 320, Height: 180, FPS: 30})
	v := tl.AddVideoTrack("main")
	v.Add(src("a.mp4", 10*sec).Trim(0, 3*sec), 0)
	v.Add(src("b.mp4", 10*sec).Trim(2*sec, 4*sec), 3*sec) // plain cut
	v.AddWithTransition(src("c.mp4", 10*sec).Trim(2*sec, 4*sec).WithEffect(effects.SpeedUp(2)), 5*sec, Crossfade(1001*ms))
	cmd := dryRun(t, tl)

	// a and b are concatenated as before; the run ends with b's 501 ms tail
	// handle and c starts 500 ms early (1 s of source at 2x).
	for _, want := range []string{
		"trim=end=4.501:start=2",
		"trim=end=4:start=1",
		"concat=a=1:n=2:v=1",
		"xfade=duration=1.001:offset=4.500:transition=fade",
		"acrossfade=c1=tri:c2=tri:d=1.001",
		"settb=expr=AVTB",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q:\n%s", want, cmd)
		}
	}
	// The sounds of clips with transitions are pinned to their exact length
	// including handles (b: 2 s + 501 ms, c: 500 ms + 1 s).
	for _, want := range []string{"apad=whole_dur=2.501", "apad=whole_dur=1.500"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q", want)
		}
	}
	if tl.Duration() != 6*sec {
		t.Errorf("timeline duration %s, want 6s", tl.Duration())
	}
}

func TestTransition_FadesStayOnTheClip(t *testing.T) {
	sec := time.Second
	tl := New(Config{Width: 320, Height: 180, FPS: 30})
	v := tl.AddVideoTrack("main")
	v.Add(src("a.mp4", 10*sec).Trim(0, 3*sec).WithFadeOut(sec), 0)
	v.AddWithTransition(src("b.mp4", 10*sec).Trim(2*sec, 4*sec).WithFadeIn(sec), 3*sec, Crossfade(sec))
	cmd := dryRun(t, tl)
	for _, want := range []string{
		`fade=d=1.000:enable=lt(t\\,3.000):st=2.000:t=out`,
		`fade=d=1.000:enable=gte(t\\,0.500):st=0.500:t=in`,
		`afade=d=1.000:enable=gte(t\\,0.500):st=0.500:t=in`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q:\n%s", want, cmd)
		}
	}
}

func TestTransition_UpperTrackAndAudioChains(t *testing.T) {
	sec := time.Second
	tl := New(Config{Width: 320, Height: 180, FPS: 30})
	tl.AddVideoTrack("base").Add(clip.NewColor("blue", 320, 180).WithDuration(6*sec), 0)
	up := tl.AddVideoTrack("up")
	up.Add(clip.NewImage("a.png").WithDuration(2*sec), sec)
	up.AddWithTransition(clip.NewText("Hi", clip.DefaultTextStyle()).WithDuration(2*sec), 3*sec, Crossfade(sec))
	mu := tl.AddAudioTrack("music")
	mu.Add(clip.NewAudioWithDuration("m.wav", 60*sec).Trim(0, 3*sec), 0)
	mu.AddWithTransition(clip.NewAudioWithDuration("m.wav", 60*sec).Trim(10*sec, 13*sec), 3*sec, Crossfade(2*sec))
	cmd := dryRun(t, tl)
	for _, want := range []string{
		// The image lasts until 3.5 s, fading out over 2.5-3.5 s, and is
		// placed at 1 s; the text fades in over the same window.
		"-loop 1 -t 2.500 -i a.png",
		"fade=alpha=1:d=1.000:st=1.500:t=out",
		"setpts=expr=PTS-STARTPTS+1.000/TB",
		`enable=between(t\\,2.500\\,5.000)`,
		`alpha=clip((t-2.500)/1.000\\,0\\,1)`,
		// The music chain: 1 s handles, crossfade, then delayed as a whole.
		"atrim=end=4:start=0",
		"atrim=end=13:start=9",
		"acrossfade=c1=tri:c2=tri:d=2.000",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q:\n%s", want, cmd)
		}
	}
	if n := len(regexp.MustCompile(`adelay`).FindAllString(cmd, -1)); n != 0 {
		t.Errorf("chain starting at 0 should not be delayed; %d adelay filters", n)
	}
}

func TestTextAlpha(t *testing.T) {
	in, out := Crossfade(time.Second), Crossfade(2*time.Second)
	got := textAlpha(2*time.Second, 5*time.Second, 500*ms, 0, clipEdge{in: &in, out: &out})
	want := "clip(min(min(if(lt(t,2.000),1,(t-2.000)/0.500),(t-1.500)/1.000),(6.000-t)/2.000),0,1)"
	if got != want {
		t.Errorf("textAlpha = %s\nwant          %s", got, want)
	}
	if got := textAlpha(2*time.Second, 5*time.Second, 0, 0, clipEdge{}); got != "" {
		t.Errorf("no fades, no transitions: %q", got)
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
