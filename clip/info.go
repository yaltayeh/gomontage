package clip

import "github.com/ahmedhodiani/gomontage/engine"

// NewVideoFromInfo creates a VideoClip from already-probed stream information,
// without running ffprobe. It is what NewVideo does after probing: duration,
// has-audio, width/height and the source length (checked by SourceProblem
// and Timeline.Validate) all come from info.
//
// Use it when probe results are cached, e.g. in a project file's media bin.
// info must describe the file at path; info.Path is ignored.
func NewVideoFromInfo(path string, info *engine.MediaInfo) *VideoClip {
	c := &VideoClip{Base: Base{
		clipType:   TypeVideo,
		sourcePath: path,
		volume:     1.0,
		hasVideo:   true,
	}}
	c.applyVideoInfo(info)
	return c
}

// NewAudioFromInfo creates an AudioClip from already-probed stream
// information, without running ffprobe. Duration and the source length come
// from info.Duration.
func NewAudioFromInfo(path string, info *engine.MediaInfo) *AudioClip {
	c := NewAudioWithDuration(path, info.Duration)
	c.sourceDuration = info.Duration
	return c
}
