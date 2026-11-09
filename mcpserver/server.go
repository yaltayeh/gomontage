// Package mcpserver is the GoMontage MCP server: tools that let an agent
// (such as Claude Code) create, edit, preview and render GoMontage project
// files. cmd/gomontage-mcp runs it over stdio; see docs/mcp.md.
//
// The tools are stateless: every call names its project file, and the file
// is the state. The server caches loaded projects (reloading files that
// changed on disk), serializes calls per project, saves every edit at once
// with project.Save, and snapshots the previous file into
// .gomontage/history/ next to it.
//
// Nothing in the server writes to stdout, which carries the protocol; logs
// go to the logger given in Options (stderr in cmd/gomontage-mcp).
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ahmedhodiani/gomontage/docs"
	"github.com/ahmedhodiani/gomontage/project"
	"github.com/ahmedhodiani/gomontage/schema"
)

// Instructions are sent to the client at initialization: the rules an
// agent needs before calling any tool.
const Instructions = `GoMontage edits video projects stored as *.gomontage.json files and renders them with FFmpeg.

Rules:
- Every tool except project_create takes "project": the absolute path of the project file. The file is the state. Every edit is saved immediately, and the previous version is kept in .gomontage/history/ next to the file.
- All times are integer milliseconds (*_ms), in inputs and in outputs. Never seconds: 2.5 s is 2500.
- Take IDs from timeline_get (media, tracks and clips share one ID namespace). Call it when you start on a project and whenever the file may have changed; never guess IDs.
- Usual order: project_create, media_import, timeline_edit, timeline_get, preview_frame (sparingly), render, then render_job.
- Batch related changes into ONE timeline_edit call. Its ops apply in order, all or nothing, and later ops can use IDs created by earlier ones (set new_id).
- Layers: the first video track in the list is the bottom layer, and later video tracks draw on top of it (titles, logos, picture-in-picture). Audio tracks are mixed together.
- Ripple: trim or remove with ripple:true shifts the later clips on the same track only. Other tracks (music, titles) never move; shift them yourself if they must stay in sync.
- Transitions: transition_in on the incoming clip, e.g. {"type":"crossfade","duration_ms":1000}. Clips stay butt-joined (the previous clip must end exactly at this clip's at_ms). The crossfade is centred on the cut and uses extra source footage beyond in_ms/out_ms, so no position or duration changes. Both clips need that footage: half the duration before in_ms, and half after the previous clip's out_ms.
- preview_frame returns an image, which costs many more tokens than text. Check timeline_get first, and preview only to verify what text can't show (layout, text rendering, colors, transitions).
- render is asynchronous: it returns a job ID at once. Then call render_job with wait_ms to wait for it instead of polling rapidly. A render uses a snapshot of the project, so you can keep editing while it runs.
- Reference: the resources gomontage://docs/project-format (format specification), gomontage://schema/project.v1.json (JSON Schema) and gomontage://docs/mcp (these tools).`

// Tool descriptions. Each says when to use the tool, not only what it does.
const (
	descProjectCreate = `Create a new, empty project file. Use this once, at the start, when there is no *.gomontage.json yet; to work on an existing project, pass its path to the other tools instead. Give a preset (reel = 1080x1920 vertical, youtube_720p, youtube_1080p, youtube_4k), or width, height and fps. The preset also sets the render preset and the default output file. Returns the timeline summary.`

	descMediaImport = `Add media files (video, audio, images) to the project's media bin, several in one call. Use this before placing a file on the timeline: clips refer to media IDs, never to file paths. Each file is probed once and gets an ID such as m_beach. A file already in the bin returns its existing ID. A file that fails is reported and the others are still imported.`

	descTimelineGet = `Show the project as compact text: format, duration, media bin, every track with its clips (ID, type, media, start–end on the timeline, source window, non-default properties), gaps, and the check results (project errors, render blockers, warnings). Use it before editing, to get IDs and times, and to verify edits; it is much cheaper than preview_frame. Set detail:true to also get every clip's full JSON.`

	descTimelineEdit = `Use for every timeline change; group all related operations into a single call. Ops: add_track, add_clip (any clip type on any track, including text, logos and color overlays on upper video tracks), trim (move a clip's edges on the timeline), remove (a clip or a track, optionally ripple), update (any clip field: volume, fades, layout, text, style, transition_in, effects, at_ms; or a track's name/enabled). Ops apply in order and atomically: if any op fails or the result is invalid, nothing is saved and the error names the op (ops[2]: clip "c9" not found). On success it returns the changes, new IDs, warnings and render blockers.`

	descPreviewFrame = `Render a single frame at time_ms as a JPEG image (default 480 px wide). Use it sparingly, to check what timeline_get can't show: framing and layout, text rendering, colors, a transition. Images cost far more tokens than text. Repeated calls for an unchanged project are served from a cache.`

	descRender = `Start rendering the project to a video or audio file; returns a job ID immediately. Use it when the edit is done (check timeline_get shows no render blockers first). Then call render_job with wait_ms to follow it. The output defaults to the project's output settings, or <project dir>/output/. Pass preset or output to override them. dry_run:true returns the FFmpeg command instead of rendering, which is useful for debugging. Rendering uses a snapshot, so editing during a render is safe.`

	descRenderJob = `Use after render, to follow or stop the render job. action "status" reports progress (percent, eta_ms), and when finished the output path or a short error summary with a hint. Set wait_ms (up to 60000) to wait for the job to finish or for that long before answering, instead of polling in a tight loop. Leave job empty to list all jobs. action "cancel" stops the job and removes its partial output.`
)

