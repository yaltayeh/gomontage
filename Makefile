.PHONY: test test-e2e test-e2e-long test-e2e-bugs test-all

# Fast unit tests (no FFmpeg needed).
test:
	go test ./...

# End-to-end tests against real FFmpeg/ffprobe. Known-bug tests are skipped.
test-e2e:
	go test -tags e2e -count=1 ./e2e/...

# End-to-end tests including slow ones (e.g. a full 20-minute podcast render).
test-e2e-long:
	GOMONTAGE_E2E_LONG=1 go test -tags e2e -count=1 -timeout 20m ./e2e/...

# End-to-end tests including known-bug tests (expected to FAIL until fixed).
test-e2e-bugs:
	GOMONTAGE_KNOWN_BUGS=1 go test -tags e2e -count=1 -v ./e2e/... -run 'KnownBug'

test-all: test test-e2e
