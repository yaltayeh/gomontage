// Package schema holds the JSON Schema for GoMontage project files.
//
// The schema (project.v1.schema.json) is the single source of truth for the
// shape of a project file; see docs/project-format.md. It is embedded so the
// project loader can validate files without reading it from disk.
package schema

import _ "embed"

// ProjectV1 is the JSON Schema (draft 2020-12) for version 1 project files.
//
//go:embed project.v1.schema.json
var ProjectV1 []byte

// ProjectV1URL is the schema's $id, used as its resource URL when compiling.
const ProjectV1URL = "https://github.com/yaltayeh/gomontage/schema/project.v1.schema.json"
