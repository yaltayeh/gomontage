package engine

import "testing"

func TestEscapeFilterValue(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello", "Hello"},
		{"قريباً", "قريباً"},
		// Example from the FFmpeg filters documentation.
		{
			"this is a 'string': may contain one, or more, special characters",
			`this is a \\\'string\\\'\\: may contain one\, or more\, special characters`,
		},
		{`a\b`, `a\\\\b`},
		{"[x];y", `\[x\]\;y`},
		{"100%", "100%"},
		{" pad ", `\\ pad\\ `},
		{"C:/fonts/My Font.ttf", `C\\:/fonts/My Font.ttf`},
	}
	for _, tt := range tests {
		if got := EscapeFilterValue(tt.in); got != tt.want {
			t.Errorf("EscapeFilterValue(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
