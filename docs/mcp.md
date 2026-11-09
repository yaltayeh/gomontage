# GoMontage MCP server

`gomontage-mcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server over stdio. It lets an agent such as Claude Code create, edit, preview
and render GoMontage projects (`*.gomontage.json`, see
[project-format.md](project-format.md)).

```bash
go install github.com/ahmedhodiani/gomontage/cmd/gomontage-mcp@latest
claude mcp add gomontage -- gomontage-mcp
```

It needs `ffmpeg` and `ffprobe` on `PATH` (import, preview and render call
them).

## Conventions

- **Stateless tools, the file is the state.** Every tool except
  `project_create` takes `project`, the path of the project file. Use an
  absolute path; relative paths (here and in every other path argument) are
  resolved against the server's working directory. Loaded projects are
  cached by path and reloaded when the file's modification time or size
  changes, so edits made by other programs are picked up. Calls on one
  project run one at a time, and calls on different projects run in parallel.
- **Every edit is saved immediately**, with the canonical `project.Save`
  (atomic write). Before each saving call (`timeline_edit`, `media_import`,
  `project_create` with `overwrite`), the previous file is copied to
  `.gomontage/history/<file name>/<UTC time>.gomontage.json` next to the
  project. A batch that changes nothing writes nothing.
- **Times are integer milliseconds** (`*_ms`) everywhere, as in the file
  format.
- **IDs** come from `timeline_get` (media, tracks and clips share one
  namespace). Created items get readable IDs (`t_titles`, `c_beach`,
  `c_beach_2`), or the ID you pass as `new_id`.
- **Layers:** the first video track in the list is the bottom layer, and
  later video tracks draw on top. Audio tracks are mixed.
- Results are short text meant for an agent, not JSON. Errors are tool errors
  (`isError`) with a message that says what to fix.
- stdout carries only protocol messages. Logs go to stderr
  (`gomontage-mcp -log-level info`).

## Resources

| URI | Content |
|---|---|
| `gomontage://docs/project-format` | The format specification (markdown) |
| `gomontage://schema/project.v1.json` | The JSON Schema of project files |
| `gomontage://docs/mcp` | This document |

## Tools

### `project_create`

Creates a new, empty project file.

| Field | Meaning |
|---|---|
| `path` | Required. Must end in `.gomontage.json`. Missing directories are created. |
| `preset` | `reel` (1080×1920), `youtube_720p`, `youtube_1080p` or `youtube_4k`, all at 30 fps. It also sets `output.preset` and `output.path` (`output/<name>.mp4`). |
| `width`, `height`, `fps` | The canvas, instead of a preset or overriding it. `fps` defaults to 30. |
| `output_preset` | The render preset to store (`h265`, `prores`, `gif`, `mp3`, `wav`, `flac`, ...). |
| `name` | The project name (default: the file name). |
| `overwrite` | Replace an existing file. Its old content goes to the history. |

Returns the `timeline_get` summary of the new project.

### `media_import`

`project`, `paths` (one or more files). Each file is probed once and added to
the media bin, and the reply has one line per file:

```
imported 2, failed 1, already present 1
+ m_beach · video · 12000ms 1920x1080 30fps · +audio · media/beach.mp4
+ m_song · audio · 180000ms 48000Hz 2ch · media/song.flac
= m_logo · image · 512x512 · logo.png (already imported)
✗ /work/missing.mp4: file not found
```

A file that is already in the bin returns its existing ID. A file that fails
doesn't stop the others. If every file fails, the result is an error and
nothing is saved.

### `timeline_get`

`project`, optional `detail`. A compact summary:

