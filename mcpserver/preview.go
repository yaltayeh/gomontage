package mcpserver

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ahmedhodiani/gomontage/export"
	"github.com/ahmedhodiani/gomontage/project"
)

const (
	defaultPreviewWidth = 480
	maxPreviewWidth     = 1920
	previewCacheSize    = 64
)

// previewKey identifies a rendered frame: the project file's content (as
// saved; it includes each media file's fingerprint), the time and the
// width.
type previewKey struct {
	hash   [32]byte
	timeMS int64
	width  int
}

// previewCache is a small LRU of rendered JPEG frames.
type previewCache struct {
	mu    sync.Mutex
	order *list.List // of previewKey, most recent first
	items map[previewKey]*list.Element
	data  map[previewKey][]byte
	hits  int
}

func newPreviewCache() *previewCache {
	return &previewCache{order: list.New(), items: map[previewKey]*list.Element{}, data: map[previewKey][]byte{}}
}

func (c *previewCache) get(k previewKey) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	c.hits++
	return c.data[k], true
}

func (c *previewCache) put(k previewKey, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		c.order.MoveToFront(el)
		c.data[k] = b
		return
	}
	c.items[k] = c.order.PushFront(k)
	c.data[k] = b
	for c.order.Len() > previewCacheSize {
		old := c.order.Remove(c.order.Back()).(previewKey)
		delete(c.items, old)
		delete(c.data, old)
	}
}

// previewWindow returns the part [start, end) of the timeline that must be
// rendered to get the frame at t exactly: about one frame, widened so no
// clip is cut inside a fade (SubRange keeps a fade it cuts through at full
// length) and so every transition window that touches the frame is whole
// (SubRange turns a cut transition into a plain cut).
func previewWindow(p *project.Project, t, total int64) (int64, int64) {
	frame := int64(math.Ceil(1000 / p.Format.FPS))
	start, end := t, min(t+2*frame, total)
	for changed := true; changed; {
		changed = false
		widen := func(s, e int64) {
			if s < start {
				start, changed = max(s, 0), true
			}
			if e > end {
				end, changed = min(e, total), true
			}
		}
		for ti := range p.Tracks {
			tr := &p.Tracks[ti]
			if !tr.IsEnabled() || tr.Kind != project.KindVideo {
				continue
			}
			for ci := range tr.Clips {
				c := &tr.Clips[ci]
				a, b := c.AtMS, clipEnd(p, c)
				if tt := c.TransitionIn; tt != nil {
					ws, we := a-tt.Before(), a+tt.After()
					if ws < end && we > start {
						widen(ws, we)
					}
				}
				if a >= end || b <= start {
					continue
				}
				if c.FadeInMS > 0 && start > a && start < a+c.FadeInMS {
					widen(a, end)
				}
				if c.FadeOutMS > 0 && end < b && end > b-c.FadeOutMS {
					widen(start, b)
				}
			}
		}
	}
	return start, end
}

// visibleAt reports whether any enabled video-track clip covers [start, end).
func visibleAt(p *project.Project, start, end int64) bool {
	for ti := range p.Tracks {
		tr := &p.Tracks[ti]
		if !tr.IsEnabled() || tr.Kind != project.KindVideo {
			continue
		}
		for ci := range tr.Clips {
			c := &tr.Clips[ci]
			if c.AtMS < end && clipEnd(p, c) > start {
				return true
			}
		}
	}
	return false
}

func previewSize(p *project.Project, width int) (int, int) {
	h := int(math.Round(float64(width)*float64(p.Format.Height)/float64(p.Format.Width)/2)) * 2
	return width, max(h, 2)
}

// renderPreview returns the JPEG frame at timeMS, width pixels wide (the
// height follows the canvas aspect ratio). data is the project file's
// content, used as the cache key.
func (s *Server) renderPreview(ctx context.Context, p *project.Project, data []byte, timeMS int64, width int) ([]byte, bool, error) {
	total := projectDuration(p)
	if timeMS < 0 || timeMS >= total {
		return nil, false, fmt.Errorf("time_ms %d is outside the timeline (0 to %d ms; the last frame starts just before %d)", timeMS, max(total-1, 0), total)
	}
	key := previewKey{sha256.Sum256(data), timeMS, width}
	if b, ok := s.previews.get(key); ok {
		return b, true, nil
	}
	w, h := previewSize(p, width)

	// Sound never matters for a frame: drop audio tracks (so a missing
	// music file doesn't block the preview) and build without sound.
	q := p.Clone()
	tracks := q.Tracks[:0]
	for _, t := range q.Tracks {
		if t.Kind == project.KindVideo {
			tracks = append(tracks, t)
		}
	}
	q.Tracks = tracks

	start, end := previewWindow(q, timeMS, total)
	var out []byte
	if !visibleAt(q, timeMS, timeMS+1) {
		out = blackJPEG(w, h)
	} else {
		tl, err := project.BuildWith(q, project.BuildOptions{VideoOnly: true})
		if err != nil {
			return nil, false, fmt.Errorf("can't preview: %s", problemsInline(err))
		}
		sub := tl.SubRange(ms(start), ms(end))
		dir, err := os.MkdirTemp("", "gomontage-preview-")
		if err != nil {
			return nil, false, err
		}
		defer os.RemoveAll(dir)
		file := filepath.Join(dir, "frame.jpg")
		extra := map[string]string{"-frames:v": "1", "-q:v": "4", "-update": "1"}
		if off := timeMS - start; off > 0 {
			extra["-ss"] = strconv.FormatFloat(float64(off)/1000, 'f', 3, 64)
		}
		profile := &export.Profile{
			Name: "preview frame", VideoCodec: "mjpeg", PixelFormat: "yuvj420p", CRF: -1,
			Width: w, Height: h, Format: "image2", ExtraArgs: extra,
		}
		if err := sub.ExportSilentContext(ctx, profile, file, nil); err != nil {
			return nil, false, fmt.Errorf("preview failed: %s", summarizeError(err, p))
		}
		if out, err = os.ReadFile(file); err != nil || len(out) == 0 {
			return nil, false, fmt.Errorf("preview failed: FFmpeg wrote no frame at %d ms", timeMS)
		}
	}
	s.previews.put(key, out)
	return out, false, nil
}

func ms(v int64) time.Duration { return time.Duration(v) * time.Millisecond }

// blackJPEG is the frame shown where no video clip is on screen.
func blackJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}
