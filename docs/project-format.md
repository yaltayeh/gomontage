# GoMontage Project Format — Version 1

**Status:** design (phase 3a). No loader or saver exists yet; this document is
the contract that `project.Load` (phase 3b) and the MCP server will implement.

- JSON Schema: [`schema/project.v1.schema.json`](../schema/project.v1.schema.json) (draft 2020-12)
- Examples: [`examples/`](../examples) — every example is validated against the
  schema by `go test ./schema/`.

A project file fully describes an edit: the canvas, the media it uses, the
tracks and clips, and the render settings. Tools (and later an MCP server) edit
this file instead of Go code. The existing Go API is unchanged; a loader builds
a `*timeline.Timeline` from the file.

---

## 1. Conventions

| Topic | Rule |
|---|---|
| Encoding | UTF-8 JSON. Recommended file name: `<name>.gomontage.json`. |
| Strictness | Unknown fields are rejected everywhere, except inside `meta` (free-form). The only other extra key allowed at the top level is `$schema`. |
| Version | Top-level `"version": 1`. Loaders reject any other value. A future version 2 ships with a migration from 1. |
| IDs | Every media entry, track and clip has an `id`: 1–64 chars, `^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`. **Unique across the whole project** (media, tracks and clips share one namespace), so a tool can address any item by ID alone. IDs never change once assigned; tools generate new ones for new items (e.g. `c_3f9a`). |
| Times | **Integer milliseconds**, always in fields ending in `_ms`. No floats for time. `at_ms`/`in_ms` ≥ 0; durations ≥ 1. |
| Paths | Relative to the directory containing the project file, `/` as separator. `..` is allowed; absolute paths (`/…`, `\…`, `C:…`) are not. |
| Colors | `#RRGGBB` or `#RRGGBBAA` (AA = alpha, `FF` opaque). |
| Canvas coordinates | Fractions of the canvas: `x`,`y` in [0, 1], (0,0) = top-left. Sizes are fractions of canvas width/height. Font sizes are fractions of canvas height. Everything visual therefore survives a change of `format.width/height`. |
| Defaults | A field's default applies when it is absent. Savers may omit fields equal to their default. |

---

## 2. Top level

```json
{
  "$schema": "../schema/project.v1.schema.json",
  "version": 1,
  "name": "Coming soon reel",
  "format": { "width": 1080, "height": 1920, "fps": 30 },
  "media":  [ … ],
  "tracks": [ … ],
  "output": { "preset": "reel", "path": "output/coming-soon.mp4" },
  "meta":   { }
}
```

