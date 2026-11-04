// Package e2e contains end-to-end tests that render real media with FFmpeg
// and inspect the output with ffprobe.
//
// All test files in this package are behind the "e2e" build tag so that a
// plain `go test ./...` stays fast and does not require FFmpeg. Run with:
//
//	go test -tags e2e ./e2e/...
//	GOMONTAGE_KNOWN_BUGS=1 go test -tags e2e ./e2e/...   # also run known-bug tests
//
// See TESTING.md at the repository root for details.
//
// This file exists only so the package is never empty when the e2e tag is
// absent; it contains no code.
package e2e
