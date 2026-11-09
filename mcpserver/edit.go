package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ahmedhodiani/gomontage/project"
)

// Op names. New ops are added here, in opFields and in apply; existing ops
// and fields never change meaning, so clients written against the MVP keep
// working.
const (
	opAddTrack = "add_track"
	opAddClip  = "add_clip"
	opTrim     = "trim"
	opRemove   = "remove"
	opUpdate   = "update"
)

// opFields lists the fields each op accepts besides "op". Anything else is
// rejected with the op's index, so a misspelt field never silently does
// nothing.
var opFields = map[string][]string{
	opAddTrack: {"new_id", "kind", "name", "index"},
	opAddClip:  {"new_id", "track", "clip"},
	opTrim:     {"id", "start_ms", "end_ms", "ripple"},
	opRemove:   {"id", "ripple"},
	opUpdate:   {"id", "set"},
}

// opNames returns the op names, sorted.
func opNames() []string {
	names := make([]string, 0, len(opFields))
	for n := range opFields {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// editOp is one operation of a timeline_edit batch. Which fields apply
// depends on Op (opFields).
type editOp struct {
	Op      string          `json:"op"`
	ID      string          `json:"id"`
	NewID   string          `json:"new_id"`
	Kind    string          `json:"kind"`
	Name    *string         `json:"name"`
	Index   *int            `json:"index"`
	Track   string          `json:"track"`
	Clip    json.RawMessage `json:"clip"`
	Set     json.RawMessage `json:"set"`
	StartMS *int64          `json:"start_ms"`
	EndMS   *int64          `json:"end_ms"`
	Ripple  bool            `json:"ripple"`
}

// decodeOp checks the op's fields and decodes it.
func decodeOp(raw json.RawMessage) (editOp, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return editOp{}, errors.New("an op must be a JSON object")
	}
	var name string
	if err := json.Unmarshal(fields["op"], &name); err != nil || name == "" {
		return editOp{}, fmt.Errorf(`missing "op" (one of %s)`, strings.Join(opNames(), ", "))
	}
	allowed, ok := opFields[name]
	if !ok {
		return editOp{}, fmt.Errorf("unknown op %q (one of %s)", name, strings.Join(opNames(), ", "))
	}
	for k := range fields {
		if k != "op" && !contains(allowed, k) {
			return editOp{}, fmt.Errorf("%s doesn't take %q (it takes %s)", name, k, strings.Join(allowed, ", "))
		}
	}
	var op editOp
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&op); err != nil {
		return editOp{}, fmt.Errorf("%s: %v", name, jsonErr(err))
	}
	return op, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// jsonErr shortens encoding/json errors.
func jsonErr(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		f := te.Field
		if f == "" {
			f = "value"
		}
		return fmt.Sprintf("%s must be %s, not %s", f, jsonKind(te.Type.Kind().String()), te.Value)
	}
	return strings.TrimPrefix(err.Error(), "json: ")
}

func jsonKind(k string) string {
	switch {
	case strings.HasPrefix(k, "int"), strings.HasPrefix(k, "uint"):
		return "an integer"
	case strings.HasPrefix(k, "float"):
		return "a number"
	case k == "bool":
		return "true or false"
	case k == "string":
		return "a string"
	case k == "slice":
		return "an array"
	}
	return "an object"
}

// editor applies ops to a working copy of a project.
type editor struct {
	p       *project.Project
	touched map[string]int // item ID -> index of the last op that changed it
	notes   []string       // side effects worth reporting (e.g. dropped transitions)
	newIDs  []string       // "ops[i] → id"
}

func newEditor(p *project.Project) *editor {
	return &editor{p: p, touched: map[string]int{}}
}

func (ed *editor) touch(i int, id string) { ed.touched[id] = i }

// apply runs one op.
func (ed *editor) apply(i int, op editOp) error {
	switch op.Op {
	case opAddTrack:
		return ed.addTrack(i, op)
	case opAddClip:
		return ed.addClip(i, op)
	case opTrim:
		return ed.trim(i, op)
	case opRemove:
		return ed.remove(i, op)
	case opUpdate:
		return ed.update(i, op)
	}
	return fmt.Errorf("unknown op %q", op.Op)
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// usedIDs returns every ID in the project (media, tracks and clips share
// one namespace).
func usedIDs(p *project.Project) map[string]bool {
	used := map[string]bool{}
	for _, m := range p.Media {
		used[m.ID] = true
	}
	for _, t := range p.Tracks {
		used[t.ID] = true
		for _, c := range t.Clips {
			used[c.ID] = true
		}
	}
	return used
}

// newID returns the client's id if given (checked), else a readable unique
// ID made from base.
func (ed *editor) newID(want, base string) (string, error) {
	used := usedIDs(ed.p)
	if want != "" {
		if !idPattern.MatchString(want) {
			return "", fmt.Errorf("id %q is not valid: 1-64 characters, starting with a letter or digit, then letters, digits, '_', '-' or '.'", want)
		}
		if used[want] {
			return "", fmt.Errorf("id %q is already used in the project", want)
		}
		return want, nil
	}
	if !used[base] {
		return base, nil
	}
	for n := 2; ; n++ {
		if id := fmt.Sprintf("%s_%d", base, n); !used[id] {
			return id, nil
		}
	}
}

// slug reduces s to ID characters (lower case), at most 40 of them.
func slug(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(s) {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
			under = false
		} else if !under && b.Len() > 0 {
			b.WriteByte('_')
			under = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "_")
	}
	return out
}

