package mcpserver

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A batch whose last op fails saves nothing: the file stays byte-identical
// and no snapshot is written; the error names the op.
func TestEdit_AtomicRollback(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	before := readFile(t, path)

	text, isErr := edit(t, cs, path, `[
		{"op": "update", "id": "c1", "set": {"volume": 0.5}},
		{"op": "add_track", "kind": "video", "name": "titles"},
		{"op": "remove", "id": "c9"}
	]`)
	if !isErr {
		t.Fatalf("expected an error, got:\n%s", text)
	}
	assertContains(t, text, "rejected, nothing was saved", `ops[2] (remove): clip or track "c9" not found`)
	if got := readFile(t, path); got != before {
		t.Error("the file changed although the batch was rejected")
	}
	if s := snapshots(t, path); len(s) != 0 {
		t.Errorf("snapshots written for a rejected batch: %v", s)
	}
}

// Problems found by validating the result are attributed to the op that
// last touched the item they are about.
func TestEdit_ResultErrorsNameTheOp(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	before := readFile(t, path)

	// ops[1] makes c2 overlap c1; ops[2] gives the logo an invalid volume.
	text, isErr := edit(t, cs, path, `[
		{"op": "update", "id": "c_song", "set": {"volume": 0.4}},
		{"op": "update", "id": "c2", "set": {"at_ms": 3000}},
		{"op": "update", "id": "c1", "set": {"fade_out_ms": 99999}}
	]`)
	if !isErr {
		t.Fatalf("expected an error, got:\n%s", text)
	}
	assertContains(t, text, "ops[1]: ", `"c2"`, "ops[2]: ", `"c1"`)
	if strings.Contains(text, "ops[0]") {
		t.Errorf("ops[0] (valid) blamed:\n%s", text)
	}
	if readFile(t, path) != before {
		t.Error("the file changed although the batch was rejected")
	}
}

// Op-level checks: unknown ops, fields an op doesn't take, bad types.
func TestEdit_OpValidation(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	cases := []struct{ ops, want string }{
		{`[{"op": "explode"}]`, `ops[0]: unknown op "explode" (one of add_clip, add_track, remove, trim, update)`},
		{`[{"kind": "video"}]`, `ops[0]: missing "op"`},
		{`[{"op": "remove", "id": "c1", "set": {}}]`, `ops[0]: remove doesn't take "set" (it takes id, ripple)`},
		{`[{"op": "trim", "id": "c1", "start_ms": "soon"}]`, "ops[0]: trim: start_ms must be an integer"},
		{`[{"op": "add_track", "kind": "subtitles"}]`, `add_track needs "kind": "video" or "audio"`},
		{`[{"op": "add_clip", "track": "t_main", "clip": {"type": "text", "text": "x", "at_ms": 0, "duration_ms": 1, "colour": "red"}}]`, `clip: unknown field "colour"`},
		{`[{"op": "update", "id": "c1", "set": {"type": "image"}}]`, "a clip's type can't be changed"},
		{`[{"op": "update", "id": "m_a", "set": {"volume": 1}}]`, `"m_a" is a media entry`},
		{`[{"op": "add_track", "kind": "video", "new_id": "c1"}]`, `id "c1" is already used`},
		{`[{"op": "trim", "id": "c1", "start_ms": 3000, "end_ms": 3000}]`, "would be empty"},
		{`[{"op": "trim", "id": "c1", "start_ms": 0, "end_ms": 11000}]`, `clip "c1" can end at most 5000 ms later`},
	}
	for _, c := range cases {
		text, isErr := edit(t, cs, path, c.ops)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("ops %s:\n got (error=%v): %s\nwant: %s", c.ops, isErr, text, c.want)
		}
	}
}

// Later ops can refer to items created earlier in the same batch, by
// client-chosen or generated IDs; the result lists the new IDs.
func TestEdit_IDReferencesWithinBatch(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	text, isErr := edit(t, cs, path, `[
		{"op": "add_track", "kind": "video", "name": "Titles", "new_id": "t_titles"},
		{"op": "add_clip", "track": "t_titles", "new_id": "title", "clip": {"type": "text", "text": "Hello", "at_ms": 500, "duration_ms": 2000}},
		{"op": "update", "id": "title", "set": {"fade_in_ms": 300, "style": {"size": 0.08, "box": {"color": "#00000080"}}}},
		{"op": "add_clip", "track": "t_titles", "clip": {"type": "color", "color": "#FF000080", "at_ms": 3000, "duration_ms": 1000}},
		{"op": "add_track", "kind": "audio", "name": "Voice Over"},
		{"op": "add_clip", "track": "t_voice_over", "clip": {"type": "audio", "media": "m_song", "at_ms": 0, "in_ms": 1000, "out_ms": 2000}}
	]`)
	if isErr {
		t.Fatal(text)
	}
	assertContains(t, text, "new ids: ops[0] t_titles · ops[1] title · ops[3] c_color · ops[4] t_voice_over · ops[5] c_song_2",
		"+ track t_titles (video \"Titles\", position 3 of 5)",
		"+ clip title on t_titles: text \"Hello\" · 500–2500 · fade_in 300 · style 0.08 box")
	p := load(t, path)
	c := clipByID(t, p, "title")
	if c.FadeInMS != 300 || c.Style == nil || *c.Style.Size != 0.08 || c.Style.Box.Color != "#00000080" {
		t.Errorf("title clip %+v", c)
	}
	if ti, _ := findClip(p, "c_song_2"); p.Tracks[ti].ID != "t_voice_over" {
		t.Error("c_song_2 not on t_voice_over")
	}
}

