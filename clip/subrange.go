package clip

import "time"

// SubRange returns a new Clip containing only the portion visible within the
// given window [start, end) in clip-local time (relative to the clip's start,
// not the timeline). The returned clip's Duration equals end - start.
//
// For file-backed clips (video, audio), the source trim window is adjusted so
// FFmpeg reads only the needed frames. For generated clips (color, text, image),
// the duration is shortened.
//
// start and end are clamped to [0, clip.Duration()]. If the window is empty
// (start >= end after clamping), nil is returned.
//
// Effects, volume, layout and other properties are preserved on the returned
// clip. Fades are kept unless the window leaves them out entirely (a
// fade-in that ends before start, a fade-out that starts at or after end),
// so the piece doesn't fade at the cut.
func SubRange(c Clip, start, end time.Duration) Clip {
	dur := c.Duration()

	// Clamp to valid range.
	if start < 0 {
		start = 0
	}
	if end > dur {
		end = dur
	}
	if start >= end {
		return nil
	}

	// If the window covers the full clip, return as-is (no work needed).
	if start == 0 && end == dur {
		return c
	}

	switch v := c.(type) {
	case *VideoClip:
		n := subRangeVideo(v, start, end)
		dropCutFades(&n.Base, start, end, dur)
		return n
	case *AudioClip:
		n := subRangeAudio(v, start, end)
		dropCutFades(&n.Base, start, end, dur)
		return n
	case *ImageClip:
		n := subRangeImage(v, end-start)
		dropCutFades(&n.Base, start, end, dur)
		return n
	case *ColorClip:
		n := subRangeColor(v, end-start)
		dropCutFades(&n.Base, start, end, dur)
		return n
	case *TextClip:
		n := subRangeText(v, end-start)
		dropCutFades(&n.Base, start, end, dur)
		return n
	default:
		return nil
	}
}

// dropCutFades removes the fades the window [start, end) of a clip of
// length dur leaves out entirely: a fade-in that ends before start, a
// fade-out that starts at or after end. Without this the piece would fade in
// (or out) again at the cut. A fade the window cuts through is kept with its
// full length, so it restarts at the cut (a partial fade can't be
// expressed); callers that need exact pixels there, such as a one-frame
// preview, should widen the window to the clip's edge.
func dropCutFades(b *Base, start, end, dur time.Duration) {
	if b.fadeIn > 0 && start >= b.fadeIn {
		b.fadeIn = 0
	}
	if b.fadeOut > 0 && end <= dur-b.fadeOut {
		b.fadeOut = 0
	}
}

// subRangeVideo trims a video clip to the given clip-local window.
// It translates the local window into source-file coordinates.
func subRangeVideo(c *VideoClip, start, end time.Duration) *VideoClip {
	n := &VideoClip{Base: *c.base()}

	// Translate clip-local time to source-file time.
	// For clips with speed effects, we need the source-time ratio.
	// TrimStart/TrimEnd are always in source coordinates.
	// Duration() accounts for speed effects.
	//
	// sourceWindow = TrimEnd - TrimStart (always, regardless of speed)
	// localDuration = Duration() (accounts for speed)
	// ratio = sourceWindow / localDuration
	//
	// So to convert local time to source time:
	//   sourceTime = TrimStart + localTime * ratio
	localDur := c.Duration()
	sourceStart := c.TrimStart()
	sourceEnd := c.TrimEnd()
	sourceWindow := sourceEnd - sourceStart

	if localDur > 0 && sourceWindow > 0 {
		ratio := float64(sourceWindow) / float64(localDur)
		n.trimStart = sourceStart + time.Duration(float64(start)*ratio)
		n.trimEnd = sourceStart + time.Duration(float64(end)*ratio)
	} else {
		n.trimStart = sourceStart + start
		n.trimEnd = sourceStart + end
	}

	n.duration = end - start
	n.trimmed = true

	return n
}

// subRangeAudio trims an audio clip to the given clip-local window.
func subRangeAudio(c *AudioClip, start, end time.Duration) *AudioClip {
	n := &AudioClip{Base: *c.base()}

	localDur := c.Duration()
	sourceStart := c.TrimStart()
	sourceEnd := c.TrimEnd()
	sourceWindow := sourceEnd - sourceStart

	if localDur > 0 && sourceWindow > 0 {
		ratio := float64(sourceWindow) / float64(localDur)
		n.trimStart = sourceStart + time.Duration(float64(start)*ratio)
		n.trimEnd = sourceStart + time.Duration(float64(end)*ratio)
	} else {
		n.trimStart = sourceStart + start
		n.trimEnd = sourceStart + end
	}

	n.duration = end - start
	n.trimmed = true

	return n
}

// subRangeImage shortens an image clip to the given duration.
func subRangeImage(c *ImageClip, dur time.Duration) *ImageClip {
	n := &ImageClip{Base: *c.base()}
	n.duration = dur
	n.trimEnd = dur
	return n
}

// subRangeColor shortens a color clip to the given duration.
func subRangeColor(c *ColorClip, dur time.Duration) *ColorClip {
	n := &ColorClip{Base: *c.base(), Color: c.Color}
	n.duration = dur
	n.trimEnd = dur
	return n
}

// subRangeText shortens a text clip to the given duration.
func subRangeText(c *TextClip, dur time.Duration) *TextClip {
	n := &TextClip{Base: *c.base(), Text: c.Text, Style: c.Style}
	n.duration = dur
	n.trimEnd = dur
	return n
}
