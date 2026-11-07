# Testing GoMontage

There are two layers of tests.

| Suite | Command | Needs FFmpeg | What it checks |
|---|---|---|---|
| Unit | `go test ./...` or `make test` | No | Graph/command shape, clip/timeline logic. `timeline/testdata/no_transitions.golden` holds the FFmpeg commands of timelines without transitions, recorded before transitions were implemented: they must stay byte-identical (regenerate with `GOMONTAGE_UPDATE_GOLDEN=1` only for an intended change) |
| Project package | `go test ./project/` (part of `go test ./...`) | No | Loader rules and exact messages (each `schema/testdata/loader` fixture produces exactly its recorded error), canonical Save (round trip, byte-stable, defaults omitted, examples are canonical), Build (DryRun of `simple-trim-concat` equals the hand-written Go timeline; every example builds with no render blockers; transitions map to timeline transitions) |
| Project schema | `go test ./schema/` (part of `go test ./...`) | No | `schema/project.v1.schema.json` compiles, every file in `examples/` validates, invalid projects are rejected with useful errors, and the loader-level fixtures in `schema/testdata/loader/` are schema-valid and declare their expected loader error |
| End-to-end | `go test -tags e2e ./e2e/...` or `make test-e2e` | Yes | Real renders: durations, pixel colors, audio levels, codecs |

## End-to-end suite (`e2e/`)

All files in `e2e/` except `doc.go` carry the `//go:build e2e` tag, so the
plain `go test ./...` stays fast and never calls FFmpeg.

- **Fixtures** are generated at test time with FFmpeg's lavfi sources
  (`testsrc2`, `color`, `sine`, `geq`) into `t.TempDir()`. No binary fixtures
  are committed.
- **Helpers** (`e2e/harness_test.go`): `probe` (independent ffprobe parsing,
  not `engine.Probe`), `pixelAt` / `assertPixel` (extract the frame at time T,
  read RGB at (x,y) with tolerance), `audioLevel` (volumedetect over a time
  window), `assertDuration` (±1.5 frames, min 40ms).
- Text tests use the committed font `testdata/fonts/ElMessiri.ttf` (OFL; the
  one binary fixture), by file and — through a generated fontconfig
  configuration (`FONTCONFIG_FILE`) — by family name, so results don't depend
  on the fonts installed.
- Renders use `testProfile()` (`mpeg4` + native `aac`), which every FFmpeg
  build has. Tests that need optional pieces (`libx264`, `libx265`,
  `libmp3lame`, the `drawtext` filter) skip with a message naming what is
  missing.
- If `ffmpeg`/`ffprobe` are not on `PATH`, every e2e test skips with a clear
  message.
- Transition tests (`e2e/transitions_test.go`) check the blend at the
  midpoint and the pure colors outside the window, that the output length is
  unchanged, that handle frames come from beyond the trim points (sources
  whose color changes over time), speed-scaled handles, audio crossfade levels
  and A/V sync over several transitions, upper-track logos, and audio tracks.
  `TestProject_RenderTransitionsExample` renders
  `examples/effects-transitions.json`.

### Known-bug tests

`e2e/knownbugs_test.go` contains `TestKnownBug_<N>_<Name>` tests. Each asserts
the **correct** behavior for a bug that exists today, so each fails today.
They are skipped by default to keep the suite green.

```bash
# Default: known bugs skipped, suite is green.
make test-e2e

# Run the known-bug tests (they are expected to FAIL until fixed).
GOMONTAGE_KNOWN_BUGS=1 go test -tags e2e -v ./e2e/... -run KnownBug
# or
make test-e2e-bugs
```

Slow end-to-end tests (currently the full 20-minute render of
`examples/podcast-audio.json`, about a minute because `loudnorm` resamples to
192 kHz) only run with `GOMONTAGE_E2E_LONG=1` or `make test-e2e-long`; by
default that test renders the first and last 30 seconds.

When a bug is fixed, delete the `knownBug(...)` line at the top of its test so
it becomes a permanent regression test.

| # | Bug | Status |
|---|---|---|
| 1 | Overlay tracks are opaque black outside their content (gaps, tail, pad, text background) | fixed |
| 2 | `WithPosition` / `WithSize` / `TextStyle.Position` are ignored | fixed |
| 3 | `drawtext` text is not escaped | fixed |
| 4 | A timeline with only generated clips fails ("graph has no inputs") | fixed |
| 5 | Timeline FPS is not applied to file clips | fixed |
| 6 | `Profile.Width/Height` are ignored | fixed |
| 7 | Embedded audio of video clips ignores clip fades | fixed |
| 8 | `SpeedUp` retimes video only; audio drifts | fixed |
| 9 | Missing files / trims past source end are not reported | fixed |
| 10 | Audio-only profiles fail on timelines with video; GIF fails with audio | fixed |
| 11 | Reusing one image with different durations truncates the output | fixed |
| 12 | Fades on sped-up clips are applied in source time (wrong place) | fixed |

Fixed bugs keep their `TestKnownBug_<N>_*` test, which now runs in the
default mode as a regression test.
