package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ahmedhodiani/gomontage/schema"
)

// The project JSON Schema is the single source of truth for what clip,
// track and format fields mean. Tool input schemas take their field
// descriptions (and, for clip fields, the field schemas) from it.

var (
	projectSchemaOnce sync.Once
	projectSchemaDoc  map[string]any
)

func projectSchema() map[string]any {
	projectSchemaOnce.Do(func() {
		if err := json.Unmarshal(schema.ProjectV1, &projectSchemaDoc); err != nil {
			panic(fmt.Sprintf("mcpserver: embedded project schema: %v", err))
		}
	})
	return projectSchemaDoc
}

func defs() map[string]any { return projectSchema()["$defs"].(map[string]any) }

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

// description returns the description of a schema node, following $ref.
func description(node map[string]any) string {
	if d, ok := node["description"].(string); ok {
		return d
	}
	if ref, ok := node["$ref"].(string); ok {
		return description(obj(defs()[strings.TrimPrefix(ref, "#/$defs/")]))
	}
	return ""
}

// schemaDesc returns the description at a path of property names into
// the project schema: a $defs entry ("" = the root), then properties, e.g.
// schemaDesc("format", "width").
func schemaDesc(def string, props ...string) string {
	node := projectSchema()
	if def != "" {
		node = obj(defs()[def])
	}
	for _, p := range props {
		node = obj(obj(node["properties"])[p])
	}
	return description(node)
}

// clipVariants are the per-type clip definitions, in the order their
// descriptions are combined.
var clipVariants = []struct{ def, typ string }{
	{"videoClip", "video"}, {"audioClip", "audio"}, {"imageClip", "image"}, {"textClip", "text"}, {"colorClip", "color"},
}

// clipFieldSchemas returns one schema per clip field across all clip
// types. A field whose description differs between types gets them all,
// labelled by type ("video: ...; audio: ...").
func clipFieldSchemas() map[string]map[string]any {
	type seen struct {
		schema map[string]any
		descs  []string
		types  []string
	}
	fields := map[string]*seen{}
	var order []string
	for _, v := range clipVariants {
		props := obj(obj(defs()[v.def])["properties"])
		for name, raw := range props {
			if name == "type" || name == "id" {
				continue
			}
			p := obj(raw)
			s, ok := fields[name]
			if !ok {
				cp := map[string]any{}
				for k, val := range p {
					if k != "description" {
						cp[k] = val
					}
				}
				s = &seen{schema: cp}
				fields[name] = s
				order = append(order, name)
			}
			s.types = append(s.types, v.typ)
			d := description(p)
			if !contains(s.descs, d) {
				s.descs = append(s.descs, d)
			}
		}
	}
	out := map[string]map[string]any{}
	for _, name := range order {
		s := fields[name]
		d := s.descs[0]
		if len(s.descs) > 1 {
			var parts []string
			for _, v := range clipVariants {
				p := obj(obj(obj(defs()[v.def])["properties"])[name])
				if p != nil {
					parts = append(parts, v.typ+": "+description(p))
				}
			}
			d = strings.Join(parts, " ")
		}
		if len(s.types) < len(clipVariants) {
			d = "(" + strings.Join(s.types, ", ") + " clips) " + d
		}
		out[name] = inlineRef(s.schema, d)
	}
	out["type"] = map[string]any{
		"description": description(obj(obj(obj(defs()["clip"])["properties"])["type"])),
		"enum":        []any{"video", "audio", "image", "text", "color"},
	}
	return out
}

// inlineRef returns field with its $ref (if any) replaced by the
// referenced definition, described by desc instead of the definition's own
// (near-identical) description, so each field is described once.
func inlineRef(field map[string]any, desc string) map[string]any {
	out := map[string]any{}
	if ref, ok := field["$ref"].(string); ok {
		for k, v := range obj(defs()[strings.TrimPrefix(ref, "#/$defs/")]) {
			out[k] = v
		}
	}
	for k, v := range field {
		if k != "$ref" {
			out[k] = v
		}
	}
	if ref, ok := out["$ref"].(string); ok && len(out) == 1 { // a ref to a ref
		return inlineRef(map[string]any{"$ref": ref}, desc)
	}
	out["description"] = desc
	return out
}

