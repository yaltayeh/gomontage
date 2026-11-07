package timeline

import (
	"strings"
	"testing"
	"time"

	"github.com/ahmedhodiani/gomontage/clip"
)

func TestDrawtextParams(t *testing.T) {
	st := clip.TextStyle{Size: 40, Color: "#FFFFFF80", FontFamily: "El Messiri", Background: "#000000AA", BoxPadding: 10,
		BorderWidth: 3, BorderColor: "#112233", Align: "right", Direction: "rtl"}
	tc := clip.NewText("a\nb", st)
	pl := clip.ResolvePlacement(tc.WithLayout(clip.Layout{X: 0.5, Y: 0.9, Anchor: clip.AnchorBottom}), 1080, 1920)
	p := drawtextParams(tc, pl)
	want := map[string]string{
		"fontsize": "40", "fontcolor": "#FFFFFF80", "font": "El Messiri", "box": "1", "boxcolor": "#000000AA",
		"boxborderw": "10", "borderw": "3", "bordercolor": "#112233", "text_align": "right",
		"x": "540-0.5*(tw+20)+10", "y": "1728-1*(th+20)+10",
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s = %q, want %q", k, p[k], v)
		}
	}
	if !strings.Contains(p["text"], "‫a‬\n‫b‬") {
		t.Errorf("rtl text not wrapped per line: %q", p["text"])
	}
	if _, ok := p["fontfile"]; ok {
		t.Error("fontfile set without Font")
	}
	st.Font = "f.ttf"
	if p := drawtextParams(clip.NewText("x", st), pl); p["fontfile"] != "f.ttf" || p["font"] != "" {
		t.Errorf("Font takes precedence over FontFamily: %v", p)
	}
}

func TestTextFadeAlpha(t *testing.T) {
	if a := textFadeAlpha(time.Second, 3*time.Second, 0, 0); a != "" {
		t.Errorf("no fades: %q", a)
	}
	if a := textFadeAlpha(time.Second, 3*time.Second, 500*time.Millisecond, time.Second); a != "clip(min((t-1.000)/0.500,(3.000-t)/1.000),0,1)" {
		t.Errorf("fades: %q", a)
	}
}

func TestColorHasAlpha(t *testing.T) {
	for c, want := range map[string]bool{"#000000": false, "#000000FF": false, "#00000080": true, "red@0.5": true, "red": false} {
		if got := colorHasAlpha(c); got != want {
			t.Errorf("%s: %v", c, got)
		}
	}
}