// update is a JSON merge patch: objects merge, null resets to the default.
func TestEdit_UpdateMergePatch(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	if text, isErr := edit(t, cs, path, `[
		{"op": "update", "id": "c_logo", "set": {"position": {"x": 0.9}, "size": null, "anchor": null}},
		{"op": "update", "id": "c1", "set": {"fade_in_ms": null, "volume": 0.8, "effects": [{"type": "speed", "factor": 2}]}},
		{"op": "update", "id": "t_logo", "set": {"enabled": false, "name": "Logo"}}
	]`); isErr {
		t.Fatal(text)
	}
	p := load(t, path)
	logo := clipByID(t, p, "c_logo")
	if logo.Position.X != 0.9 || logo.Position.Y != 0.05 || logo.Size != nil || logo.Anchor != "" {
		t.Errorf("logo after merge: pos %+v size %+v anchor %q", logo.Position, logo.Size, logo.Anchor)
	}
	c1 := clipByID(t, p, "c1")
	if c1.FadeInMS != 0 || *c1.Volume != 0.8 || c1.SpeedFactor() != 2 || p.ClipDuration(c1) != 2000 {
		t.Errorf("c1 after update: %+v", c1)
	}
	if tr := p.Tracks[findTrack(p, "t_logo")]; tr.IsEnabled() || tr.Name != "Logo" || len(tr.Clips) != 1 {
		t.Errorf("track after update: %+v", tr)
	}
}

// remove with ripple pulls the later clips of the same track (only) earlier
// and drops the transition of the clip that followed the removed one.
func TestEdit_RippleRemove(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	text, isErr := edit(t, cs, path, `[{"op": "remove", "id": "c2", "ripple": true}]`)
	if isErr {
		t.Fatal(text)
	}
	assertContains(t, text, "- clip c2 (was on t_main, 4000–7000)", "~ clip c3: range 7000–10000→4000–7000 · transition_in",
		"note ops[0]: removed the transition_in of c3")
	p := load(t, path)
	if c3 := clipByID(t, p, "c3"); c3.AtMS != 4000 || c3.TransitionIn != nil {
		t.Errorf("c3 at %d transition %+v", c3.AtMS, c3.TransitionIn)
	}
	if logo := clipByID(t, p, "c_logo"); logo.AtMS != 1000 {
		t.Errorf("ripple moved another track's clip to %d", logo.AtMS)
	}

	// Without ripple the gap stays.
	path = writeFixture(t)
	if text, isErr := edit(t, cs, path, `[{"op": "remove", "id": "c1"}]`); isErr {
		t.Fatal(text)
	}
	if c2 := clipByID(t, load(t, path), "c2"); c2.AtMS != 4000 {
		t.Errorf("c2 moved to %d without ripple", c2.AtMS)
	}
}

