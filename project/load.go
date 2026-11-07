package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/ahmedhodiani/gomontage/schema"
)

// Problem is one validation error or warning, located by a JSON pointer into
// the project file (e.g. "/tracks/1/clips/0"; "" is the whole document). The
// message names the item it is about, e.g. `clip "c2": ...`.
type Problem struct {
	Path    string
	Message string
}

// Warning is a problem that doesn't block loading (e.g. a missing media file).
type Warning = Problem

// String formats the problem as "<path>: <message>" ("/" for the root).
func (p Problem) String() string {
	path := p.Path
	if path == "" {
		path = "/"
	}
	return path + ": " + p.Message
}

// ValidationError lists every problem that made a project invalid (Load) or
// unrenderable (Build). Its Error() has one "<path>: <message>" line per
// problem.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	return strings.Join(lines, "\n")
}

// Load reads, validates and canonicalizes a project file.
//
// Validation happens in the order the specification (section 12) requires:
// the file is validated against the embedded JSON Schema (schema.ProjectV1),
// the only place shape rules live; then the loader rules L1–L7, L11 and L14
// run. Every problem found is returned in a *ValidationError. Warnings
// (missing media files, stale probes, audio settings on clips without sound)
// never block loading.
//
// Render-time checks (missing media, fonts, empty projects, features the
// library can't render yet) are done by Build.
func Load(path string) (*Project, []Warning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("project: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, fmt.Errorf("project: %w", err)
	}
	return Parse(data, filepath.Dir(abs))
}

// Parse is like Load for a project that is already in memory. dir is the
// directory relative paths are resolved against (where the file lives).
func Parse(data []byte, dir string) (*Project, []Warning, error) {
	if problems := validateSchema(data); len(problems) > 0 {
		return nil, nil, &ValidationError{Problems: problems}
	}
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		// The schema guarantees the shape; this only happens for numbers
		// like 1.0 that JSON Schema counts as integers.
		return nil, nil, &ValidationError{Problems: []Problem{{Message: "cannot decode project: " + err.Error()}}}
	}
	p.dir = dir
	canonicalize(&p)

	r := &ruleRun{p: &p}
	r.checkIDs()
	r.checkClips()
	r.checkOutput()
	if len(r.problems) > 0 {
		return nil, r.warnings, &ValidationError{Problems: r.problems}
	}
	r.checkMediaFiles()
	return &p, r.warnings, nil
}

var (
	schemaOnce     sync.Once
	compiledSchema *jsonschema.Schema
	schemaErr      error
)

func projectSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema.ProjectV1))
		if err != nil {
			schemaErr = fmt.Errorf("project: embedded schema: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(schema.ProjectV1URL, doc); err != nil {
			schemaErr = fmt.Errorf("project: embedded schema: %w", err)
			return
		}
		compiledSchema, schemaErr = c.Compile(schema.ProjectV1URL)
	})
	return compiledSchema, schemaErr
}

var printer = message.NewPrinter(language.English)

// validateSchema validates data against the embedded schema and returns one
// problem per failing leaf, located by JSON pointer.
func validateSchema(data []byte) []Problem {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return []Problem{{Message: "invalid JSON: " + err.Error()}}
	}
	sch, err := projectSchema()
	if err != nil {
		return []Problem{{Message: err.Error()}}
	}
	err = sch.Validate(inst)
	if err == nil {
		return nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []Problem{{Message: err.Error()}}
	}
	var out []Problem
	seen := map[string]bool{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			p := Problem{Path: jsonPointer(e.InstanceLocation), Message: e.ErrorKind.LocalizedString(printer)}
			if key := p.String(); !seen[key] {
				seen[key] = true
				out = append(out, p)
			}
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	return out
}

func jsonPointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		t = strings.ReplaceAll(t, "~", "~0")
		b.WriteString(strings.ReplaceAll(t, "/", "~1"))
	}
	return b.String()
}