// findClip returns the track and clip index of a clip ID, or -1s.
func findClip(p *project.Project, id string) (int, int) {
	for ti := range p.Tracks {
		for ci := range p.Tracks[ti].Clips {
			if p.Tracks[ti].Clips[ci].ID == id {
				return ti, ci
			}
		}
	}
	return -1, -1
}

func findTrack(p *project.Project, id string) int {
	for ti := range p.Tracks {
		if p.Tracks[ti].ID == id {
			return ti
		}
	}
	return -1
}

// notFound explains a missing ID, saying what kind of item it is if it
// exists as something else.
func notFound(p *project.Project, id, want string) error {
	if id == "" {
		return fmt.Errorf(`missing "id" (the %s to change; IDs are listed by timeline_get)`, want)
	}
	if p.MediaByID(id) != nil {
		return fmt.Errorf("%q is a media entry, not a %s", id, want)
	}
	if want == "clip" && findTrack(p, id) >= 0 {
		return fmt.Errorf("%q is a track, not a clip", id)
	}
	return fmt.Errorf("%s %q not found (IDs are listed by timeline_get)", want, id)
}

func sortClips(t *project.Track) {
	sort.SliceStable(t.Clips, func(a, b int) bool { return t.Clips[a].AtMS < t.Clips[b].AtMS })
}

func (ed *editor) addTrack(i int, op editOp) error {
	if op.Kind != project.KindVideo && op.Kind != project.KindAudio {
		return errors.New(`add_track needs "kind": "video" or "audio"`)
	}
	base := "t_" + op.Kind
	t := project.Track{Kind: op.Kind, Clips: []project.Clip{}}
	if op.Name != nil && *op.Name != "" {
		t.Name = *op.Name
		if s := slug(*op.Name); s != "" {
			base = "t_" + s
		}
	}
	id, err := ed.newID(op.NewID, base)
	if err != nil {
		return err
	}
	t.ID = id
	at := len(ed.p.Tracks)
	if op.Index != nil {
		if *op.Index < 0 || *op.Index > len(ed.p.Tracks) {
			return fmt.Errorf("index %d is out of range: the project has %d tracks (0 = first, %d = last)", *op.Index, len(ed.p.Tracks), len(ed.p.Tracks))
		}
		at = *op.Index
	}
	ed.p.Tracks = append(ed.p.Tracks, project.Track{})
	copy(ed.p.Tracks[at+1:], ed.p.Tracks[at:])
	ed.p.Tracks[at] = t
	ed.touch(i, id)
	ed.newIDs = append(ed.newIDs, fmt.Sprintf("ops[%d] %s", i, id))
	return nil
}

// decodeStrict decodes JSON into v, rejecting unknown fields.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New(jsonErr(err))
	}
	return nil
}

