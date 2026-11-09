package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ahmedhodiani/gomontage/project"
)

// formatPreset is a canvas and the render preset that goes with it.
type formatPreset struct {
	width, height int
	fps           float64
	render        string
}

var formatPresets = map[string]formatPreset{
	"reel":          {1080, 1920, 30, "reel"},
	"youtube_720p":  {1280, 720, 30, "youtube_720p"},
	"youtube_1080p": {1920, 1080, 30, "youtube_1080p"},
	"youtube_4k":    {3840, 2160, 30, "youtube_4k"},
}

func formatPresetNames() string {
	names := make([]string, 0, len(formatPresets))
	for n := range formatPresets {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

type createInput struct {
	Path         string  `json:"path" jsonschema:"Where to create the project file. Must end in .gomontage.json; use an absolute path (a relative one is resolved against the server's working directory). Missing directories are created."`
	Preset       string  `json:"preset,omitempty" jsonschema:"Canvas preset: reel (1080x1920, 30 fps), youtube_720p (1280x720), youtube_1080p (1920x1080) or youtube_4k (3840x2160), all 30 fps. Also sets the render preset. Omit to give width, height and fps."`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	FPS          float64 `json:"fps,omitempty"`
	OutputPreset string  `json:"output_preset,omitempty" jsonschema:"Render preset stored in the project (output.preset), e.g. youtube_1080p, reel, h265, prores, gif, mp3, wav, flac. Defaults to the canvas preset; can also be passed to render later."`
	Name         string  `json:"name,omitempty"`
	Overwrite    bool    `json:"overwrite,omitempty" jsonschema:"Replace an existing file at path (its previous content is kept in the history). Default false: an existing file is an error."`
}

func (s *Server) projectCreate(_ context.Context, in createInput) (*mcp.CallToolResult, error) {
	path, err := absPath(in.Path)
	if err != nil {
		return nil, fmt.Errorf("path: %w", err)
	}
	if !strings.HasSuffix(path, project.FileExtension) {
		return nil, fmt.Errorf("path must end in %s (e.g. %s)", project.FileExtension, strings.TrimSuffix(path, filepath.Ext(path))+project.FileExtension)
	}
	f := project.Format{Width: in.Width, Height: in.Height, FPS: in.FPS}
	render := in.OutputPreset
	if in.Preset != "" {
		fp, ok := formatPresets[in.Preset]
		if !ok {
			return nil, fmt.Errorf("unknown preset %q (one of %s; or give width, height and fps)", in.Preset, formatPresetNames())
		}
		if f.Width == 0 {
			f.Width = fp.width
		}
		if f.Height == 0 {
			f.Height = fp.height
		}
		if f.FPS == 0 {
			f.FPS = fp.fps
		}
		if render == "" {
			render = fp.render
		}
	}
	if f.Width == 0 || f.Height == 0 {
		return nil, fmt.Errorf("give a preset (%s) or width and height", formatPresetNames())
	}
	if f.FPS == 0 {
		f.FPS = 30
	}
	if _, err := os.Stat(path); err == nil && !in.Overwrite {
		return nil, fmt.Errorf("%s already exists: work on it with timeline_get/timeline_edit, or pass overwrite:true to replace it", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	p := project.New(filepath.Dir(path), f)
	stem := strings.TrimSuffix(filepath.Base(path), project.FileExtension)
	p.Name = in.Name
	if p.Name == "" {
		p.Name = stem
	}
	if render != "" {
		if _, err := project.PresetProfile(render); err != nil {
			return nil, err
		}
		p.Output = &project.Output{Preset: render, Path: "output/" + stem + defaultExt[render]}
	}
	if _, err := project.Validate(p); err != nil {
		return nil, fmt.Errorf("can't create the project:\n  %s", problemsInline(err))
	}

	e, err := s.store.create(path)
	if err != nil {
		return nil, err
	}
	defer e.release()
	if e.data != nil {
		err = e.save(p) // overwrite: snapshot the old file
	} else {
		err = e.write(p)
	}
	if err != nil {
		return nil, err
	}
	return textResult("created " + path + "\n\n" + timelineText(e, false)), nil
}

type importInput struct {
	Project string   `json:"project"`
	Paths   []string `json:"paths" jsonschema:"Files to import (video, audio or still images). Absolute paths; relative ones are resolved against the server's working directory. The project stores them relative to its own directory."`
}

func (s *Server) mediaImport(_ context.Context, in importInput) (*mcp.CallToolResult, error) {
	if len(in.Paths) == 0 {
		return nil, errors.New("paths is empty: give at least one file to import")
	}
	e, err := s.store.open(in.Project)
	if err != nil {
		return nil, err
	}
	defer e.release()
	p, err := e.valid()
	if err != nil {
		return nil, err
	}
	q := p.Clone()
	var lines []string
	imported, failed := 0, 0
	for _, raw := range in.Paths {
		abs, err := absPath(raw)
		if err != nil {
			lines = append(lines, fmt.Sprintf("✗ %s: %v", raw, err))
			failed++
			continue
		}
		if m := mediaWithPath(q, abs); m != nil {
			lines = append(lines, fmt.Sprintf("= %s · %s (already imported)", m.ID, mediaSummary(m)))
			continue
		}
		id, err := project.ImportMedia(q, abs)
		if err != nil {
			msg := strings.TrimPrefix(err.Error(), fmt.Sprintf("import media %q: ", abs))
			lines = append(lines, fmt.Sprintf("✗ %s: %s", abs, msg))
			failed++
			continue
		}
		lines = append(lines, fmt.Sprintf("+ %s · %s", id, mediaSummary(q.MediaByID(id))))
		imported++
	}
	if imported > 0 {
		if err := e.save(q); err != nil {
			return nil, err
		}
	}
	head := fmt.Sprintf("imported %d, failed %d, already present %d", imported, failed, len(in.Paths)-imported-failed)
	res := textResult(head + "\n" + strings.Join(lines, "\n"))
	if imported == 0 && failed > 0 {
		res.IsError = true
	}
	return res, nil
}

// mediaWithPath returns the media entry whose file is abs, or nil.
func mediaWithPath(p *project.Project, abs string) *project.Media {
	for i := range p.Media {
		if src, err := absPath(p.Resolve(p.Media[i].Path)); err == nil && sameFile(src, abs) {
			return &p.Media[i]
		}
	}
	return nil
}

type getInput struct {
	Project string `json:"project"`
	Detail  bool   `json:"detail,omitempty" jsonschema:"Also print every clip's full JSON as stored in the file. Default false (one summary line per clip)."`
}

func (s *Server) timelineGet(_ context.Context, in getInput) (*mcp.CallToolResult, error) {
	e, err := s.store.open(in.Project)
	if err != nil {
		return nil, err
	}
	defer e.release()
	return textResult(timelineText(e, in.Detail)), nil
}

// editArgs is timeline_edit's input.
type editArgs struct {
	Project string            `json:"project"`
	Ops     []json.RawMessage `json:"ops"`
}

// timelineEditHandler is registered with the low-level AddTool: the server
// decodes and validates ops itself, so every error names its op.
func (s *Server) timelineEditHandler(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("tool panicked", "tool", "timeline_edit", "panic", r)
			res = errorResult(fmt.Errorf("internal error in timeline_edit: %v", r))
		}
		s.log.Info("tool call", "tool", "timeline_edit", "ms", time.Since(start).Milliseconds())
	}()
	var in editArgs
	if err := decodeStrict(req.Params.Arguments, &in); err != nil {
		return errorResult(fmt.Errorf("arguments: %v (expected {\"project\": ..., \"ops\": [...]})", err)), nil
	}
	text, err := s.timelineEdit(in)
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(text), nil
}

func errorResult(err error) *mcp.CallToolResult {
	r := &mcp.CallToolResult{}
	r.SetError(err)
	return r
}

func (s *Server) timelineEdit(in editArgs) (string, error) {
	if in.Project == "" {
		return "", errors.New(`missing "project" (path to the *.gomontage.json file)`)
	}
	if len(in.Ops) == 0 {
		return "", errors.New(`"ops" is empty: give at least one op`)
	}
	ops := make([]editOp, len(in.Ops))
	var errs []string
	for i, raw := range in.Ops {
		op, err := decodeOp(raw)
		if err != nil {
			errs = append(errs, fmt.Sprintf("ops[%d]: %v", i, err))
		}
		ops[i] = op
	}
	if len(errs) > 0 {
		return "", rejected(errs)
	}

	e, err := s.store.open(in.Project)
	if err != nil {
		return "", err
	}
	defer e.release()
	before, err := e.valid()
	if err != nil {
		return "", err
	}
	ed := newEditor(before.Clone())
	for i, op := range ops {
		if err := ed.apply(i, op); err != nil {
			return "", rejected([]string{fmt.Sprintf("ops[%d] (%s): %v", i, op.Op, err)})
		}
	}
	res := project.Check(ed.p)
	if len(res.ProjectErrors) > 0 {
		for _, p := range res.ProjectErrors {
			errs = append(errs, ed.attribute(p))
		}
		return "", rejected(errs)
	}

	changes := diffText(before, ed.p)
	var b strings.Builder
	if len(changes) == 0 {
		b.WriteString("no changes (the ops left the project as it was); file not modified\n")
	} else {
		if err := e.save(ed.p); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "saved · %s · duration %d ms\n", plural(len(ops), "op"), projectDuration(ed.p))
		if len(ed.newIDs) > 0 {
			b.WriteString("new ids: " + strings.Join(ed.newIDs, " · ") + "\n")
		}
		b.WriteString("changes:\n")
		for _, l := range changes {
			b.WriteString("  " + l + "\n")
		}
		for _, n := range ed.notes {
			b.WriteString("  note " + n + "\n")
		}
	}
	if len(res.RenderBlockers) > 0 || len(res.Warnings) > 0 {
		b.WriteString(checkText(res, nil))
	} else {
		b.WriteString("check: ok (renderable)\n")
	}
	return b.String(), nil
}

func rejected(errs []string) error {
	return fmt.Errorf("rejected, nothing was saved:\n  %s", strings.Join(errs, "\n  "))
}

type previewInput struct {
	Project string `json:"project"`
	TimeMS  int64  `json:"time_ms" jsonschema:"The time of the frame on the timeline, in milliseconds (0 to the duration shown by timeline_get, exclusive)."`
	Width   int    `json:"width,omitempty" jsonschema:"Image width in pixels (16-1920; the height follows the canvas aspect ratio). Default 480. Larger images cost more tokens."`
}

func (s *Server) previewFrame(ctx context.Context, in previewInput) (*mcp.CallToolResult, error) {
	w := in.Width
	if w == 0 {
		w = defaultPreviewWidth
	}
	if w < 16 || w > maxPreviewWidth {
		return nil, fmt.Errorf("width %d is out of range (16 to %d)", w, maxPreviewWidth)
	}
	w -= w % 2
	e, err := s.store.open(in.Project)
	if err != nil {
		return nil, err
	}
	defer e.release()
	p, err := e.valid()
	if err != nil {
		return nil, err
	}
	img, cached, err := s.renderPreview(ctx, p, e.data, in.TimeMS, w)
	if err != nil {
		return nil, err
	}
	pw, ph := previewSize(p, w)
	note := fmt.Sprintf("frame at %d ms · %dx%d", in.TimeMS, pw, ph)
	if cached {
		note += " · cached"
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.ImageContent{Data: img, MIMEType: "image/jpeg"},
		&mcp.TextContent{Text: note},
	}}, nil
}