// referencedDefs returns the $defs reachable from the given nodes.
func referencedDefs(nodes ...any) map[string]any {
	out := map[string]any{}
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok {
				name := strings.TrimPrefix(ref, "#/$defs/")
				if _, done := out[name]; !done {
					out[name] = defs()[name]
					walk(defs()[name])
				}
			}
			for _, v := range x {
				walk(v)
			}
		case []any:
			for _, v := range x {
				walk(v)
			}
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// editInputSchema is timeline_edit's input schema. The server validates
// ops itself (to report errors by op index), so the schema is there to
// tell the model what each op and field means.
func editInputSchema() map[string]any {
	// Each clip field is defined once in $defs (clip_<field>) and used by
	// both add_clip's clip and update's set, so descriptions aren't
	// repeated.
	clipFields := clipFieldSchemas()
	fieldDefs := map[string]any{}
	clipProps := map[string]any{
		"id": map[string]any{
			"type":        "string",
			"description": "Optional ID for the new clip (same as new_id). " + schemaDesc("id"),
		},
	}
	setProps := map[string]any{}
	for _, name := range sortedKeys(clipFields) {
		ref := map[string]any{"$ref": "#/$defs/clip_" + name}
		fieldDefs["clip_"+name] = clipFields[name]
		clipProps[name] = ref
		if name != "type" {
			setProps[name] = map[string]any{"anyOf": []any{ref, map[string]any{"type": "null"}}}
		}
	}
	setProps["name"] = map[string]any{"description": "(tracks) " + schemaDesc("track", "name"), "type": []any{"string", "null"}}
	setProps["enabled"] = map[string]any{"description": "(tracks) " + schemaDesc("track", "enabled"), "type": []any{"boolean", "null"}}

	op := map[string]any{
		"type":                 "object",
		"required":             []any{"op"},
		"additionalProperties": false,
		"properties": map[string]any{
			"op": map[string]any{
				"enum": toAny(opNames()),
				"description": `The operation. add_track: new track (kind, optional name, index, new_id). ` +
					`add_clip: new clip of any type on any track (track, clip); this is also how text, logos, images and color overlays are added: put them on a video track above the main one. ` +
					`trim: move a clip's edges on the timeline (id, start_ms and/or end_ms; video/audio clips adjust in_ms/out_ms, other clips duration_ms). ` +
					`remove: delete a clip or a whole track (id, optional ripple). ` +
					`update: change any fields of a clip, or a track's name/enabled (id, set).`,
			},
			"id": map[string]any{
				"type":        "string",
				"description": "trim, remove, update: the ID of the clip (remove/update: or track) to change, from timeline_get or created earlier in this batch.",
			},
			"new_id": map[string]any{
				"type":        "string",
				"description": "add_track, add_clip: optional ID for the new item, so later ops in the same batch can refer to it. If omitted a readable ID is generated (e.g. t_titles, c_beach, c_beach_2); the result lists the new IDs. " + schemaDesc("id"),
			},
			"kind":  map[string]any{"enum": []any{"video", "audio"}, "description": "add_track: " + schemaDesc("track", "kind")},
			"name":  map[string]any{"type": "string", "description": "add_track: " + schemaDesc("track", "name")},
			"index": map[string]any{"type": "integer", "minimum": 0, "description": "add_track: position in the project's track list (0 = first; default: last). Video tracks earlier in the list are drawn below later ones, so a title or logo track goes after the main video track."},
			"track": map[string]any{"type": "string", "description": "add_clip: ID of the track to put the clip on."},
			"clip": map[string]any{
				"type":        "object",
				"description": "add_clip: the clip, exactly as stored in the project file (" + description(obj(defs()["clip"])) + ") id is optional.",
				"required":    []any{"type", "at_ms"},
				"properties":  clipProps,
			},
			"set": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"description":          "update: the fields to change, as a JSON merge patch: given fields replace the current value, objects (style, position, size, transition_in) merge field by field, arrays (effects) are replaced whole, and null resets a field to its default. id and type can't change. at_ms moves the clip. For a track only name and enabled apply.",
				"properties":           setProps,
			},
			"start_ms": map[string]any{"type": "integer", "minimum": 0, "description": "trim: the clip's new start on the timeline, in ms. Without ripple the clip's end stays where it is."},
			"end_ms":   map[string]any{"type": "integer", "minimum": 1, "description": "trim: the clip's new end on the timeline, in ms."},
			"ripple":   map[string]any{"type": "boolean", "description": "trim, remove: also shift every later clip on the same track (clips starting at or after this clip's old end) by the change in length, closing or opening the gap. Other tracks never move. trim with ripple keeps the clip's start fixed: trimming its head shortens it from the front and pulls the rest of the track earlier. Default false."},
		},
	}
	root := map[string]any{
		"type":                 "object",
		"required":             []any{"project", "ops"},
		"additionalProperties": false,
		"properties": map[string]any{
			"project": map[string]any{"type": "string", "description": projectFieldDesc},
			"ops": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "The operations, applied in order. All or nothing: if any op fails, or the result has project errors, nothing is saved and the error names the op (ops[3]: ...).",
				"items":       map[string]any{"$ref": "#/$defs/op"},
			},
		},
	}
	all := referencedDefs(fieldDefs)
	for k, v := range fieldDefs {
		all[k] = v
	}
	all["op"] = op
	root["$defs"] = all
	return root
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

const projectFieldDesc = "Path to the project file (*.gomontage.json). Use an absolute path; a relative one is resolved against the server's working directory."
