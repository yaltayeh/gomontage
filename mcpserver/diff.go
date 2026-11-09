package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ahmedhodiani/gomontage/project"
)

type placedClip struct {
	track string
	clip  *project.Clip
}

func clipsByID(p *project.Project) map[string]placedClip {
	m := map[string]placedClip{}
	for ti := range p.Tracks {
		for ci := range p.Tracks[ti].Clips {
			c := &p.Tracks[ti].Clips[ci]
			m[c.ID] = placedClip{p.Tracks[ti].ID, c}
		}
	}
	return m
}

// fieldsOf returns v's JSON object as raw values per key.
func fieldsOf(v any) map[string]json.RawMessage {
	data, _ := json.Marshal(v)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(data, &m)
	return m
}

func rawText(r json.RawMessage) string {
	if r == nil {
		return "—"
	}
	var v any
	if json.Unmarshal(r, &v) == nil {
		if s, ok := v.(string); ok {
			return quote(s, 40)
		}
	}
	return compactJSON(r)
}

// fieldChanges lists "key old→new" for the top-level fields that differ.
func fieldChanges(a, b any, skip ...string) []string {
	fa, fb := fieldsOf(a), fieldsOf(b)
	keys := map[string]bool{}
	for k := range fa {
		keys[k] = true
	}
	for k := range fb {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		if !contains(skip, k) {
			sorted = append(sorted, k)
		}
	}
	sort.Strings(sorted)
	var out []string
	for _, k := range sorted {
		if string(fa[k]) != string(fb[k]) {
			out = append(out, fmt.Sprintf("%s %s→%s", k, rawText(fa[k]), rawText(fb[k])))
		}
	}
	return out
}

// diffText describes what changed from before to after, one line per
// track or clip, in the order tracks and clips appear in after.
func diffText(before, after *project.Project) []string {
	var lines []string
	bt := map[string]int{}
	for i, t := range before.Tracks {
		bt[t.ID] = i
	}
	at := map[string]bool{}
	for i := range after.Tracks {
		t := &after.Tracks[i]
		at[t.ID] = true
		j, ok := bt[t.ID]
		if !ok {
			s := fmt.Sprintf("+ track %s (%s", t.ID, t.Kind)
			if t.Name != "" {
				s += " " + strconv.Quote(t.Name)
			}
			lines = append(lines, s+fmt.Sprintf(", position %d of %d)", i, len(after.Tracks)))
			continue
		}
		if ch := fieldChanges(&before.Tracks[j], t, "clips"); len(ch) > 0 {
			lines = append(lines, fmt.Sprintf("~ track %s: %s", t.ID, strings.Join(ch, " · ")))
		}
	}
	for _, t := range before.Tracks {
		if !at[t.ID] {
			lines = append(lines, fmt.Sprintf("- track %s (with its %d clips)", t.ID, len(t.Clips)))
		}
	}

	bc, ac := clipsByID(before), clipsByID(after)
	for ti := range after.Tracks {
		t := &after.Tracks[ti]
		for _, ci := range sortedClipIndexes(t) {
			c := &t.Clips[ci]
			old, ok := bc[c.ID]
			if !ok {
				lines = append(lines, fmt.Sprintf("+ clip %s on %s: %s", c.ID, t.ID, clipSummary(after, c)))
				continue
			}
			var ch []string
			if old.track != t.ID {
				ch = append(ch, fmt.Sprintf("track %s→%s", old.track, t.ID))
			}
			oa, ob := old.clip.AtMS, clipEnd(before, old.clip)
			na, nb := c.AtMS, clipEnd(after, c)
			if oa != na || ob != nb {
				ch = append(ch, fmt.Sprintf("range %d–%d→%d–%d", oa, ob, na, nb))
			}
			ch = append(ch, fieldChanges(old.clip, c, "at_ms")...)
			if len(ch) > 0 {
				lines = append(lines, fmt.Sprintf("~ clip %s: %s", c.ID, strings.Join(ch, " · ")))
			}
		}
	}
	var removed []string
	for id, old := range bc {
		if _, ok := ac[id]; !ok && at[old.track] {
			removed = append(removed, fmt.Sprintf("- clip %s (was on %s, %d–%d)", id, old.track, old.clip.AtMS, clipEnd(before, old.clip)))
		}
	}
	sort.Strings(removed)
	return append(lines, removed...)
}
