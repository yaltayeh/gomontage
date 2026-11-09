package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ahmedhodiani/gomontage/engine"
	"github.com/ahmedhodiani/gomontage/export"
	"github.com/ahmedhodiani/gomontage/project"
	"github.com/ahmedhodiani/gomontage/timeline"
)

// Job states.
const (
	jobRunning   = "running"
	jobDone      = "done"
	jobFailed    = "failed"
	jobCancelled = "cancelled"
)

// job is one asynchronous render. It renders a timeline built from a
// snapshot of the project, so edits made while it runs don't affect it.
type job struct {
	id         string
	project    string
	output     string
	preset     string
	durationMS int64
	started    time.Time
	cancel     context.CancelFunc
	done       chan struct{}

	mu       sync.Mutex
	state    string
	percent  float64
	eta      time.Duration
	finished time.Time
	err      string
	size     int64
}

type jobs struct {
	mu   sync.Mutex
	next int
	byID map[string]*job
}

func newJobs() *jobs { return &jobs{byID: map[string]*job{}} }

func (js *jobs) get(id string) *job {
	js.mu.Lock()
	defer js.mu.Unlock()
	return js.byID[id]
}

func (js *jobs) all() []*job {
	js.mu.Lock()
	defer js.mu.Unlock()
	out := make([]*job, 0, len(js.byID))
	for _, j := range js.byID {
		out = append(out, j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].started.Before(out[b].started) })
	return out
}

// cancelAll stops every running job (server shutdown).
func (js *jobs) cancelAll() {
	for _, j := range js.all() {
		j.cancel()
		<-j.done
	}
}

// defaultExt is the output file extension used for a preset when no output
// path is given.
var defaultExt = map[string]string{
	"youtube_720p": ".mp4", "youtube_1080p": ".mp4", "youtube_4k": ".mp4", "reel": ".mp4",
	"h265": ".mp4", "prores": ".mov", "gif": ".gif", "mp3": ".mp3", "wav": ".wav", "flac": ".flac",
}

// renderTarget resolves the profile and the absolute output path for p.
// output (absolute or relative to the working directory) and preset
// override the project's output section; with neither an output path given
// nor one in the project, the output goes to <project dir>/output/<project
// file name><ext>. The output may not be the project file or any media
// file of the project.
func renderTarget(projectPath string, p *project.Project, preset, output string) (*export.Profile, string, error) {
	if preset == "" && p.Output != nil {
		preset = p.Output.Preset
	}
	if preset == "" {
		return nil, "", fmt.Errorf("the project has no output preset: pass preset (one of %s)", strings.Join(project.Presets(), ", "))
	}
	if _, err := project.PresetProfile(preset); err != nil {
		return nil, "", err
	}
	var path string
	switch {
	case output != "":
		abs, err := absPath(output)
		if err != nil {
			return nil, "", err
		}
		path = abs
	case p.Output != nil && p.Output.Path != "":
		path = p.Resolve(p.Output.Path)
	default:
		stem := strings.TrimSuffix(filepath.Base(projectPath), project.FileExtension)
		stem = strings.TrimSuffix(stem, filepath.Ext(stem))
		path = filepath.Join(p.Dir(), "output", stem+defaultExt[preset])
	}
	path, err := absPath(path)
	if err != nil {
		return nil, "", err
	}
	profile, path, err := project.RenderTarget(p, preset, path)
	if err != nil {
		return nil, "", err
	}
	if sameFile(path, projectPath) {
		return nil, "", fmt.Errorf("output %s is the project file itself; choose another output path", path)
	}
	for i := range p.Media {
		if src, err := absPath(p.Resolve(p.Media[i].Path)); err == nil && sameFile(path, src) {
			return nil, "", fmt.Errorf("output %s is the source file of media %q; rendering would overwrite it. Choose another output path", path, p.Media[i].ID)
		}
	}
	return profile, path, nil
}