```
project /work/trip.gomontage.json
format 1920x1080 @ 30fps · duration 27040 ms
output preset youtube_1080p → output/trip.mp4

media (3)
  m_drive · video · 12000ms 1920x1080 30fps · +audio · media/drive.mp4
  ...

tracks (3; video tracks listed first draw at the bottom)
  t_main · video "main" · 3 clips
    c_drive · video m_drive · 0–11000 · src 0–11000
    c_beach · video m_beach · 11000–19000 · src 2000–10000 · crossfade_in 1000
    ...
  t_titles · video "titles" · 1 clip
    (gap 0–500)
    c_title · text "Road trip" · 500–3500 · fade_in 300 · pos 0.5,0.85 · style 0.08 box

check: ok (renderable)
```

Each clip line has its ID, type, source, `start–end` on the timeline, the
source window (`src in–out`), and the properties that differ from their
defaults. Gaps are shown. The check section lists project errors, render
blockers (missing media or fonts, nothing to render) and warnings. If the file
itself is invalid, its errors are listed instead. `detail: true` adds each
clip's full JSON.

### `timeline_edit`

One tool for every timeline change: `project` plus `ops`, an array applied in
order.

- **Atomic.** If any op fails, or the resulting project has project errors
  (schema or loader rules, e.g. overlapping clips), nothing is saved and the
  file stays byte-identical. Errors name the op: `ops[3] (update): clip or
  track "c9" not found`. A problem found while validating the result is
  attributed to the last op that changed the item it is about:
  `ops[1]: clip "c2": overlaps clip "c1" ...`.
- **One history snapshot per call.**
- **References within a batch.** Ops can use IDs created by earlier ops; set
  `new_id` to choose them.
- On success the reply lists the changes (`+` added, `~` changed with
  `field old→new`, `-` removed), the new IDs, notes about side effects,
  render blockers and warnings.

Each op is an object with `op` and the fields of that op. Any other field is
rejected, so a misspelt field never silently does nothing.

