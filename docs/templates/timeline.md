# Timeline & Tracks

The Timeline is the master container for your video project. It holds named
tracks where clips are placed at specific time positions.

## Creating a Timeline

```go
tl := timeline.New(timeline.Config{
    Width:  1920,
    Height: 1080,
    FPS:    30,
})
```

## Tracks

### Video Tracks

Video tracks hold visual clips. Multiple tracks are composited (layered)
in order — later tracks appear on top.

```go
main := tl.AddVideoTrack("main")       // Base layer
overlay := tl.AddVideoTrack("overlay")  // On top
```

### Audio Tracks

Audio tracks hold audio clips. All audio tracks are mixed together.

```go
voice := tl.AddAudioTrack("narration")
music := tl.AddAudioTrack("music")
sfx := tl.AddAudioTrack("sfx")
```

## Placing Clips

### At a Specific Time

```go
track.Add(myClip, timeline.At(5*time.Second))
```

### Sequentially (Back-to-Back)

```go
track.AddSequence(intro, mainContent, outro)
// intro starts at 0, mainContent starts when intro ends, etc.
```

### With a Transition

A transition joins a clip to the clip that ends exactly where it starts. It is
centred on the cut and made from extra source footage ("handles") beyond the
two clips' trim points, so clip positions and the timeline duration never
change:

```go
a := interview.Trim(10*time.Second, 20*time.Second)
b := broll.Trim(5*time.Second, 15*time.Second)
track.Add(a, 0)
track.AddWithTransition(b, a.Duration(), timeline.Crossfade(time.Second))
// The crossfade runs 9.5s-10.5s: a shows source 20s-20.5s after its end,
// b shows source 4.5s-5s before its start.
```

Handles scale with speed effects (a 2x clip needs twice as much source). Images,
text and colors have unlimited handles. On the bottom video track the pictures
dissolve (`xfade`); on upper tracks the two clips' opacity cross-fades; sound
cross-fades with `acrossfade` (audio tracks and the sound of video clips).
`Validate` reports a transition without an adjacent previous clip, without
enough source for its handles, or overlapping the clip's other transition.

## Timeline Duration

Duration is automatically calculated from the latest-ending clip:

```go
duration := tl.Duration() // Returns the total timeline length
```

## Validation

Check for errors before exporting:

```go
if err := tl.Validate(); err != nil {
    log.Fatal(err)
}
```
