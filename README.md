# GoMontage

![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white)
![FFmpeg](https://img.shields.io/badge/FFmpeg-required-007808?style=flat&logo=ffmpeg&logoColor=white)
![License](https://img.shields.io/badge/license-MIT-green?style=flat)

> GoMontage is a programmatic video editing framework for Go. Instead of reaching for a GUI editor, you describe your edit in code — trim clips, arrange tracks, layer audio, apply effects — and GoMontage compiles it into an FFmpeg command and renders it. If you can write Go, you can automate video production.

![titlecard](images/titlecard.png)

---

## How GoMontage Works

GoMontage has four layers that take your code all the way to a rendered video file:

1. **Clips** — Load media with `clip.NewVideo()`, `clip.NewAudio()`, etc. Clips are immutable; every transform (`.Trim()`, `.WithVolume()`) returns a new instance.
2. **Timeline** — Add clips to named video and audio tracks using `tl.AddVideoTrack()`. Positioning is explicit: `timeline.At(5 * time.Second)` places a clip at the 5-second mark.
3. **Compiler** — When you call `tl.Export()`, the Compiler walks the timeline and builds an FFmpeg filter graph — handling scaling, mixing, sequencing, and effects automatically.
4. **Engine** — The filter graph is handed to the Engine, which constructs the final FFmpeg command and runs it.

You write Go. GoMontage handles the FFmpeg.

---

## Requirements

- Go 1.25+
- FFmpeg & ffprobe installed and available in `PATH`

---

## Install

```bash
go install github.com/ahmedhodiani/gomontage/cmd/gomontage@latest
go install github.com/ahmedhodiani/gomontage/cmd/gomontage-mcp@latest   # MCP server (optional)
```

---

## Quick Start

### 1. Scaffold a project

```bash
gomontage init my-video
cd my-video
```

This creates the project structure with a starter `main.go`, config file, and folders for your media files.

### 2. Add your media

Drop video, audio, and image files into `resources/video/`, `resources/audio/`, etc.

### 3. Edit `main.go`

```go
package main

import (
    "time"

    "github.com/ahmedhodiani/gomontage/clip"
    "github.com/ahmedhodiani/gomontage/export"
    "github.com/ahmedhodiani/gomontage/timeline"
)

func main() {
    tl := timeline.New(timeline.Config{
        Width:  1920,
        Height: 1080,
        FPS:    30,
    })

    video := clip.NewVideo("resources/video/footage.mp4")
    trimmed := video.Trim(0, 30*time.Second)

    main := tl.AddVideoTrack("main")
    main.Add(trimmed, timeline.At(0))

    tl.Export(export.YouTube1080p(), "output/final.mp4")
}
```

### 4. Render

```bash
gomontage run
```

---

## CLI Commands

| Command | Description |
|---|---|
| `gomontage init <name>` | Scaffold a new project |
| `gomontage run` | Run `main.go` to render the video |
| `gomontage probe <file>` | Inspect media file metadata |
| `gomontage validate` | Validate the project config |
| `gomontage docs` | Generate API documentation into `docs/` |

### Generate docs

```bash
gomontage docs
```

Writes a full markdown API reference and guides into the `docs/` directory.

---

## Project files

Instead of writing Go code, an edit can be described in a JSON project file
(`*.gomontage.json`): media bin, tracks, clips, effects and output settings.
The format is specified in [docs/project-format.md](docs/project-format.md),
with a JSON Schema in [`schema/`](schema/project.v1.schema.json) and examples
in [`examples/`](examples/).

```bash
gomontage validate examples/simple-trim-concat.json   # project errors, render blockers, warnings
gomontage render my-edit.gomontage.json               # render with the project's output preset
gomontage render my-edit.gomontage.json -o out.wav --preset wav
```

From Go, the `project` package loads, validates, edits, saves and builds them:

```go
p, warnings, err := project.Load("my-edit.gomontage.json")
id, err := project.ImportMedia(p, "media/interview.mov") // probe once, add to the media bin
tl, err := project.Build(p)                              // -> *timeline.Timeline
err = project.Save("my-edit.gomontage.json", p)          // canonical, byte-stable
```

Every feature of the format renders, including crossfade transitions
(`transition_in`, handle model). Problems that stop a valid project from
rendering right now — e.g. a missing media file or font — are reported by
`render`/`validate` as "render blockers" instead of being rendered
differently.

## Transitions

```go
track.Add(a, 0)
track.AddWithTransition(b, a.Duration(), timeline.Crossfade(time.Second))
```

A transition is centred on the cut and uses source footage beyond the clips'
trim points, so it never changes clip positions or the timeline length (see
[docs/project-format.md §9](docs/project-format.md#9-transitions)).

---

## Output size

The timeline size is the composition canvas. If the export profile sets a
width/height (the YouTube and Reel presets do), the finished video is scaled
to that size, keeping the aspect ratio and adding black bars if the aspect
ratios differ. Profiles without a size (ProRes, H265, GIF, custom) output at
the timeline size. See `docs/templates/export.md`.

---

## Use with Claude Code

`gomontage-mcp` is an [MCP](https://modelcontextprotocol.io) server that lets
Claude Code (or any MCP client) edit and render GoMontage projects.

```bash
go install github.com/ahmedhodiani/gomontage/cmd/gomontage-mcp@latest
claude mcp add gomontage -- gomontage-mcp
```

(From a clone, `go build -o ~/go/bin/gomontage-mcp ./cmd/gomontage-mcp`
does the same. FFmpeg and ffprobe must be on `PATH`.)

It has seven tools:

- `project_create` and `media_import` set up a project.
- `timeline_get` returns a compact text view of the timeline, with the
  check results.
- `timeline_edit` takes one atomic batch of ops: add_track, add_clip, trim,
  remove (with ripple) and update.
- `preview_frame` returns one frame as a JPEG.
- `render` starts an async render job, and `render_job` follows it.

The project file is the state: every edit is saved at once, and the previous
version is kept in `.gomontage/history/`. All times are in milliseconds.

An example conversation:

> **You:** In `~/trip`, make a 1080p video from `drive.mp4`, `beach.mp4` and
> `sunset.mp4` with 1-second crossfades, the title "Road trip" for the first
> 3 seconds, and `song.mp3` quietly underneath.
>
> **Claude** calls:
> 1. `project_create` with preset `youtube_1080p`.
> 2. `media_import` with the four files.
> 3. One `timeline_edit` that adds the tracks, places the clips butt-joined
>    with crossfade `transition_in`s, adds the text on a titles track, and
>    adds the song at volume 0.3 with a fade-out.
> 4. `timeline_get`, which reports `check: ok (renderable)`.
> 5. `preview_frame` at 1000 ms, to check the title.
> 6. `render`, then `render_job` with `wait_ms`.
>
> **Claude:** Rendered `~/trip/output/trip.mp4` (27040 ms). The title fades in
> over the drive, the clips crossfade at 11000 and 19000 ms, and the song
> fades out at the end.

Every tool and op is documented in [docs/mcp.md](docs/mcp.md).

---

## Testing

```bash
make test            # fast unit tests (no FFmpeg)
make test-e2e        # end-to-end renders with real FFmpeg (known bugs skipped)
make test-e2e-long   # also the slow ones (full 20-minute podcast render)
make test-e2e-bugs   # also run known-bug tests (GOMONTAGE_KNOWN_BUGS=1; expected to fail)
```

See [TESTING.md](TESTING.md) for details.