// Options configure a Server.
type Options struct {
	// Logger receives diagnostics. Nil discards them. Never point it at
	// stdout when serving over stdio.
	Logger *slog.Logger
	// Version is reported to clients.
	Version string
}

// Server is the GoMontage MCP server.
type Server struct {
	mcp      *mcp.Server
	store    *store
	jobs     *jobs
	previews *previewCache
	log      *slog.Logger
}

// New returns a server with all tools and resources registered.
func New(opts Options) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	s := &Server{store: newStore(), jobs: newJobs(), previews: newPreviewCache(), log: log}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "gomontage", Title: "GoMontage", Version: version}, &mcp.ServerOptions{
		Instructions: Instructions,
		Logger:       log,
		Capabilities: &mcp.ServerCapabilities{},
	})
	s.addTools()
	s.addResources()
	return s
}

// MCP returns the underlying SDK server (e.g. to connect it to a
// transport in tests).
func (s *Server) MCP() *mcp.Server { return s.mcp }

// Run serves on t until the client disconnects or ctx is done; running
// renders are cancelled on return.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	defer s.jobs.cancelAll()
	return s.mcp.Run(ctx, t)
}

// textResult is a tool result holding one text block.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// problemsInline formats an error, listing a ValidationError's problems.
func problemsInline(err error) string {
	var ve *project.ValidationError
	if !errors.As(err, &ve) {
		return err.Error()
	}
	lines := make([]string, len(ve.Problems))
	for i, p := range ve.Problems {
		lines[i] = problemText(p)
	}
	return strings.Join(lines, "\n  ")
}

// inputSchema infers T's schema and overrides property descriptions (the
// project schema is the source for fields it defines).
func inputSchema[T any](descs map[string]string) *jsonschema.Schema {
	sc, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	for k, d := range descs {
		if p, ok := sc.Properties[k]; ok {
			p.Description = d
		} else {
			panic(fmt.Sprintf("inputSchema: no property %q", k))
		}
	}
	return sc
}

// logged wraps a typed handler with timing logs and panic recovery.
func logged[In any](s *Server, name string, h func(context.Context, In) (*mcp.CallToolResult, error)) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (res *mcp.CallToolResult, _ any, err error) {
		start := time.Now()
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("tool panicked", "tool", name, "panic", r)
				res, err = nil, fmt.Errorf("internal error in %s: %v", name, r)
			}
			s.log.Info("tool call", "tool", name, "ms", time.Since(start).Milliseconds(), "error", err)
		}()
		res, err = h(ctx, in)
		return res, nil, err
	}
}

func boolPtr(b bool) *bool { return &b }

func (s *Server) addTools() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "project_create", Title: "Create project", Description: descProjectCreate,
		InputSchema: inputSchema[createInput](map[string]string{
			"width":  schemaDesc("format", "width"),
			"height": schemaDesc("format", "height"),
			"fps":    schemaDesc("format", "fps"),
			"name":   schemaDesc("", "name"),
		}),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, logged(s, "project_create", s.projectCreate))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "media_import", Title: "Import media", Description: descMediaImport,
		InputSchema: inputSchema[importInput](map[string]string{"project": projectFieldDesc}),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true},
	}, logged(s, "media_import", s.mediaImport))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "timeline_get", Title: "Show timeline", Description: descTimelineGet,
		InputSchema: inputSchema[getInput](map[string]string{"project": projectFieldDesc}),
		Annotations: readOnly,
	}, logged(s, "timeline_get", s.timelineGet))

	s.mcp.AddTool(&mcp.Tool{
		Name: "timeline_edit", Title: "Edit timeline", Description: descTimelineEdit,
		InputSchema: editInputSchema(),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, s.timelineEditHandler)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "preview_frame", Title: "Preview frame", Description: descPreviewFrame,
		InputSchema: inputSchema[previewInput](map[string]string{"project": projectFieldDesc}),
		Annotations: readOnly,
	}, logged(s, "preview_frame", s.previewFrame))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "render", Title: "Render", Description: descRender,
		InputSchema: inputSchema[renderInput](map[string]string{"project": projectFieldDesc}),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, logged(s, "render", s.render))

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "render_job", Title: "Render job", Description: descRenderJob,
		InputSchema: inputSchema[jobInput](nil),
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, logged(s, "render_job", s.renderJob))
}

// Resource URIs.
const (
	ResourceSpec   = "gomontage://docs/project-format"
	ResourceSchema = "gomontage://schema/project.v1.json"
	ResourceMCP    = "gomontage://docs/mcp"
)

func (s *Server) addResources() {
	add := func(uri, name, title, desc, mime, text string) {
		s.mcp.AddResource(&mcp.Resource{URI: uri, Name: name, Title: title, Description: desc, MIMEType: mime},
			func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: text}}}, nil
			})
	}
	add(ResourceSpec, "project-format", "GoMontage project format v1",
		"The full specification of *.gomontage.json files: every field, the loader rules, rendering (transitions §9.4). Read it for details the tool descriptions don't cover.",
		"text/markdown", docs.ProjectFormat)
	add(ResourceSchema, "project-schema", "Project JSON Schema v1",
		"The JSON Schema (draft 2020-12) of project files, the source of truth for field shapes and descriptions.",
		"application/schema+json", string(schema.ProjectV1))
	add(ResourceMCP, "mcp-tools", "GoMontage MCP tools",
		"Reference for this server's tools and timeline_edit ops, with examples.",
		"text/markdown", docs.MCP)
}