func (ed *editor) addClip(i int, op editOp) error {
	ti := findTrack(ed.p, op.Track)
	if ti < 0 {
		if op.Track == "" {
			return errors.New(`add_clip needs "track" (the ID of the track to put the clip on)`)
		}
		return notFound(ed.p, op.Track, "track")
	}
	if len(op.Clip) == 0 || op.Clip[0] != '{' {
		return errors.New(`add_clip needs "clip": an object with at least "type" and "at_ms"`)
	}
	var c project.Clip
	if err := decodeStrict(op.Clip, &c); err != nil {
		return fmt.Errorf("clip: %v", err)
	}
	if c.Type == "" {
		return errors.New(`clip needs "type": video, audio, image, text or color`)
	}
	if c.ID != "" && op.NewID != "" && c.ID != op.NewID {
		return fmt.Errorf("new_id %q and clip.id %q differ; give one of them", op.NewID, c.ID)
	}
	want := c.ID
	if want == "" {
		want = op.NewID
	}
	base := "c_" + c.Type
	if c.Media != "" {
		base = "c_" + strings.TrimPrefix(c.Media, "m_")
	} else if c.Type == project.ClipText {
		if s := slug(c.Text); s != "" {
			base = "c_" + strings.Trim(firstN(s, 20), "_")
		}
	}
	id, err := ed.newID(want, base)
	if err != nil {
		return err
	}
	c.ID = id
	t := &ed.p.Tracks[ti]
	t.Clips = append(t.Clips, c)
	sortClips(t)
	ed.touch(i, id)
	ed.newIDs = append(ed.newIDs, fmt.Sprintf("ops[%d] %s", i, id))
	return nil
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// shiftAfter moves every clip of track t that starts at or after from by
// delta ms (ripple), except the clip with ID skip.
func (ed *editor) shiftAfter(i int, t *project.Track, from, delta int64, skip string) {
	if delta == 0 {
		return
	}
	for ci := range t.Clips {
		c := &t.Clips[ci]
		if c.ID != skip && c.AtMS >= from {
			c.AtMS += delta
			ed.touch(i, c.ID)
		}
	}
	sortClips(t)
}

func (ed *editor) trim(i int, op editOp) error {
	ti, ci := findClip(ed.p, op.ID)
	if ti < 0 {
		return notFound(ed.p, op.ID, "clip")
	}
	if op.StartMS == nil && op.EndMS == nil {
		return errors.New(`trim needs "start_ms", "end_ms" or both (the clip's new edges on the timeline)`)
	}
	t := &ed.p.Tracks[ti]
	c := &t.Clips[ci]
	a, b := c.AtMS, clipEnd(ed.p, c)
	ns, ne := a, b
	if op.StartMS != nil {
		ns = *op.StartMS
	}
	if op.EndMS != nil {
		ne = *op.EndMS
	}
	if ns < 0 {
		return fmt.Errorf("start_ms %d is negative", ns)
	}
	if ne <= ns {
		return fmt.Errorf("clip %q would be empty: start_ms %d must be less than end_ms %d", c.ID, ns, ne)
	}
	if c.HasSourceWindow() {
		m := ed.p.MediaByID(c.Media)
		if m == nil {
			return fmt.Errorf("clip %q: media %q not found", c.ID, c.Media)
		}
		s := c.SpeedFactor()
		out, _ := ed.p.ClipOut(c)
		in2 := c.InMS + int64(math.Round(float64(ns-a)*s))
		out2 := out + int64(math.Round(float64(ne-b)*s))
		if in2 < 0 {
			return fmt.Errorf("clip %q can start at most %d ms earlier (in_ms is %d; the source has nothing before 0)", c.ID, int64(math.Floor(float64(c.InMS)/s)), c.InMS)
		}
		if out2 > m.Probe.DurationMS {
			return fmt.Errorf("clip %q can end at most %d ms later (out_ms is %d; media %q is %d ms long)", c.ID, int64(math.Floor(float64(m.Probe.DurationMS-out)/s)), out, m.ID, m.Probe.DurationMS)
		}
		c.InMS, c.OutMS = in2, out2
	} else {
		c.DurationMS = ne - ns
	}
	newStart := ns
	if op.Ripple {
		newStart = a // ripple: the clip keeps its place, later clips follow its end
	}
	c.AtMS = newStart
	id := c.ID
	newEnd := clipEnd(ed.p, c)
	ed.touch(i, id)
	if op.Ripple {
		ed.shiftAfter(i, t, b, newEnd-b, id)
	}
	sortClips(t)
	return nil
}

func (ed *editor) remove(i int, op editOp) error {
	if ti := findTrack(ed.p, op.ID); ti >= 0 {
		if op.Ripple {
			return errors.New("ripple applies to clips, not tracks")
		}
		ed.p.Tracks = append(ed.p.Tracks[:ti], ed.p.Tracks[ti+1:]...)
		ed.touch(i, op.ID)
		return nil
	}
	ti, ci := findClip(ed.p, op.ID)
	if ti < 0 {
		return notFound(ed.p, op.ID, "clip or track")
	}
	t := &ed.p.Tracks[ti]
	c := t.Clips[ci]
	a, b := c.AtMS, clipEnd(ed.p, &c)
	t.Clips = append(t.Clips[:ci], t.Clips[ci+1:]...)
	ed.touch(i, c.ID)
	// The clip that followed this one can't transition from it any more.
	for k := range t.Clips {
		n := &t.Clips[k]
		if n.AtMS == b && n.TransitionIn != nil {
			ed.notes = append(ed.notes, fmt.Sprintf("ops[%d]: removed the transition_in of %s (the clip before it is gone)", i, n.ID))
			n.TransitionIn = nil
			ed.touch(i, n.ID)
		}
	}
	if op.Ripple {
		ed.shiftAfter(i, t, b, a-b, "")
	}
	return nil
}

func (ed *editor) update(i int, op editOp) error {
	if len(op.Set) == 0 || op.Set[0] != '{' {
		return errors.New(`update needs "set": an object of fields to change (null resets a field to its default)`)
	}
	var patch map[string]any
	dec := json.NewDecoder(bytes.NewReader(op.Set))
	dec.UseNumber()
	if err := dec.Decode(&patch); err != nil {
		return fmt.Errorf("set: %v", jsonErr(err))
	}
	if ti := findTrack(ed.p, op.ID); ti >= 0 {
		t := &ed.p.Tracks[ti]
		for k := range patch {
			if k != "name" && k != "enabled" {
				return fmt.Errorf("a track's %q can't be changed with update (only name and enabled; move clips with add_clip/remove)", k)
			}
		}
		clips := t.Clips
		t.Clips = nil
		if err := patchInto(t, patch); err != nil {
			t.Clips = clips
			return err
		}
		t.Clips = clips
		ed.touch(i, t.ID)
		return nil
	}
	ti, ci := findClip(ed.p, op.ID)
	if ti < 0 {
		return notFound(ed.p, op.ID, "clip or track")
	}
	c := &ed.p.Tracks[ti].Clips[ci]
	for _, k := range []string{"id", "type"} {
		if v, ok := patch[k]; ok && fmt.Sprint(v) != map[string]string{"id": c.ID, "type": c.Type}[k] {
			return fmt.Errorf("a clip's %s can't be changed (remove it and add a new clip instead)", k)
		}
	}
	if err := patchInto(c, patch); err != nil {
		return err
	}
	ed.touch(i, c.ID)
	sortClips(&ed.p.Tracks[ti])
	return nil
}

// patchInto applies a JSON merge patch (RFC 7386: objects merge, null
// removes, everything else replaces) to v through its JSON form.
func patchInto(v any, patch map[string]any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return err
	}
	merged, err := json.Marshal(mergePatch(doc, patch))
	if err != nil {
		return err
	}
	// Decode into a fresh value so removed fields become zero (default).
	fresh := newOfSameType(v)
	if err := decodeStrict(merged, fresh.ptr); err != nil {
		return fmt.Errorf("set: %v", err)
	}
	fresh.store()
	return nil
}

