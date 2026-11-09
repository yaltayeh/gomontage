package export

import (
	"path/filepath"
	"strings"
)

// Container formats (FFmpeg -f names) and file extensions that can only hold
// audio, or only video.
var (
	audioOnlyFormats = map[string]bool{
		"mp3": true, "wav": true, "flac": true, "ogg": true, "oga": true,
		"opus": true, "ipod": true, "m4a": true, "adts": true, "aac": true,
		"aiff": true, "aif": true,
	}
	videoOnlyFormats = map[string]bool{
		"gif": true, "apng": true,
		// Still images (one frame or a numbered sequence).
		"image2": true, "jpg": true, "jpeg": true, "png": true, "webp": true,
	}
)

// OutputStreams reports which stream types the profile's output can carry.
// The container is taken from Profile.Format, or from the extension of
// outputPath when Format is empty.
//
//   - Audio-only containers (mp3, wav, flac, ogg, opus, m4a, aac, aiff):
//     video=false, audio=true.
//   - Video-only containers (gif, apng, and still images: image2, jpg, png,
//     webp): video=true, audio=false.
//   - Anything else (mp4, mov, mkv, webm, ...): both true.
//
// The timeline compiler maps only the streams reported here, and returns an
// error before running FFmpeg if the profile can only carry a stream type the
// timeline doesn't produce.
func (p *Profile) OutputStreams(outputPath string) (video, audio bool) {
	f := strings.ToLower(p.Format)
	if f == "" {
		f = strings.TrimPrefix(strings.ToLower(filepath.Ext(outputPath)), ".")
	}
	switch {
	case audioOnlyFormats[f]:
		return false, true
	case videoOnlyFormats[f]:
		return true, false
	default:
		return true, true
	}
}