// trim moves clip edges in timeline time; source points follow (scaled by
// speed); ripple keeps the start and shifts later clips by the change.
func TestEdit_Trim(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	if text, isErr := edit(t, cs, path, `[{"op": "trim", "id": "c1", "start_ms": 1000}]`); isErr {
		t.Fatal(text)
	}
	c1 := clipByID(t, load(t, path), "c1")
	if c1.AtMS != 1000 || c1.InMS != 2000 || c1.OutMS != 5000 {
		t.Errorf("head trim: at %d in %d out %d, want 1000 2000 5000", c1.AtMS, c1.InMS, c1.OutMS)
	}

	path = writeFixture(t)
	text, isErr := edit(t, cs, path, `[{"op": "trim", "id": "c2", "end_ms": 6000, "ripple": true}]`)
	if isErr {
		t.Fatal(text)
	}
	assertContains(t, text, "~ clip c2: range 4000–7000→4000–6000 · out_ms 3000→2000", "~ clip c3: range 7000–10000→6000–9000")
	if c3 := clipByID(t, load(t, path), "c3"); c3.AtMS != 6000 || c3.TransitionIn == nil {
		t.Errorf("c3 after ripple trim: at %d transition %+v", c3.AtMS, c3.TransitionIn)
	}

	// Ripple head trim: the clip stays at its start, shortened from the
	// front; later clips follow.
	path = writeFixture(t)
	if text, isErr := edit(t, cs, path, `[{"op": "trim", "id": "c2", "start_ms": 5000, "ripple": true}]`); isErr {
		t.Fatal(text)
	}
	p := load(t, path)
	if c2 := clipByID(t, p, "c2"); c2.AtMS != 4000 || c2.InMS != 1000 || p.ClipDuration(c2) != 2000 {
		t.Errorf("ripple head trim c2: at %d in %d dur %d", c2.AtMS, c2.InMS, p.ClipDuration(c2))
	}
	if c3 := clipByID(t, p, "c3"); c3.AtMS != 6000 {
		t.Errorf("c3 at %d, want 6000", c3.AtMS)
	}

	// Speed scales the source change: at 2x, 500 ms of timeline is 1000 ms
	// of source.
	path = writeFixture(t)
	if text, isErr := edit(t, cs, path, `[
		{"op": "update", "id": "c1", "set": {"effects": [{"type": "speed", "factor": 2}]}},
		{"op": "trim", "id": "c1", "end_ms": 1500}
	]`); isErr {
		t.Fatal(text)
	}
	if c1 := clipByID(t, load(t, path), "c1"); c1.OutMS != 4000 {
		t.Errorf("speed trim out_ms %d, want 4000", c1.OutMS)
	}

	// Image, text and color clips change duration_ms.
	path = writeFixture(t)
	if text, isErr := edit(t, cs, path, `[{"op": "trim", "id": "c_logo", "start_ms": 2000, "end_ms": 4000}]`); isErr {
		t.Fatal(text)
	}
	if l := clipByID(t, load(t, path), "c_logo"); l.AtMS != 2000 || l.DurationMS != 2000 {
		t.Errorf("logo trim at %d dur %d", l.AtMS, l.DurationMS)
	}
}

// Every successful batch snapshots the previous file once.
func TestEdit_HistorySnapshot(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	v0 := readFile(t, path)
	if text, isErr := edit(t, cs, path, `[{"op": "update", "id": "c1", "set": {"volume": 0.5}}, {"op": "update", "id": "c2", "set": {"volume": 0.5}}]`); isErr {
		t.Fatal(text)
	}
	v1 := readFile(t, path)
	if text, isErr := edit(t, cs, path, `[{"op": "remove", "id": "c_logo"}]`); isErr {
		t.Fatal(text)
	}
	s := snapshots(t, path)
	if len(s) != 2 {
		t.Fatalf("%d snapshots, want 2 (one per batch): %v", len(s), s)
	}
	if readFile(t, s[0]) != v0 || readFile(t, s[1]) != v1 {
		t.Error("snapshots don't hold the previous versions, in order")
	}

	// A batch that changes nothing doesn't save or snapshot.
	text, isErr := edit(t, cs, path, `[{"op": "update", "id": "c1", "set": {"volume": 0.5}}]`)
	if isErr || !strings.Contains(text, "no changes") {
		t.Errorf("no-op batch: %s", text)
	}
	if len(snapshots(t, path)) != 2 {
		t.Error("a no-op batch wrote a snapshot")
	}
}

// Edits made to the file by someone else are picked up (cache keyed by
// mtime and size).
func TestEdit_ReloadsChangedFile(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	mustCall(t, cs, "timeline_get", map[string]any{"project": path})
	data := strings.Replace(readFile(t, path), `"id": "c2"`, `"id": "c_two"`, 1)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	text := mustCall(t, cs, "timeline_get", map[string]any{"project": path})
	assertContains(t, text, "c_two · video m_b")
	if text, isErr := edit(t, cs, path, `[{"op": "update", "id": "c_two", "set": {"volume": 0.2}}]`); isErr {
		t.Fatal(text)
	}
}

// An invalid file on disk is reported, not edited.
func TestEdit_InvalidFile(t *testing.T) {
	cs, _ := session(t)
	path := writeFixture(t)
	data := strings.Replace(readFile(t, path), `"at_ms": 4000`, `"at_ms": 3000`, 1) // c2 overlaps c1
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	text, isErr := edit(t, cs, path, `[{"op": "update", "id": "c1", "set": {"volume": 0.5}}]`)
	if !isErr || !strings.Contains(text, "is invalid; fix these first") {
		t.Errorf("edit of an invalid file: %s", text)
	}
	text = mustCall(t, cs, "timeline_get", map[string]any{"project": path})
	assertContains(t, text, "INVALID", "overlap")
}