type freshValue struct {
	ptr   any
	store func()
}

func newOfSameType(v any) freshValue {
	switch x := v.(type) {
	case *project.Clip:
		var n project.Clip
		return freshValue{&n, func() { *x = n }}
	case *project.Track:
		var n project.Track
		return freshValue{&n, func() { *x = n }}
	}
	panic(fmt.Sprintf("patchInto: unsupported %T", v))
}

func mergePatch(target any, patch any) any {
	pm, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	tm, ok := target.(map[string]any)
	if !ok {
		tm = map[string]any{}
	}
	for k, v := range pm {
		if v == nil {
			delete(tm, k)
			continue
		}
		tm[k] = mergePatch(tm[k], v)
	}
	return tm
}

var (
	itemPathRE = regexp.MustCompile(`^/(tracks|media)/(\d+)(?:/clips/(\d+))?`)
	itemNameRE = regexp.MustCompile(`(?:clip|track|media) "([^"]+)"`)
)

// attribute prefixes a problem of the edited project with the op that
// caused it: the last op that changed the item the problem is about.
func (ed *editor) attribute(p project.Problem) string {
	var ids []string
	if m := itemPathRE.FindStringSubmatch(p.Path); m != nil {
		a, _ := strconv.Atoi(m[2])
		switch {
		case m[1] == "media" && a < len(ed.p.Media):
			ids = append(ids, ed.p.Media[a].ID)
		case m[1] == "tracks" && a < len(ed.p.Tracks):
			t := &ed.p.Tracks[a]
			if m[3] != "" {
				if c, _ := strconv.Atoi(m[3]); c < len(t.Clips) {
					ids = append(ids, t.Clips[c].ID)
				}
			}
			ids = append(ids, t.ID)
		}
	}
	for _, m := range itemNameRE.FindAllStringSubmatch(p.Message, -1) {
		ids = append(ids, m[1])
	}
	op := -1
	for _, id := range ids {
		if i, ok := ed.touched[id]; ok {
			op = max(op, i)
		}
	}
	if op < 0 {
		return "result: " + problemText(p)
	}
	return fmt.Sprintf("ops[%d]: %s", op, problemText(p))
}
