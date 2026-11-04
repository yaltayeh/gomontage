package engine

import "strings"

// EscapeFilterValue escapes an arbitrary string so it can be used verbatim
// as a filter option value inside an FFmpeg -filter_complex graph, e.g. the
// text= or fontfile= option of drawtext.
//
// FFmpeg parses such values at two levels, so two escaping passes are
// applied (see "Notes on filtergraph escaping" in the FFmpeg filters docs):
//
//  1. Option level (key=value list): backslash, single quote and colon are
//     backslash-escaped, as are leading and trailing whitespace characters
//     (which would otherwise be trimmed).
//  2. Filtergraph level: backslash, single quote, '[', ']', ',' and ';' in
//     the result of step 1 are backslash-escaped.
//
// The returned value is meant to be passed to FFmpeg as a single argv entry
// (as engine.Run does via os/exec) — it is not shell-escaped.
//
// Note: drawtext additionally interprets '%' sequences unless its
// expansion option is set to "none"; the timeline compiler does that.
func EscapeFilterValue(s string) string {
	// Level 1: option value.
	var l1 strings.Builder
	runes := []rune(s)
	lead := 0
	for lead < len(runes) && isFilterSpace(runes[lead]) {
		lead++
	}
	trail := len(runes)
	for trail > lead && isFilterSpace(runes[trail-1]) {
		trail--
	}
	for i, r := range runes {
		switch {
		case r == '\\' || r == '\'' || r == ':':
			l1.WriteByte('\\')
		case (i < lead || i >= trail) && isFilterSpace(r):
			l1.WriteByte('\\')
		}
		l1.WriteRune(r)
	}

	// Level 2: filtergraph description.
	var l2 strings.Builder
	for _, r := range l1.String() {
		switch r {
		case '\\', '\'', '[', ']', ',', ';':
			l2.WriteByte('\\')
		}
		l2.WriteRune(r)
	}
	return l2.String()
}

func isFilterSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
