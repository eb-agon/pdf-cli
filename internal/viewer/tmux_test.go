package viewer

import (
	"testing"
)

func TestTmuxWrapDoublesEveryEscape(t *testing.T) {
	got := tmuxWrap("\x1b_Ga=d,i=7;\x1b\\")
	want := "\x1bPtmux;\x1b\x1b_Ga=d,i=7;\x1b\x1b\\\x1b\\"
	if got != want {
		t.Errorf("wrapped = %q, want %q", got, want)
	}
}

func TestPlaceholderTerminalsAreRecognisedAsTmuxNamesThem(t *testing.T) {
	cases := map[string]bool{
		"ghostty 1.4.0 xterm-256color": true,
		"xterm-ghostty":                true,
		"kitty(0.42.1) xterm-kitty":    true,
		"iterm2 3.5.0 xterm-256color":  false,
		"wezterm 20240203 xterm":       false,
		"":                             false,
	}
	for outer, want := range cases {
		if got := speaksKittyPlaceholders(outer); got != want {
			t.Errorf("%q: placeholders = %v, want %v", outer, got, want)
		}
	}
}