| op | Fields | Effect |
|---|---|---|
| `add_track` | `kind` (`video`/`audio`), `name`, `index`, `new_id` | Inserts a track at `index` in the track list (default: last). The default ID is `t_<name>` or `t_<kind>`. |
| `add_clip` | `track`, `clip`, `new_id` | Adds a clip of any type to any track. `clip` is a clip object exactly as in the file (`type`, `at_ms`, then the type's fields). `id` is optional. Text, logos, images and color overlays go on a video track above the main one. The default ID is `c_<media>`, `c_<text>` or `c_<type>`. |
| `trim` | `id`, `start_ms`, `end_ms`, `ripple` | Moves the clip's edges on the timeline. Video/audio clips adjust `in_ms`/`out_ms` (scaled by the speed factor); image/text/color clips adjust `duration_ms`. Without ripple the other edge stays put. With ripple the clip keeps its start, and the later clips on the track move by the change in length. Trimming the head therefore shortens the clip from the front and pulls the rest of the track earlier. |
| `remove` | `id`, `ripple` | Removes a clip, or a whole track with its clips. With ripple, the later clips on the same track move earlier by the clip's length. The clip that followed the removed one loses its `transition_in` (reported as a note). |
| `update` | `id`, `set` | Changes fields of a clip (any field except `id` and `type`; `at_ms` moves it) or a track (`name`, `enabled`). `set` is a JSON merge patch: given fields replace the current value, objects (`style`, `position`, `size`, `transition_in`) merge field by field, arrays (`effects`) are replaced whole, and `null` resets a field to its default. |

Ripple never moves clips on other tracks. Shift music or titles yourself
(e.g. `update` with `at_ms`, in the same batch) when they must stay in sync.

Transitions use the handle model of the format (§9). Set `transition_in` on
the incoming clip, e.g. `{"type": "crossfade", "duration_ms": 1000}`. The
previous clip must end exactly at the clip's `at_ms`, and both clips need
half the duration of extra source footage beyond their trim points. Nothing
moves or changes length.

Example: a title and a crossfade in one call:

```json
{"project": "/work/trip.gomontage.json", "ops": [
  {"op": "add_track", "kind": "video", "name": "titles", "new_id": "t_titles"},
  {"op": "add_clip", "track": "t_titles", "new_id": "c_title",
   "clip": {"type": "text", "text": "Road trip", "at_ms": 500, "duration_ms": 3000,
            "position": {"x": 0.5, "y": 0.85}, "fade_in_ms": 300,
            "style": {"size": 0.08, "box": {"color": "#00000080"}}}},
  {"op": "update", "id": "c_beach", "set": {"transition_in": {"type": "crossfade", "duration_ms": 1000}}},
  {"op": "update", "id": "c_song", "set": {"volume": 0.4, "fade_out_ms": 2000}}
]}
```

New ops (`split`, `move`, `close_gaps`, ...) will be added as new `op`
values with their own fields. Existing ops and fields keep their meaning.

### `preview_frame`

`project`, `time_ms`, optional `width` (default 480, at most 1920; the height
follows the canvas). Returns a JPEG image of the frame and a short text line.

To keep it fast, only about one frame of the timeline is rendered
(`Timeline.SubRange`) and nothing with sound. The range grows only when a
fade or a transition crosses its edge, so that frame is still exact. Sound is
never decoded, and audio tracks (even with missing files) don't block a
preview. A time where no video clip is on screen gives a black frame without
running FFmpeg. Frames are cached in memory, keyed by the project file's
content, the time and the width, so a repeat call for an unchanged project is
instant and says `cached`.

Images cost far more tokens than text, so check `timeline_get` first.

### `render`

`project`, optional `preset`, `output`, `dry_run`. Starts a render in the
background and returns a job ID at once:

```
job r1 started · preset youtube_1080p · 27040 ms of video → /work/output/trip.mp4
```

- The preset and output come from the project's `output` section unless
  given. With no output path anywhere, the output goes to
  `<project dir>/output/<project file name>.<ext>`. The output's directory
  is created.
- The output may not be the project file or the file of any media entry
  (paths are compared after resolving them, including symlinks).
- The job renders a snapshot of the project, so editing while it runs is
  safe. It writes to a temporary file next to the output and renames it on
  success. A failed or cancelled render leaves no partial file and keeps the
  previous output.
- Render blockers (missing files, fonts) are reported before anything
  starts.
- `dry_run: true` returns the FFmpeg command instead of rendering.

### `render_job`

`action` (`status` (default) or `cancel`), `job`, `wait_ms`.

- `status` with `wait_ms` (up to 60000) waits until the job finishes or the
  time is up, so an agent doesn't need to poll in a loop. Without `job` it
  lists all jobs.
- Running: `r1 running · 42% · elapsed 8000 ms · eta 11000 ms · output ...`.
- Done: `r1 done · 100% · output /work/output/trip.mp4 (14.2 MB, 27040 ms of
  video) · took 19000 ms`.
- Failed: the few FFmpeg lines that matter (not the whole log), the media and
  clips they mention, and a hint, e.g.

  ```
  r2 failed after 300 ms · output /work/output/trip.mp4
  ffmpeg failed (exit status 1):
    /work/media/beach.mp4: Invalid data found when processing input
    involves media m_beach (media/beach.mp4), used by c_beach on t_main
  hint: a media file is damaged or not a supported format: re-export it or replace the media
  ```
- `cancel` stops FFmpeg and removes the partial output.

Jobs live as long as the server process. Running jobs are cancelled when the
client disconnects.

## Example conversation

> **You:** Make a 30-second reel from `~/clips/*.mp4` with `song.mp3` under it,
> a title "Summer" at the start, and crossfades between the clips.
>
> **Claude:** *(project_create preset reel → media_import of the clips and the
> song → one timeline_edit that adds the tracks, places the clips butt-joined
> with `transition_in` crossfades, adds the title on a track above and the
> song with a fade-out → timeline_get to check → preview_frame at the title
> and inside a crossfade → render → render_job wait_ms 60000)*
>
> Done: `~/clips/output/summer.mp4` (30000 ms, 1080×1920). The title fades in
> over the first clip, and the song fades out over the last 2 seconds.