type renderInput struct {
	Project string `json:"project"`
	Preset  string `json:"preset,omitempty" jsonschema:"Render preset, overriding the project's output.preset: youtube_720p, youtube_1080p, youtube_4k, reel, h265, prores, gif, mp3, wav or flac."`
	Output  string `json:"output,omitempty" jsonschema:"Output file, overriding the project's output.path (absolute, or relative to the server's working directory). Its extension must suit the preset. Default: the project's output.path, else <project dir>/output/<project name>.<ext>. It may not be a media source or the project file."`
	DryRun  bool   `json:"dry_run,omitempty" jsonschema:"Return the FFmpeg command that would run, without rendering. Default false."`
}

func (s *Server) render(_ context.Context, in renderInput) (*mcp.CallToolResult, error) {
	e, err := s.store.open(in.Project)
	if err != nil {
		return nil, err
	}
	defer e.release()
	p, err := e.valid()
	if err != nil {
		return nil, err
	}
	snapshot := p.Clone()
	profile, out, err := renderTarget(e.path, snapshot, in.Preset, in.Output)
	if err != nil {
		return nil, err
	}
	tl, err := project.Build(snapshot)
	if err != nil {
		return nil, fmt.Errorf("can't render yet:\n  %s", problemsInline(err))
	}
	preset := in.Preset
	if preset == "" {
		preset = snapshot.Output.Preset
	}
	if in.DryRun {
		cmd, err := tl.DryRun(profile, out)
		if err != nil {
			return nil, err
		}
		return textResult(fmt.Sprintf("dry run (nothing rendered) · preset %s · output %s\n%s", preset, out, cmd.String())), nil
	}
	dur := projectDuration(snapshot)
	j, err := s.jobs.start(e.path, preset, out, dur, tl, profile, snapshot)
	if err != nil {
		return nil, err
	}
	return textResult(fmt.Sprintf("job %s started · preset %s · %d ms of video → %s\nfollow it with render_job {\"job\": %q, \"wait_ms\": 30000}", j.id, preset, dur, out, j.id)), nil
}

