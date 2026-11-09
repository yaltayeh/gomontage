package docs

import _ "embed"

// ProjectFormat is the project file format specification
// (docs/project-format.md), embedded for tools that serve it, such as the
// MCP server.
//
//go:embed project-format.md
var ProjectFormat string

// MCP is the MCP server's tool reference (docs/mcp.md).
//
//go:embed mcp.md
var MCP string
