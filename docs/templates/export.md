# Export Profiles

Export profiles configure the output format, codec, quality, and resolution.

## Built-in Presets

### YouTube
```go
export.YouTube1080p()  // 1920x1080, H.264, AAC, CRF 18
export.YouTube4K()     // 3840x2160, H.264, AAC, CRF 18
export.YouTube720p()   // 1280x720, H.264, AAC, CRF 20
```

### Social Media
```go
export.Reel()  // 1080x1920 (9:16 vertical), H.264
```

### Professional
```go
export.ProRes()  // Apple ProRes 422, MOV container
export.H265()    // H.265/HEVC, better compression
```

### Audio Only
```go
export.MP3()   // MP3, 192kbps
export.WAV()   // Uncompressed PCM
export.FLAC()  // Lossless compressed
```

### Animated
```go
export.GIF()  // Animated GIF
```

## Custom Profiles

Use the fluent builder for full control:

```go
profile := export.NewProfile().
    WithName("Cinema 4K").
    WithCodec("libx265").
    WithCRF(18).
    WithPreset("slow").
    WithPixelFormat("yuv420p").
    WithResolution(3840, 2160).
    WithMaxRate("20M").
    WithBufSize("40M").
    WithAudioCodec("aac").
    WithAudioBitrate("320k").
    WithAudioSampleRate(48000).
    WithFastStart().
    Build()
```

## Output Size

The timeline's `Width`/`Height` is the **composition canvas**: every clip is
fitted, padded and composited at that size. A profile's `Width`/`Height`
(set by the YouTube and Reel presets, or `WithResolution`) is the **output
size**:

| Profile sets | Output |
|---|---|
| Neither width nor height (ProRes, H265, GIF, custom) | Timeline size |
| Both | The composite is scaled to fit, aspect ratio preserved, with black bars if the aspect ratios differ |
| Only one | The other is derived from the timeline's aspect ratio (rounded to even) |

For example, `export.YouTube4K()` on a 1920x1080 timeline produces 3840x2160,
and `export.Reel()` on a 16:9 timeline produces a 1080x1920 frame with the
16:9 picture letterboxed in the middle. To avoid bars, make the timeline the
same aspect ratio as the profile (e.g. `gomontage.Vertical()` with `Reel()`).

## Which Streams Are Written

Only the streams the output container can hold are written
(`Profile.OutputStreams`; the container comes from `Profile.Format` or the
file extension):

| Profile / container | Streams written | Error before rendering if… |
|---|---|---|
| Audio-only: MP3, WAV, FLAC (and ogg, opus, m4a, aac, aiff) | audio | the timeline has no audio |
| Video-only: GIF (and apng) | video | the timeline has no video |
| Everything else (mp4, mov, mkv, …) | video and audio, whichever exist | never |

So `tl.Export(export.WAV(), "audio.wav")` on a timeline with video extracts
the mixed soundtrack, and `export.GIF()` drops the audio.
