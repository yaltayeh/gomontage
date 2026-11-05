package project

import (
	"errors"
	"fmt"
	"os"
)

// CheckResult separates the two kinds of problems a project can have, as
// `gomontage validate` reports them:
//
//   - ProjectErrors: the project itself is invalid — schema violations and
//     loader rules L1–L7, L14 (specification section 12). It can't be loaded,
//     saved or built until they're fixed.
//   - RenderBlockers: the project is valid but can't be rendered right now —
//     missing media used by enabled clips (L8), missing font files (L9), no
//     enabled clips (L12), features the library can't render yet (L13,
//     section 14, including output.fit "cover"). The project can still be
//     loaded, edited and saved.
//
// Render blockers are only checked when there are no project errors
// (RenderChecked reports whether they were). Warnings never block anything;
// a missing media file that is a render blocker is not repeated as a warning.
type CheckResult struct {
	ProjectErrors  []Problem
	RenderBlockers []Problem
	Warnings       []Warning
	RenderChecked  bool
}

// OK reports whether the project is valid and can be rendered.
func (r *CheckResult) OK() bool {
	return len(r.ProjectErrors) == 0 && len(r.RenderBlockers) == 0 && r.RenderChecked
}

// Err returns the problems that block rendering as a *ValidationError —
// project errors if there are any, else render blockers — or nil.
func (r *CheckResult) Err() error {
	switch {
	case len(r.ProjectErrors) > 0:
		return &ValidationError{Problems: r.ProjectErrors}
	case len(r.RenderBlockers) > 0:
		return &ValidationError{Problems: r.RenderBlockers}
	}
	return nil
}

// Check checks an in-memory project (e.g. one built or edited in Go): the
// schema and loader rules exactly as Load applies them, then the render-time
// checks.
func Check(p *Project) *CheckResult {
	r, _ := check(p)
	return r
}

// check returns the result and, if there are no project errors, the
// canonical validated copy of p that Build uses.
func check(p *Project) (*CheckResult, *Project) {
	data, err := Marshal(p)
	if err != nil {
		return &CheckResult{ProjectErrors: []Problem{{Message: err.Error()}}}, nil
	}
	q, warnings, err := Parse(data, p.dir)
	return finishCheck(q, warnings, err)
}

// CheckFile loads and checks a project file. The returned error is only for
// files that can't be read; every problem with the content is in the
// result. The project is returned when it has no project errors.
func CheckFile(path string) (*CheckResult, *Project, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, nil, fmt.Errorf("project: %w", err)
	}
	p, warnings, err := Load(path)
	var ve *ValidationError
	if err != nil && !errors.As(err, &ve) {
		return nil, nil, err
	}
	r, _ := finishCheck(p, warnings, err)
	if len(r.ProjectErrors) > 0 {
		p = nil
	}
	return r, p, nil
}

func finishCheck(q *Project, warnings []Warning, loadErr error) (*CheckResult, *Project) {
	r := &CheckResult{Warnings: warnings}
	if loadErr != nil {
		var ve *ValidationError
		if errors.As(loadErr, &ve) {
			r.ProjectErrors = ve.Problems
		} else {
			r.ProjectErrors = []Problem{{Message: loadErr.Error()}}
		}
		return r, nil
	}
	r.RenderChecked = true
	r.RenderBlockers = renderProblems(q)
	if q.Output != nil && q.Output.FitValue() == FitCover {
		r.RenderBlockers = append(r.RenderBlockers, Problem{Path: "/output/fit", Message: `output: fit "cover" is not supported yet`})
	}
	// A missing media file is a load warning and a render blocker: keep it
	// once, as the blocker.
	blocker := map[string]bool{}
	for _, b := range r.RenderBlockers {
		blocker[b.String()] = true
	}
	kept := r.Warnings[:0:0]
	for _, w := range r.Warnings {
		if !blocker[w.String()] {
			kept = append(kept, w)
		}
	}
	r.Warnings = kept
	return r, q
}
