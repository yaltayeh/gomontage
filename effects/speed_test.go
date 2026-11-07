package effects

import "testing"

func TestSpeedEffect_KeepAudioSpeed(t *testing.T) {
	e := SpeedUp(2)
	if !e.AffectsAudio() {
		t.Error("SpeedUp should retime audio by default")
	}
	k := e.KeepAudioSpeed()
	if k.AffectsAudio() || k.Factor() != 2 || k.DurationFactor() != 0.5 {
		t.Errorf("KeepAudioSpeed: affects %v factor %g duration %g", k.AffectsAudio(), k.Factor(), k.DurationFactor())
	}
	if !e.AffectsAudio() {
		t.Error("KeepAudioSpeed must not modify the original effect")
	}
}