// start runs tl into path in the background and returns the job.
func (js *jobs) start(projectPath, preset, path string, durationMS int64, tl *timeline.Timeline, profile *export.Profile, p *project.Project) (*job, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("cannot create the output directory: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	js.mu.Lock()
	js.next++
	j := &job{
		id: fmt.Sprintf("r%d", js.next), project: projectPath, output: path, preset: preset,
		durationMS: durationMS, started: time.Now(), cancel: cancel, done: make(chan struct{}), state: jobRunning,
	}
	js.byID[j.id] = j
	js.mu.Unlock()

	// Render next to the output and rename on success, so a failed or
	// cancelled render never leaves a broken file where the old one was.
	ext := filepath.Ext(path)
	tmp := strings.TrimSuffix(path, ext) + ".rendering-" + j.id + ext
	go func() {
		defer close(j.done)
		defer cancel()
		err := tl.ExportSilentContext(ctx, profile, tmp, func(pr engine.Progress) {
			j.mu.Lock()
			if pr.Percent > j.percent {
				j.percent = min(pr.Percent, 99.9)
			}
			j.eta = pr.ETA
			j.mu.Unlock()
		})
		if err == nil {
			err = os.Rename(tmp, path)
		}
		j.mu.Lock()
		defer j.mu.Unlock()
		j.finished = time.Now()
		switch {
		case err == nil:
			j.state, j.percent, j.eta = jobDone, 100, 0
			if st, e := os.Stat(path); e == nil {
				j.size = st.Size()
			}
		case errors.Is(err, context.Canceled):
			os.Remove(tmp)
			j.state = jobCancelled
		default:
			os.Remove(tmp)
			j.state = jobFailed
			j.err = summarizeError(err, p)
		}
	}()
	return j, nil
}

// statusText reports a job in one or two lines.
func (j *job) statusText() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch j.state {
	case jobRunning:
		s := fmt.Sprintf("%s running · %.0f%% · elapsed %d ms", j.id, j.percent, time.Since(j.started).Milliseconds())
		if j.eta > 0 {
			s += fmt.Sprintf(" · eta %d ms", j.eta.Milliseconds())
		}
		return s + " · output " + j.output
	case jobDone:
		return fmt.Sprintf("%s done · 100%% · output %s (%s, %d ms of video) · took %d ms", j.id, j.output, humanSize(j.size), j.durationMS, j.finished.Sub(j.started).Milliseconds())
	case jobCancelled:
		return fmt.Sprintf("%s cancelled after %d ms · no output written (%s)", j.id, j.finished.Sub(j.started).Milliseconds(), j.output)
	}
	return fmt.Sprintf("%s failed after %d ms · output %s\n%s", j.id, j.finished.Sub(j.started).Milliseconds(), j.output, j.err)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

var (
	ffmpegErrLine = regexp.MustCompile(`(?i)error|invalid|no such|not found|unable|cannot|could not|failed|unknown|permission|does not|unsupported|too many|out of`)
	ffmpegNoise   = regexp.MustCompile(`(?i)^conversion failed!?$|^\s*$|^exiting normally|error while (?:decoding|filtering) stream.*$`)
)

// errorHints maps an FFmpeg message to advice an agent can act on.
var errorHints = []struct {
	re   *regexp.Regexp
	hint string
}{
	{regexp.MustCompile(`(?i)no such file|does not exist`), "a media or font file is missing: check paths with timeline_get (render blockers) or re-import the file"},
	{regexp.MustCompile(`(?i)invalid data found|moov atom not found|could not find codec parameters`), "a media file is damaged or not a supported format: re-export it or replace the media"},
	{regexp.MustCompile(`(?i)unknown encoder|encoder not found`), "this FFmpeg build lacks the preset's encoder: pick another preset (e.g. one using a different codec) or install a full FFmpeg"},
	{regexp.MustCompile(`(?i)permission denied|read-only file system`), "the output location isn't writable: pass another output path"},
	{regexp.MustCompile(`(?i)no space left`), "the disk is full"},
	{regexp.MustCompile(`(?i)no such filter|filter not found`), "this FFmpeg build lacks a filter the project needs (e.g. drawtext for text clips): install an FFmpeg built with it"},
	{regexp.MustCompile(`(?i)font|fontconfig`), "a text clip's font can't be loaded: set style.font.file to a .ttf/.otf file"},
	{regexp.MustCompile(`(?i)cannot allocate memory|out of memory`), "FFmpeg ran out of memory: render a smaller preset or fewer overlays"},
}

// summarizeError turns a render error into a few lines: the relevant FFmpeg
// messages (not the whole log), the media and clips they mention, and a
// hint.
func summarizeError(err error, p *project.Project) string {
	var fe *engine.FFmpegError
	if !errors.As(err, &fe) {
		return "error: " + err.Error()
	}
	var picked []string
	for _, l := range fe.StderrTail {
		l = strings.TrimSpace(l)
		if ffmpegErrLine.MatchString(l) && !ffmpegNoise.MatchString(l) && !contains(picked, l) {
			picked = append(picked, l)
		}
	}
	if len(picked) == 0 && len(fe.StderrTail) > 0 {
		picked = fe.StderrTail[max(0, len(fe.StderrTail)-2):]
	}
	if len(picked) > 4 {
		picked = picked[len(picked)-4:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ffmpeg failed (%v):\n", fe.Err)
	for _, l := range picked {
		if len(l) > 300 {
			l = l[:300] + "…"
		}
		b.WriteString("  " + l + "\n")
	}
	joined := strings.Join(picked, "\n")
	if p != nil {
		for i := range p.Media {
			m := &p.Media[i]
			if strings.Contains(joined, filepath.Base(m.Path)) {
				var clips []string
				for ti := range p.Tracks {
					for ci := range p.Tracks[ti].Clips {
						if p.Tracks[ti].Clips[ci].Media == m.ID {
							clips = append(clips, p.Tracks[ti].Clips[ci].ID+" on "+p.Tracks[ti].ID)
						}
					}
				}
				fmt.Fprintf(&b, "  involves media %s (%s), used by %s\n", m.ID, m.Path, strings.Join(clips, ", "))
			}
		}
	}
	for _, h := range errorHints {
		if h.re.MatchString(joined) {
			b.WriteString("hint: " + h.hint + "\n")
			break
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