type jobInput struct {
	Action string `json:"action,omitempty" jsonschema:"status (default) or cancel."`
	Job    string `json:"job,omitempty" jsonschema:"The job ID returned by render (e.g. r1). Empty with status: list all jobs."`
	WaitMS int64  `json:"wait_ms,omitempty" jsonschema:"status: wait up to this many milliseconds (max 60000) for the job to finish before answering. Default 0 (answer at once)."`
}

const maxWait = 60 * time.Second

func (s *Server) renderJob(ctx context.Context, in jobInput) (*mcp.CallToolResult, error) {
	action := in.Action
	if action == "" {
		action = "status"
	}
	if action != "status" && action != "cancel" {
		return nil, fmt.Errorf("unknown action %q (status or cancel)", action)
	}
	if in.Job == "" {
		if action == "cancel" {
			return nil, errors.New("cancel needs job (the ID returned by render)")
		}
		all := s.jobs.all()
		if len(all) == 0 {
			return textResult("no render jobs in this server session"), nil
		}
		var lines []string
		for _, j := range all {
			lines = append(lines, j.statusText())
		}
		return textResult(strings.Join(lines, "\n")), nil
	}
	j := s.jobs.get(in.Job)
	if j == nil {
		return nil, fmt.Errorf("job %q not found (jobs live only as long as the server process; call render_job without job to list them)", in.Job)
	}
	if action == "cancel" {
		j.cancel()
		select {
		case <-j.done:
		case <-time.After(10 * time.Second):
		}
		return textResult(j.statusText()), nil
	}
	if in.WaitMS > 0 {
		wait := min(time.Duration(in.WaitMS)*time.Millisecond, maxWait)
		select {
		case <-j.done:
		case <-time.After(wait):
		case <-ctx.Done():
		}
	}
	res := textResult(j.statusText())
	return res, nil
}
