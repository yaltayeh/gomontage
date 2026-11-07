package clip

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/ahmedhodiani/gomontage/engine"
)

// LoadVideo is like NewVideo but returns an error if the file is missing,
// can't be probed, or has no video stream.
//
// Example:
//
//	interview, err := clip.LoadVideo("resources/video/interview.mp4")
//	if err != nil {
//	    log.Fatal(err)
//	}
func LoadVideo(path string) (*VideoClip, error) {
	info, err := probeSource("video", path)
	if err != nil {
		return nil, err
	}
	if !info.HasVideo() {
		return nil, fmt.Errorf("clip: video %q: file has no video stream", path)
	}
	c := &VideoClip{Base: Base{
		clipType:   TypeVideo,
		sourcePath: path,
		volume:     1.0,
		hasVideo:   true,
	}}
	c.applyVideoInfo(info)
	return c, nil
}

// LoadAudio is like NewAudio but returns an error if the file is missing,
// can't be probed, or has no audio stream.
func LoadAudio(path string) (*AudioClip, error) {
	info, err := probeSource("audio", path)
	if err != nil {
		return nil, err
	}
	if !info.HasAudio() {
		return nil, fmt.Errorf("clip: audio %q: file has no audio stream", path)
	}
	c := NewAudioWithDuration(path, info.Duration)
	c.sourceDuration = info.Duration
	return c, nil
}

// LoadImage is like NewImage but returns an error if the file is missing or
// isn't a readable image. The clip has the default 5s duration; use
// WithDuration to change it.
func LoadImage(path string) (*ImageClip, error) {
	info, err := probeSource("image", path)
	if err != nil {
		return nil, err
	}
	if !info.HasVideo() {
		return nil, fmt.Errorf("clip: image %q: file is not a readable image", path)
	}
	return NewImage(path), nil
}

// probeSource checks that path exists and probes it, returning errors that
// name the file and the problem.
func probeSource(kind, path string) (*engine.MediaInfo, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("clip: %s %q: file not found", kind, path)
		}
		return nil, fmt.Errorf("clip: %s %q: %w", kind, path, err)
	}
	info, err := engine.Probe(path)
	if err != nil {
		return nil, fmt.Errorf("clip: %s %q: cannot read media (is it a valid media file?): %w", kind, path, err)
	}
	return info, nil
}

// sourceEndTolerance absorbs container rounding when comparing a clip's
// source window to the probed source length.
const sourceEndTolerance = 10 * time.Millisecond

// SourceProblem reports a problem with the clip's source file that would
// make the render wrong, or nil:
//
//   - the source could not be probed when the clip was created with
//     NewVideo/NewAudio (missing or unreadable file), or
//   - the clip's source window (Trim, AudioClip.WithDuration) ends after
//     the end of the probed source.
//
// Clips whose source length is unknown (images, generated clips, and clips
// built with New*WithDuration) only fail the first check if it applies.
// Timeline.Validate calls this for every clip.
func SourceProblem(c Clip) error {
	b := c.base()
	if b.sourceErr != nil {
		if _, err := os.Stat(b.sourcePath); errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("source %q: file not found", b.sourcePath)
		}
		return fmt.Errorf("source %q could not be read: %v", b.sourcePath, b.sourceErr)
	}
	if b.sourceDuration > 0 && b.trimEnd > b.sourceDuration+sourceEndTolerance {
		return fmt.Errorf("source %q is %.3fs long but the clip uses it up to %.3fs (check Trim/WithDuration)",
			b.sourcePath, b.sourceDuration.Seconds(), b.trimEnd.Seconds())
	}
	return nil
}

// SourceDuration returns the probed length of c's source file, or 0 if it is
// unknown (images, generated clips, clips built with New*WithDuration, or a
// source that couldn't be probed). Timeline validation uses it to check that
// a transition's handles fit in the source.
func SourceDuration(c Clip) time.Duration {
	return c.base().sourceDuration
}