| Field | Type | Req. | Default | Description |
|---|---|---|---|---|
| `$schema` | string | no | — | Schema reference for editors. Ignored. |
| `version` | `1` | **yes** | — | Format version. |
| `name` | string (1–200) | no | — | Human-readable name; default output file name when a caller renders without a path. |
| `format` | [Format](#3-format) | **yes** | — | Canvas size and frame rate. |
| `media` | array of [Media](#4-media) | **yes** | — | The media bin (may be empty). |
| `tracks` | array of [Track](#5-tracks) | **yes** | — | Tracks in layer order (may be empty). |
| `output` | [Output](#11-output) | no | — | Default render settings. |
| `meta` | object | no | — | Free-form tool data, stored and returned unchanged, never interpreted. |

---

## 3. Format

| Field | Type | Req. | Description | Go API |
|---|---|---|---|---|
| `width` | even int, 16–8192 | **yes** | Canvas width in px. | `timeline.Config.Width` |
| `height` | even int, 16–8192 | **yes** | Canvas height in px. | `timeline.Config.Height` |
| `fps` | number, (0, 240] | **yes** | Timeline frame rate; every video clip is converted to it. | `timeline.Config.FPS` |

`format` is required even for audio-only projects (tools write `1920×1080@30`);
it is simply unused there. Loader note: `29.97`, `59.94` and `23.976` should be
mapped to the exact NTSC rates (30000/1001 etc.) when the library supports
rational rates; today `Config.FPS` is a `float64`, which is fine.

---

## 4. Media

The media bin holds every source file, **probed once at import**. Clips refer to
media by ID. The probe results needed for building and validating the timeline
are stored, so loading a project never has to run ffprobe.

| Field | Type | Req. | Description |
|---|---|---|---|
| `id` | ID | **yes** | |
| `kind` | `"video"` \| `"audio"` \| `"image"` | **yes** | What the file is. A video file used only for its sound is still `video`. |
| `path` | relative path | **yes** | Location of the file. |
| `probe` | Probe | **yes** | Stored ffprobe results (below). |
| `fingerprint` | `{size_bytes?, modified_unix_ms?}` | no | File size / mtime at probe time, for staleness detection. |

**Probe** — required fields depend on `kind`:

| Field | Type | video | audio | image | Meaning |
|---|---|---|---|---|---|
| `duration_ms` | int ≥ 0 | **req** | **req** | — | Length of the file. |
| `has_video` | bool | **req** (`true`) | opt | — | Has a picture stream. |
| `has_audio` | bool | **req** | **req** (`true`) | — | Has a sound stream. |
| `width`, `height` | int ≥ 1 | **req** | — | **req** | Picture size (first video stream). |
| `fps` | number > 0 | **req** | — | — | Source frame rate (informational). |
| `sample_rate` | int ≥ 1 | opt | opt | — | Informational. |
| `channels` | int ≥ 1 | opt | opt | — | Informational. |

Source: `engine.Probe` → `engine.MediaInfo` (`Duration`, `HasVideo()`,
`HasAudio()`, `VideoStreams[0].Width/Height/FPS`, `AudioStreams[0].SampleRate/Channels`).

**Staleness.** If `fingerprint` is present and the file's current size or mtime
differs, the loader emits a **warning** (`media "m_x": file changed since it was
probed; re-import it`). Tools should re-probe and rewrite the entry.

Unused media entries (imported but not placed) are valid.

---

## 5. Tracks

| Field | Type | Req. | Default | Description |
|---|---|---|---|---|
| `id` | ID | **yes** | | |
| `kind` | `"video"` \| `"audio"` | **yes** | | |
| `name` | string ≤ 200 | no | — | Display name (`"main"`, `"titles"`, `"music"`). Maps to the `name` argument of `AddVideoTrack/AddAudioTrack` (falls back to the ID). |
| `enabled` | bool | no | `true` | Disabled tracks stay in the file but are not rendered. |
| `clips` | array of [Clip](#6-clips) | **yes** | | Clips on the track (any order; tools keep them sorted by `at_ms`). |

**Layer order.** Video tracks are composited bottom-up in array order: the first
video track is the bottom layer, each later one is drawn on top
(`timeline.AddVideoTrack` order). Audio tracks and the sound of video clips are
all mixed (`amix`); their order does not matter. Audio and video tracks may be
interleaved in the array; only the relative order of video tracks matters.

**What goes where** (enforced by the schema):

| Track kind | Allowed clip types |
|---|---|
| `video` | `video`, `image`, `text`, `color` |
| `audio` | `audio` |

**Overlap rules** (loader-enforced):
- *Video track:* clips must not overlap — ever. Transitions don't need an
  overlap: they use source handles beyond the clips' in/out points
  ([§9](#9-transitions)).
- *Audio track:* clips may overlap freely; overlapping sound is mixed.
- Gaps are allowed on every track: black on the bottom video track, transparent
  on upper video tracks (see [Known gaps](#14-known-gaps-and-loader-behavior), bug #1), silence on audio.

---

## 6. Clips

Every clip has a `type` that selects which fields apply. The schema rejects
fields that don't belong to the type (e.g. `duration_ms` on a `video` clip,
`position` on an `audio` clip).

### 6.1 Fields common to all clip types

| Field | Type | Req. | Default | Description |
|---|---|---|---|---|
| `id` | ID | **yes** | | |
| `type` | `video`\|`audio`\|`image`\|`text`\|`color` | **yes** | | |
| `label` | string ≤ 200 | no | — | Human-readable label. |
| `at_ms` | int ≥ 0 | **yes** | | Start on the timeline. |
| `fade_in_ms` | int ≥ 0 | no | `0` | Fade-in at the clip start. |
| `fade_out_ms` | int ≥ 0 | no | `0` | Fade-out at the clip end. |
| `transition_in` | [Transition](#9-transitions) | no | — | Transition from the previous clip on the track. |

### 6.2 `video` — part of a video file (video tracks only)

| Field | Type | Req. | Default | Description |
|---|---|---|---|---|
| `media` | ID | **yes** | | Media of kind `video`. |
| `in_ms` | int ≥ 0 | no | `0` | Start of the window in the source. |
| `out_ms` | int ≥ 1 | no | source end | End of the window (exclusive). `in_ms < out_ms ≤ probe.duration_ms`. |
| `include_audio` | bool | no | `true` | Play the file's sound with the picture. Ignored if the media has no audio. |
| `volume` | number 0–10 | no | `1` | Loudness of the clip's sound, linear (see [§6.9](#69-volume)). |
| `effects` | [Effects](#10-effects) | no | `[]` | |
| `position`, `anchor`, `size` | [Layout](#7-layout-position-anchor-size) | no | centred, full canvas, contain | |

### 6.3 `audio` — sound of a file (audio tracks only)

| Field | Type | Req. | Default | Description |
|---|---|---|---|---|
| `media` | ID | **yes** | | Media of kind `audio`, or kind `video` with `probe.has_audio: true` (its sound is used, the picture is ignored). |
| `in_ms`, `out_ms` | | | | As for `video` clips. |
| `volume` | number 0–10 | no | `1` | Loudness, linear (see [§6.9](#69-volume)). |
| `effects` | [Effects](#10-effects) | no | `[]` | |

### 6.4 `image` — a still picture (video tracks only)

| Field | Type | Req. | Description |
|---|---|---|---|
| `media` | ID | **yes** | Media of kind `image`. |
| `duration_ms` | int ≥ 1 | **yes** | How long it is shown. |
| `position`, `anchor`, `size` | Layout | no | |

PNG transparency is preserved on upper video tracks.

### 6.5 `color` — a solid color (video tracks only)

| Field | Type | Req. | Description |
|---|---|---|---|
| `color` | color | **yes** | Fill color. |
| `duration_ms` | int ≥ 1 | **yes** | |
| `position`, `anchor`, `size` | Layout | no | Default: the whole canvas. |

### 6.6 `text` — rendered text (video tracks only)

| Field | Type | Req. | Description |
|---|---|---|---|
| `text` | string 1–2000 | **yes** | Literal text, any Unicode (Arabic, Hebrew, CJK, emoji if the font has them). `\n` = line break. No escaping is ever needed: the renderer escapes for FFmpeg (`engine.EscapeFilterValue`). |
| `duration_ms` | int ≥ 1 | **yes** | |
| `style` | [TextStyle](#8-text-style) | no | |
| `position`, `anchor` | Layout | no | Default: centred. Text has no `size`; its box size follows from the font size. |

### 6.7 Clip timeline duration

| Type | Duration on the timeline |
|---|---|
| `video`, `audio` | `round((out_ms − in_ms) / speed.factor)` (`factor` = 1 without a speed effect). Round half away from zero. |
| `image`, `text`, `color` | `duration_ms` |

The clip occupies `[at_ms, at_ms + duration)`. The project duration is the
latest end over all **enabled** tracks. Transitions never change these values
(they use handles, §9).

All positions are computed in integer milliseconds with this formula, and all
overlap/transition rules use it. (With fractional speed factors the Go library
carries sub-millisecond precision internally, e.g. 10000/3 ms; the difference
is below one frame and is ignored.)

### 6.8 Fades — one mechanism

`fade_in_ms` / `fade_out_ms` are the **only** way to fade. They apply to the
picture **and** the sound of the clip:

- picture: from/to black on the bottom video track; from/to transparent on upper
  video tracks (opacity fade);
- sound: from/to silence (video clips with `include_audio`, and audio clips).

Fades are measured in timeline time, after speed changes. Rule:
`fade_in_ms + fade_out_ms ≤ clip duration`. There are no fade effects (the
library's `effects.FadeIn/FadeOut/AudioFadeIn/AudioFadeOut` are not exposed).

Go API: `WithFadeIn/WithFadeOut` on every clip type (the compiler applies them to
picture and embedded sound, after effects — fixed in known bugs #7 and #12).

### 6.9 Volume

`volume` is a clip field on `video` and `audio` clips (the clips that can make
sound), next to the fade fields. It is a **linear** multiplier: `1` (default)
unchanged, `0.5` ≈ −6 dB, `2` ≈ +6 dB, `0` silent; range 0–10. It is the only
way to change a clip's loudness — there is no volume effect. Fades are applied
on top of it.

Go API: `WithVolume(volume)` on `VideoClip` / `AudioClip`.

---

## 7. Layout (position, anchor, size)

Applies to `video`, `image`, `color` (all three fields) and `text`
(`position`, `anchor` only).

**`position`** `{x, y}`, each in [0, 1], default `{0.5, 0.5}`: a point on the
canvas, in fractions of its width and height.

**`anchor`**, default `"center"`: which point of the clip is placed at
`position`. One of `top-left`, `top`, `top-right`, `left`, `center`, `right`,
`bottom-left`, `bottom`, `bottom-right`.

Pixel placement (canvas `W×H`, clip box `w×h`, anchor fractions `ax, ay` where
left/top = 0, center = 0.5, right/bottom = 1):

```
left = round(x·W − ax·w)
top  = round(y·H − ay·h)
```

Example: a logo in the top-right corner with a 4% margin:
`"position": {"x": 0.96, "y": 0.04}, "anchor": "top-right"`.

**`size`** — fractions of the canvas; at least one of `width`/`height`:

| Given | Box |
|---|---|
| `width` only | `w = width·W`, height from the clip's aspect ratio. |
| `height` only | `h = height·H`, width from the clip's aspect ratio. |
| both | box `width·W × height·H`, filled according to `fit`. |

`fit` (only with both): `contain` (default; whole picture visible, aspect kept,
empty space left transparent/black), `cover` (box filled, aspect kept, overflow
cropped) or `stretch` (fills the box exactly, aspect not kept). Values may go up
to 4 (zoomed in beyond the canvas). Clip pixels outside the canvas are cropped.

**Defaults.** No `size` = `{width: 1, height: 1, fit: contain}`: the picture is
as large as possible inside the canvas — exactly what the library does today
for every clip (`scale … force_original_aspect_ratio=decrease` + `pad`). A
`color` clip without `size` fills the canvas.

---

## 8. Text style

All fields optional.

| Field | Type | Default | Description |
|---|---|---|---|
| `font` | `{family}` **or** `{file}` | system sans-serif | `family`: installed font name (`"Noto Sans Arabic"`), resolved by fontconfig. `file`: font file relative to the project. Exactly one of the two. For Arabic and other non-Latin scripts the font must contain those letters (the default font often does not). |
| `size` | number (0, 1] | `0.05` | Font size as a fraction of canvas height (0.05 → 54 px on a 1080-high canvas). |
| `color` | color | `#FFFFFF` | Text color (alpha allowed). |
| `box` | `{color, padding?}` | none | Background box. `padding` = fraction of font size (default 0.25). |
| `outline` | `{color, width?}` | none | Letter outline. `width` = fraction of font size (default 0.05). |
| `align` | `left`\|`center`\|`right` | `center` | Alignment of lines in multi-line text. |
| `direction` | `auto`\|`ltr`\|`rtl` | `auto` | `auto` detects direction from the text (Arabic → RTL). |

RTL: text is shaped with HarfBuzz and reordered with FriBidi, which requires an
FFmpeg built with `libharfbuzz` and `libfribidi` (`drawtext`). Verified in phase 2
with `قريباً` and a Tahoma font (`e2e`, known bug #3 test).

Go API: `clip.NewText(text, clip.TextStyle{Font: file, Size: round(size·H), Color})`.
See [Known gaps](#14-known-gaps-and-loader-behavior) for fields the compiler does not render yet.

---

## 9. Transitions

```json
"transition_in": { "type": "crossfade", "duration_ms": 1000 }
```

A transition is a property of the **incoming** clip: it describes the
transition at the cut between this clip and the **previous clip** on the same
track, i.e. the clip that ends exactly at this clip's `at_ms`.

| Field | Type | Req. | Description |
|---|---|---|---|
| `type` | `"crossfade"` | **yes** | Pictures dissolve into each other; sounds cross-fade. |
| `duration_ms` | int ≥ 1 | **yes** | Length of the transition, centred on the cut. |

### 9.1 Handle model

Clips stay **butt-joined**. A transition is centred on the cut and is made from
extra source footage ("handles") beyond the clips' in and out points, so
**adding, changing or removing a transition never changes any `at_ms`, any clip
duration, or the track/timeline duration.**

For a cut at `T` (= incoming clip's `at_ms`) and `d = duration_ms`:

```
before = floor(d / 2)        after = d - before   (= ceil(d / 2))

timeline:   ... outgoing clip P ...  | T |  ... incoming clip C ...
transition:              [T - before ......... T + after)
P plays:     normal tail  [T-before, T)   + handle [T, T+after)  ← source AFTER P.out_ms
C plays:     handle [T-before, T)  ← source BEFORE C.in_ms   + normal head [T, T+after)
```

Handle sizes in **source** time (speed effects scale them; round up):

| Clip | Needs | Available |
|---|---|---|
| incoming `C` (video/audio) | `ceil(before × C.speed)` ms before `C.in_ms` | `C.in_ms` |
| outgoing `P` (video/audio) | `ceil(after × P.speed)` ms after `P.out_ms` | `media(P).probe.duration_ms − P.out_ms` |
| `image`, `text`, `color` | — | unlimited (the still picture / text / color simply shows longer) |

`speed` is the clip's `speed.factor` (1 without a speed effect). With
`affect_audio: false` (not renderable yet, §14) the sound handle would use
factor 1.

**Worked example** (`examples/effects-transitions.json`, cut into `c_beach` at
11000 ms, `crossfade` 1000 ms → before = 500, after = 500):

- incoming `c_beach` (in 5000, slow-mo factor 0.5) needs ceil(500 × 0.5) = **250 ms**
  of source before 5000 — 5000 ms available ✓.
- outgoing `c_drive` (out 40000, factor 4, media 60000 ms) needs
  ceil(500 × 4) = **2000 ms** after 40000 — 20000 ms available ✓.
- The crossfade runs 10500–11500 ms; every `at_ms` and the total length
  (27000 ms) are unchanged by it.

### 9.2 Rules (loader, all errors)

| Rule | Error message (exact format) |
|---|---|
| Not on the first clip of a track. | `clip "c1": transition_in on the first clip of track "t_main" (there is no previous clip)` |
| A clip must end exactly at `at_ms` (no gap). | `clip "c2": transition_in needs the previous clip to end exactly at at_ms (3500ms); the nearest previous clip "c1" ends at 3000ms (500ms gap)` |
| Exactly one clip ends at `at_ms` (relevant on audio tracks, where clips may overlap). | `clip "c3": transition_in is ambiguous: clips "a", "b" all end at at_ms (5000ms)` |
| Incoming handle available. | `clip "c2": transition_in needs 250ms of source before in_ms (100ms); only 100ms available` |
| Outgoing handle available. | `clip "c2": transition_in needs 250ms of source after out_ms (4000ms) of previous clip "c1"; only 100ms available (media "m_short" is 4100ms long)` |
| A clip's two transitions don't overlap: `after(own transition_in) + before(next clip's transition_in) ≤ clip duration`. | `clip "c2": transitions overlap: transition_in of "c2" uses its first 500ms and transition_in of "c3" uses its last 600ms, but "c2" is only 1000ms long` |

Each rule has a fixture in `schema/testdata/loader/` that is schema-valid and
records the expected message in `meta.expect_error`.

### 9.3 Interaction with fades and tracks

- Fades still apply to the clip's own span `[at_ms, at_ms + duration)`; handle
  footage is not faded. Use fades **or** a transition at a cut, usually not both.
- Allowed on video tracks (picture and the sound of video clips) and on audio
  tracks (sound).
- More types (`wipe_left`, `slide_up`, … from FFmpeg `xfade`) can be added later
  without a version change.

Go API: **none yet** — see [Known gaps](#14-known-gaps-and-loader-behavior).

---

## 10. Effects

```json
"effects": [
  { "type": "speed", "factor": 0.5, "affect_audio": false },
  { "type": "normalize", "target_lufs": -16 }
]
```

A flexible list, but each `type` has a **strict** parameter schema: unknown
types, unknown parameters, missing required parameters and out-of-range values
are all rejected with an error naming the exact location (e.g.
`/tracks/0/clips/2/effects/0: additional properties 'db' not allowed`).
Each type may appear **at most once** per clip. Effects are allowed on `video`
and `audio` clips only. They are applied in list order.

| Type | Params | Description | Go API |
|---|---|---|---|
| `speed` | `factor` (req, 0.1–10), `affect_audio` (bool, default `true`) | Plays faster (`2`) or slower (`0.5`). Timeline duration = source window / factor. With `affect_audio: true` the sound is retimed too (pitch preserved), keeping sync. With `false` the sound plays at normal speed and is cut at the clip end (or ends early when slowed). Not allowed with `affect_audio: false` on `audio` clips (meaningless). | video clip: `effects.SpeedUp(factor)` (audio follows automatically, fixed in bug #8). `affect_audio:false`: add `effects.AudioSpeed(1)` to suppress the automatic retiming (see gaps). audio clip: `effects.AudioSpeed(factor)`. |
| `normalize` | `target_lufs` (−70 to −5, default −16) | EBU R128 loudness normalization. | `effects.NormalizeTo(target_lufs)` |

There is no separate audio-speed effect (one way to change speed) and no fade
effects (fades are clip fields) and no volume effect (`volume` is a clip
field, §6.9). `normalize` or `volume ≠ 1` on a `video` clip with
`include_audio: false` or without sound have no effect; the loader warns.

---

## 11. Output

| Field | Type | Req. | Default | Description |
|---|---|---|---|---|
| `preset` | enum (below) | **yes** | | Encoding settings. |
| `path` | relative path | **yes** | | Output file; extension must suit the preset (loader-checked). |
| `fit` | `contain` \| `cover` | no | `contain` | How the canvas fits a preset with a fixed size that differs from `format`. `contain` = scale to fit + black bars; `cover` = scale to fill + crop. |

| Preset | Go profile | Fixed size | Streams | Extensions |
|---|---|---|---|---|
| `youtube_720p` | `export.YouTube720p()` | 1280×720 | A/V | `.mp4`, `.mov` |
| `youtube_1080p` | `export.YouTube1080p()` | 1920×1080 | A/V | `.mp4`, `.mov` |
| `youtube_4k` | `export.YouTube4K()` | 3840×2160 | A/V | `.mp4`, `.mov` |
| `reel` | `export.Reel()` | 1080×1920 | A/V | `.mp4`, `.mov` |
| `h265` | `export.H265()` | canvas | A/V | `.mp4`, `.mov`, `.mkv` |
| `prores` | `export.ProRes()` | canvas | A/V | `.mov` |
| `gif` | `export.GIF()` | canvas | video | `.gif` |
| `mp3` | `export.MP3()` | — | audio | `.mp3` |
| `wav` | `export.WAV()` | — | audio | `.wav` |
| `flac` | `export.FLAC()` | — | audio | `.flac` |

Output size and stream selection follow the phase-2 rules
(`Compiler.CompileProfile`, `Profile.OutputStreams`; see
`docs/templates/export.md`). `output` is optional so a project can exist before
render settings are chosen; tools may also pass preset/path per render.

---

## 12. Validation

Validation happens in two layers, in this order:

1. **Schema (shape).** The loader embeds `schema/project.v1.schema.json` with
   `go:embed` and validates every file against it before doing anything else.
   The schema is the **single source of truth for shape validation**; the loader
   does not re-implement those rules in Go. (`go:embed` cannot reach parent
   directories, so the `schema` package will expose the embedded file, e.g.
   `schema.ProjectV1 []byte`, and the loader imports it. The validator is
   `github.com/santhosh-tekuri/jsonschema/v6`, already used by
   `schema/schema_test.go`.)
2. **Loader rules (semantics)** L1–L14 below, which JSON Schema cannot express.
   They run only if the schema validation passed, so they can rely on the shape.

Both layers report **all** problems found (not just the first), each with a
JSON-pointer location and, where available, the ID:

```
/tracks/1/clips/0 (clip "c_logo"): media "m_logo2" does not exist
```

### 12.1 Schema (structure) — `schema/project.v1.schema.json`

Types, required fields, ranges, enums, ID/path/color formats, which fields each
clip type and effect type accepts, which clip types each track kind accepts,
at-most-once effects, integer times.

### 12.2 Loader (semantics) — not expressible in JSON Schema

| # | Rule | Severity |
|---|---|---|
| L1 | IDs are unique across media, tracks and clips. | error |
| L2 | Every `media` reference exists and has a compatible kind (§6). | error |
| L3 | `in_ms < out_ms ≤ media.probe.duration_ms`. | error |
| L4 | `fade_in_ms + fade_out_ms ≤` clip duration. | error |
| L5 | No overlapping clips on video tracks (§5). | error |
| L6 | `speed.affect_audio: false` only on `video` clips. | error |
| L7 | `output.path` extension suits `output.preset`. | error |
| L8 | Media file exists at `path`. | warning on load, error on render (so tools can open a project and relink missing media) |
| L9 | Font `file` exists / font `family` resolves. | error on render |
| L10 | Fingerprint differs from the file. | warning |
| L11 | `volume ≠ 1` or a `normalize` effect on a clip that produces no sound (`include_audio: false`, or media without audio). | warning |
| L12 | At least one enabled clip (for rendering). | error on render |
| L13 | Features the library can't render yet (§14). | error |
| L14 | Transition placement and handles: not on a track's first clip, previous clip directly adjacent and unique, enough source before `in_ms` / after `out_ms`, transitions on one clip don't overlap (§9.2, exact messages there). | error |

Warnings never block loading; the loader returns them alongside the timeline.

---

## 13. Mapping to the Go API

| Format | Go |
|---|---|
| `format` | `timeline.New(timeline.Config{Width, Height, FPS})` |
| video track (array order) | `tl.AddVideoTrack(name)` |
| audio track | `tl.AddAudioTrack(name)` |
| `enabled: false` | track not added (loader-only) |
| `video` clip | `clip.NewVideo…(path)` (see below) `.Trim(in, out)`; `include_audio:false` → `.VideoOnly()` |
| `audio` clip, audio media | `clip.NewAudio…(path).Trim(in, out)` on an `AudioTrack` |
| `audio` clip, video media | `clip.NewVideo…(path).AudioOnly().Trim(in, out)` on an `AudioTrack` |
| `image` clip | `clip.NewImage(path).WithDuration(d)` |
| `color` clip | `clip.NewColor(color, 0, 0).WithDuration(d)` |
| `text` clip | `clip.NewText(text, clip.TextStyle{…}).WithDuration(d)` |
| `at_ms` | `track.Add(c, at)` |
| `fade_in_ms` / `fade_out_ms` | `.WithFadeIn(d)` / `.WithFadeOut(d)` |
| `volume` | `.WithVolume(v)` (only when ≠ 1) |
| effects | §10 table |
| `output.preset` | §11 table; render with `tl.Export(profile, path)` |
| ms → Go | `time.Duration(ms) * time.Millisecond` (exact) |

**Library additions needed by the loader** (phase 3b, all additive):

1. **Build clips from stored probe data, without re-probing.** Today
   `NewVideo/NewAudio` always run ffprobe, and `NewVideoWithDuration` hard-codes
   `hasAudio = true` and records no source length (so `Validate` can't check
   trims). Needed: e.g. `clip.NewVideoFromInfo(path string, info *engine.MediaInfo)`
   and `clip.NewAudioFromInfo(...)`, setting duration, has-audio, size and the
   source length used by `clip.SourceProblem`.
2. A way to report **all** validation problems: `Timeline.Validate` returns the
   first error only. The loader does its own L1–L14 checks, so this is not
   blocking.

---

## 14. Known gaps and loader behavior

The format is defined ahead of the library. Principle: **the loader never
silently renders something different from what the file says.** Anything the
library cannot render yet is a load **error** naming the field, the clip ID and
the reason, so a tool or agent can remove or change it. Purely informational
mismatches are warnings.

| Format feature | Library status | Loader behavior until supported |
|---|---|---|
| `transition_in` | Not implemented: no `xfade`/`acrossfade`, and the compiler can't yet read handle footage beyond a clip's in/out points. The L14 checks (placement, handles) still run first, so a project is only rejected for this once its transitions are otherwise valid. | **error**: `clip "c_beach": transition_in is not supported yet` |
| Clips on upper video tracks (any video track after the first) | Known bug **#1**: upper tracks are opaque black outside their content (gaps, padding, text background). | **error** while bug #1 is open: `track "t_logo": overlay tracks are not supported yet (known bug #1)` |
| `position`, `anchor`, `size` with non-default values | Known bug **#2**: ignored; `overlay` always at 0,0 and every clip is fitted to the canvas. | **error** while bug #2 is open. Default values (centred, full-canvas contain) are fine. |
| `size.fit: "cover"` / `"stretch"` | Not implemented (clips are always contained). | **error** |
| `output.fit: "cover"` | Not implemented (`CompileProfile` always contains). | **error** |
| `text.style.font.family` | `TextStyle` only has a font file; drawtext's `font=` (fontconfig) is not used. | **error** (use `font.file`) |
| `text.style.box`, `outline` | `TextStyle.Background/BorderWidth/BorderColor/BoxPadding` exist but the compiler ignores them. | **error** |
| `text.style.align`, `direction: "ltr"/"rtl"` | Not supported (drawtext auto-detects direction; no alignment option). `auto`/`center` defaults are fine. | **error** for non-default values |
| `text.style.size` | Supported (converted to px). | — |
| Text on the bottom track | Supported, but the text is drawn on an opaque black background (#1 is about upper tracks; on the bottom track black is expected). | — |
| Colors with alpha (`#RRGGBBAA`) on `color` clips | The compiler converts generated clips to `yuv420p`, dropping alpha. | **error** for alpha ≠ `FF` |
| `speed.affect_audio: false` | Possible via `AudioSpeed(1)`, but the sound is then longer/shorter than the clip and is not trimmed/padded, which breaks sequence timing. | **error** |
| Fades on the sound of `video` clips | Supported (bug #7). | — |
| Fades on sped-up clips | Supported (bug #12). | — |
| `enabled: false` | Loader-only. | — |

When bugs #1/#2 and the other gaps are fixed, the corresponding rows switch to
"supported" and the loader stops rejecting them; no format change is needed.
Of the examples, `simple-trim-concat.json` and `podcast-audio.json` render with
today's library; `reel-arabic-overlay.json` (overlays, layout, font family,
box, cover) and `effects-transitions.json` (transitions, `affect_audio: false`)
will be rejected with the errors above until those gaps close.

---

## 15. Versioning and interchange

- `version` is an integer. Additive, backward-compatible changes (new effect
  types, new transition types, new optional fields) keep version 1 **only if**
  older loaders can safely reject them — because loaders reject unknown fields
  and enum values, an old loader fails loudly on a newer file instead of
  misrendering it. Any change in meaning bumps the version and ships a
  migration (`project.Migrate(raw) → v2`).
- **OTIO export (later).** The model maps cleanly onto OpenTimelineIO:
  tracks → `Track` (`kind` Video/Audio); clips → `Clip` with an
  `ExternalReference` to the media path and `source_range` =
  `RationalTime(in_ms, 1000)…`; gaps → `Gap` items (OTIO tracks are
  sequential, so `at_ms` positions become gaps); `transition_in` →
  `Transition` with `in_offset = floor(d/2)` and `out_offset = ceil(d/2)` — OTIO uses the same handle model (transitions sit on the cut and use media beyond the clips' source ranges), so no conversion is needed;
  `speed` → `LinearTimeWarp`; `volume`/`normalize` → effects/metadata;
  layout → metadata (no OTIO equivalent). Millisecond times convert exactly to
  `RationalTime(ms, 1000)`.

---

## 16. Examples

| File | Shows | Renders today? |
|---|---|---|
| [`simple-trim-concat.json`](../examples/simple-trim-concat.json) | Trim + back-to-back sequence, fades, fingerprint | yes |
| [`reel-arabic-overlay.json`](../examples/reel-arabic-overlay.json) | 9:16 reel, Arabic text with box, logo overlay, music with `volume`, `cover` | no (§14) |
| [`podcast-audio.json`](../examples/podcast-audio.json) | Audio-only WAV, sound from a video file, normalize, disabled track, `meta` | yes |
| [`effects-transitions.json`](../examples/effects-transitions.json) | Speed up/slow down, crossfades (video and audio tracks), color clip, normalize + `volume` | no (§14) |

---

## 17. Design decisions (v1)

Decided during review of the phase 3a design (PRs #2 and its follow-up).

| # | Question | Decision |
|---|---|---|
| 1 | ID uniqueness scope | Global across media, tracks and clips. |
| 2 | `media` shape | Array of entries with `id` (same shape as tracks/clips). |
| 3 | Transition model | **Handle model**: butt-joined clips, transition centred on the cut, handles from source beyond in/out (§9). |
| 4 | Volume | Clip field `volume` (linear, default 1) next to the fades; no volume effect. |
| 5 | Volume units | Linear gain. |
| 6 | Text size | Fraction of canvas height. |
| 7 | Default size of upper-track clips | Full canvas, `contain` (matches today's library). |
| 8 | Missing media file | Warning on load, error on render. |
| 9 | Features the library can't render yet | Strict load errors (§14). |
| 10 | `format` in audio-only projects | Always required. |
| 11 | Transitions on audio tracks | Allowed. |
| 12 | `speed.affect_audio: false` | Sound at normal speed, cut at the clip end (or ending early when slowed). |
| 13 | Output | Single `output`, preset only (no overrides) in v1. |
| 14 | fps | Number (loader maps 29.97/59.94/23.976 to exact NTSC rates when supported). |
| 15 | File naming | `*.gomontage.json`. |
| 16 | Schema validation in the loader | Loader embeds the schema and validates every file with it first; the schema is the single source of truth for shape (§12). |
| 17 | Gap/background color | Deferred (gaps are black on the bottom track). |
