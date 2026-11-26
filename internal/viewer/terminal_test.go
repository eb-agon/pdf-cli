package viewer

import (
	"testing"
)

// useTerminal pins what the viewer believes about its terminal for one test,
// so that no test asks the real terminal or a real tmux.
func useTerminal(t *testing.T, kind string, viaTmux bool) {
	t.Helper()
	termOnce.Do(func() {})
	savedKind, savedTmux := termKind, termViaTmux
	termKind, termViaTmux = kind, viaTmux
	t.Cleanup(func() { termKind, termViaTmux = savedKind, savedTmux })
}

func TestTerminalIsIdentifiedFromEnvironmentTmuxAndProbe(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		tmux      bool
		outer     string // what tmux says is attached
		probe     bool   // whether the terminal answers the kitty graphics query
		wantKind  string
		wantTmux  bool
		wantProbe bool // whether the terminal may be queried at all
	}{
		{
			name:     "ghostty named by the environment",
			env:      map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-ghostty"},
			wantKind: "kitty",
		},
		{
			name:      "ghostty over ssh looks like xterm and is asked",
			env:       map[string]string{"TERM": "xterm-256color"},
			probe:     true,
			wantKind:  "kitty",
			wantProbe: true,
		},
		{
			name:      "a plain xterm over ssh stays xterm",
			env:       map[string]string{"TERM": "xterm-256color"},
			wantKind:  "xterm",
			wantProbe: true,
		},
		{
			name:     "foot is not asked",
			env:      map[string]string{"TERM": "foot"},
			probe:    true,
			wantKind: "foot",
		},
		{
			name:     "tmux attached from ghostty over ssh",
			env:      map[string]string{"TERM": "tmux-256color", "TERM_PROGRAM": "tmux"},
			tmux:     true,
			outer:    "ghostty 1.4.0 xterm-256color",
			wantKind: "kitty",
			wantTmux: true,
		},
		{
			name:     "tmux attached from kitty",
			env:      map[string]string{"TERM": "tmux-256color"},
			tmux:     true,
			outer:    "kitty(0.42.1) xterm-kitty",
			wantKind: "kitty",
			wantTmux: true,
		},
		{
			name:     "tmux attached from a terminal without placeholders",
			env:      map[string]string{"TERM": "tmux-256color"},
			tmux:     true,
			outer:    "iterm2 3.5.0 xterm-256color",
			probe:    true,
			wantKind: "tmux",
		},
		{
			name:     "tmux started inside kitty, attached from elsewhere",
			env:      map[string]string{"TERM": "tmux-256color", "KITTY_WINDOW_ID": "3"},
			tmux:     true,
			outer:    "xterm-256color",
			wantKind: "tmux",
		},
		{
			name:     "tmux with no client attached",
			env:      map[string]string{"TERM": "screen-256color"},
			tmux:     true,
			wantKind: "screen",
		},
		{
			name:     "kitty forced through tmux",
			env:      map[string]string{"TERM": "tmux-256color", "DOCVIEWER_GRAPHICS": "kitty"},
			tmux:     true,
			wantKind: "kitty",
			wantTmux: true,
		},
		{
			name:     "generic path forced on ghostty",
			env:      map[string]string{"TERM_PROGRAM": "ghostty", "DOCVIEWER_GRAPHICS": "termimg"},
			wantKind: "xterm",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probed := false
			kind, viaTmux := resolveTerminal(
				func(k string) string { return tc.env[k] },
				tc.tmux,
				func() string { return tc.outer },
				func() bool { probed = true; return tc.probe },
			)
			if kind != tc.wantKind || viaTmux != tc.wantTmux {
				t.Errorf("got (%q, tmux=%v), want (%q, tmux=%v)", kind, viaTmux, tc.wantKind, tc.wantTmux)
			}
			if probed != tc.wantProbe {
				t.Errorf("terminal queried = %v, want %v", probed, tc.wantProbe)
			}
		})
	}
}

func TestDeviceAttributesReportEndsAReply(t *testing.T) {
	cases := []struct {
		in       string
		wantHead string
		found    bool
	}{
		{"\x1b_Gi=31;OK\x1b\\\x1b[?62;22c", "\x1b_Gi=31;OK\x1b\\", true},
		{"\x1b[?1;2c", "", true},
		{"\x1b[6;32;16t\x1b[?62c", "\x1b[6;32;16t", true},
		{"\x1b[6;32;16t", "", false},
	}
	for _, tc := range cases {
		loc := da1Reply.FindStringIndex(tc.in)
		if (loc != nil) != tc.found {
			t.Errorf("%q: report found = %v, want %v", tc.in, loc != nil, tc.found)
			continue
		}
		if loc != nil && tc.in[:loc[0]] != tc.wantHead {
			t.Errorf("%q: reply before the report = %q, want %q", tc.in, tc.in[:loc[0]], tc.wantHead)
		}
	}
}

func TestCellSizeReplyIsHeightThenWidth(t *testing.T) {
	m := cellSizeReply.FindStringSubmatch("\x1b[6;32;16t")
	if m == nil || m[1] != "32" || m[2] != "16" {
		t.Errorf("parsed %v, want height 32 and width 16", m)
	}
}

func TestTmuxCellInfo(t *testing.T) {
	for _, c := range []struct {
		name   string
		out    string
		w, h   float64
		mayAsk bool
	}{
		{"tmux knows the size", "17,37,1,1,1\n", 17, 37, true},
		{"no pixel size on the connection", "0,0,1,1,1", 0, 0, true},
		{"another pane is active", "0,0,0,1,1", 0, 0, false},
		{"another window is current", "0,0,1,0,1", 0, 0, false},
		{"nobody attached", "0,0,1,1,0", 0, 0, false},
		{"two clients attached", "0,0,1,1,2", 0, 0, true},
		{"a tmux without the cell formats", ",,1,1,1", 0, 0, true},
		{"half an answer is no answer", "17,0,1,1,1", 0, 0, true},
		{"not tmux's answer at all", "no server running", 0, 0, false},
	} {
		w, h, mayAsk := parseTmuxCellInfo(c.out)
		if w != c.w || h != c.h || mayAsk != c.mayAsk {
			t.Errorf("%s: got %vx%v mayAsk %v, want %vx%v mayAsk %v", c.name, w, h, mayAsk, c.w, c.h, c.mayAsk)
		}
	}
}

func TestCellSizeIsReadFromTheReply(t *testing.T) {
	// Height comes first in the reply; typing can be mixed in around it.
	if w, h := parseCellSizeReply("j\x1b[6;37;17tk"); w != 17 || h != 37 {
		t.Errorf("got %vx%v, want 17x37", w, h)
	}
	for _, reply := range []string{"", "\x1b[6;0;17t", "\x1b[4;800;1200t"} {
		if w, h := parseCellSizeReply(reply); w != 0 || h != 0 {
			t.Errorf("%q: got %vx%v, want no size", reply, w, h)
		}
	}
}
