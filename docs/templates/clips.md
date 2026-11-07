# Clips

Clips are the fundamental building blocks of a Gomontage project. Every clip
is **immutable** — transform methods return new clips, leaving originals unchanged.

## Loading Files and Errors

`clip.NewVideo`, `clip.NewAudio` and `clip.NewImage` never fail. If you want
an error as soon as a file is missing or unreadable, use the `Load` variants:

```go
video, err := clip.LoadVideo("resources/video/interview.mp4") // (*VideoClip, error)
music, err := clip.LoadAudio("resources/audio/music.mp3")     // (*AudioClip, error)
logo, err  := clip.LoadImage("resources/images/logo.png")     // (*ImageClip, error)
```

Errors name the file and the problem: `file not found`, `cannot read media`,
`file has no video stream`, `file has no audio stream`.

Problems with clips created by `NewVideo`/`NewAudio` are still caught before
anything renders: `Timeline.Validate()` (which `Export` and `DryRun` run)
reports missing or unreadable sources, and trims or `WithDuration` values
that go past the end of the source file. `clip.SourceProblem(c)` runs the
same check for a single clip.

## Clip Types

### VideoClip

Load video from a file. Contains both video and audio streams.

```go
video := clip.NewVideo("resources/video/interview.mp4")

// Trim to a segment (times relative to source)
segment := video.Trim(10*time.Second, 30*time.Second)

// Adjust audio volume
quiet := video.WithVolume(0.5)

// Add fades
faded := video.WithFadeIn(1*time.Second).WithFadeOut(2*time.Second)

// Use only video (remove audio)
videoOnly := video.VideoOnly()

// Use only audio (remove video)
audioOnly := video.AudioOnly()

// Resize
small := video.WithSize(640, 360)
```

### AudioClip

Load audio from a file (WAV, MP3, FLAC, etc.).

```go
narration := clip.NewAudio("resources/audio/narration.wav")
music := clip.NewAudio("resources/audio/bgm.mp3").WithVolume(0.3)

// Trim
intro := narration.Trim(0, 30*time.Second)

// Fade in/out
faded := music.WithFadeIn(3*time.Second).WithFadeOut(5*time.Second)
```

### ImageClip

Static images used as video clips. Default duration is 5 seconds.

```go
logo := clip.NewImage("resources/images/logo.png").WithDuration(3*time.Second)
bg := clip.NewImage("resources/images/background.jpg").WithDuration(10*time.Second)
```

### TextClip

Dynamically rendered text overlays.

```go
title := clip.NewText("Chapter 1", clip.TextStyle{
    Font:  "resources/fonts/bold.ttf",
    Size:  72,
    Color: "#FFFFFF",
}).WithDuration(4*time.Second).WithFadeIn(500*time.Millisecond)
```

### ColorClip

Solid color rectangles for backgrounds.

```go
black := clip.NewColor("#000000", 1920, 1080).WithDuration(2*time.Second)
```

## Immutability

All transforms return new clips. The original is never modified:

```go
original := clip.NewVideo("video.mp4")
trimmed := original.Trim(0, 10*time.Second)
// original is still the full video
// trimmed is a new clip with just the first 10 seconds
```

## Chaining

Transforms can be chained fluently:

```go
result := clip.NewVideo("raw.mp4").
    Trim(5*time.Second, 25*time.Second).
    WithVolume(0.8).
    WithFadeIn(1*time.Second).
    WithFadeOut(2*time.Second)
```

## Layout (position and size)

Place any visual clip — video, image, color or text — with fractions of the
canvas, so the layout survives a change of format (16:9 → 9:16):

```go
logo := clip.NewImage("logo.png").WithDuration(5 * time.Second).
    WithLayout(clip.Layout{
        X: 0.96, Y: 0.04,             // a point on the canvas (0..1)
        Anchor: clip.AnchorTopRight,  // which point of the clip sits there
        Width: 0.18,                  // 18% of the canvas width; height keeps the aspect
    })
```

`Width`/`Height` are fractions of the canvas; give one (the other keeps the
aspect ratio), both (filled according to `Fit`: `clip.FitContain`,
`clip.FitCover` or `clip.FitStretch`), or neither (as large as possible inside
the canvas). Clips on upper video tracks are composited with transparency: the
tracks beneath show through around them and in gaps.

The older pixel API still works: `WithPosition(clip.Position{X: 200, Y: 100})`
places the clip's top-left corner at that pixel (at its source size, or the
`WithSize` you give); `-1` means the right/bottom edge and `clip.Center` the
centre.

## Text styling

```go
title := clip.NewText("قريباً", clip.TextStyle{
    Font:        "fonts/ElMessiri.ttf", // or FontFamily: "El Messiri" (fontconfig)
    Size:        96,                    // px
    Color:       "#FFFFFF",             // "#RRGGBBAA" for translucent text
    Background:  "#000000AA",           // box behind the text
    BoxPadding:  24,                    // px
    BorderWidth: 3, BorderColor: "#000000", // outline
    Align:       "center",              // multi-line alignment
    Direction:   "rtl",                 // "auto" (default), "ltr" or "rtl"
}).WithDuration(4 * time.Second).WithLayout(clip.Layout{X: 0.5, Y: 0.85, Anchor: clip.AnchorBottom})
```

The font must contain every character (there is no fallback font). Arabic is
shaped and laid out right to left by HarfBuzz/FriBidi; `Direction: "rtl"` makes
a line that starts with a Latin word read right to left too.
