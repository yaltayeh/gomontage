# Testing GoMontage

There are two layers of tests.

| Suite | Command | Needs FFmpeg | What it checks |
|---|---|---|---|
| Unit | `go test ./...` or `make test` | No | Graph/command shape, clip/timeline logic |
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
- Renders use `testProfile()` (`mpeg4` + native `aac`), which every FFmpeg
  build has. Tests that need optional pieces (`libx264`, `libx265`,
  `libmp3lame`, the `drawtext` filter) skip with a message naming what is
  missing.
- If `ffmpeg`/`ffprobe` are not on `PATH`, every e2e test skips with a clear
  message.

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

When a bug is fixed, delete the `knownBug(...)` line at the top of its test so
it becomes a permanent regression test.

| # | Bug |
|---|---|
| 1 | Overlay tracks are opaque black outside their content (gaps, tail, pad, text background) |
| 2 | `WithPosition` / `WithSize` / `TextStyle.Position` are ignored |
| 3 | `drawtext` text is not escaped |
| 4 | A timeline with only generated clips fails ("graph has no inputs") |
| 5 | Timeline FPS is not applied to file clips |
| 6 | `Profile.Width/Height` are ignored |
| 7 | Embedded audio of video clips ignores clip fades |
| 8 | `SpeedUp` retimes video only; audio drifts |
| 9 | Missing files / trims past source end are not reported |
| 10 | Audio-only profiles fail on timelines with video; GIF fails with audio |
| 11 | Reusing one image with different durations truncates the output |
