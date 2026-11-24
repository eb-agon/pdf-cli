package viewer

// Running inside tmux.
//
// tmux sits between the viewer and the terminal that draws the images, and by
// itself gets in the way three times over: TERM says "tmux" whatever the
// terminal outside is, escape sequences tmux does not know are dropped unless
// the pane allows passthrough, and replies to queries come back through tmux's
// key parser as if they had been typed. So inside tmux the viewer asks tmux
// which terminal is attached instead of querying it, turns passthrough on for
// its own pane for as long as it runs, wraps every graphics command, sends none
// that asks for a reply, and shows images through unicode placeholders
// (placeholder.go).

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

// inTmux reports whether the viewer's output goes through a tmux server it can
// talk to.
func inTmux() bool {
	return os.Getenv("TMUX") != ""
}

// tmuxCommand runs a tmux command against the viewer's own pane and returns
// its trimmed output. The pane is named explicitly: without it tmux picks the
// most recently active pane, which need not be this one.
func tmuxCommand(args ...string) (string, error) {
	if pane := os.Getenv("TMUX_PANE"); pane != "" && len(args) > 0 {
		args = append([]string{args[0], "-t", pane}, args[1:]...)
	}
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// tmuxOuterTerminal names the terminal attached to this pane's session, in
// lower case: the name and version it gave tmux when asked, and the TERM it was
// started with. Empty when no client is attached or tmux cannot be reached.
func tmuxOuterTerminal() string {
	out, err := tmuxCommand("display-message", "-p", "#{client_termtype} #{client_termname}")
	if err != nil {
		return ""
	}
	return strings.ToLower(out)
}

// speaksKittyPlaceholders reports whether a terminal, named as tmux names it,
// implements kitty graphics with unicode placeholders.
func speaksKittyPlaceholders(outer string) bool {
	return strings.Contains(outer, "ghostty") || strings.Contains(outer, "kitty")
}

// tmuxWrap wraps one escape sequence so tmux hands it to the outer terminal
// untouched: a DCS addressed to tmux, with every ESC of the payload doubled.
func tmuxWrap(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// tmuxPassthroughSettle is how long the viewer waits before handing the pane's
// passthrough option back. The last graphics commands (deleting the images on
// exit) are still in the pane's output when the viewer finishes, and the option
// change travels to the server on another connection; without the wait tmux can
// apply the change first and drop them.
const tmuxPassthroughSettle = 50 * time.Millisecond

// tmuxAllowPassthrough lets this pane send escape sequences through tmux and
// returns the function that puts the pane's option back. "all" rather than
// "on": a page drawn while the pane is not visible (an auto-reload in another
// window) must still upload its image, or the placeholders the pane comes back
// with point at nothing. A tmux too old to know "all" gets "on"; one too old to
// have the option passes everything through already.
func tmuxAllowPassthrough() (restore func()) {
	restore = func() {}
	if effective, _ := tmuxCommand("show-options", "-pAv", "allow-passthrough"); effective == "all" {
		return restore
	}
	// What the pane itself had set, as opposed to what it inherited: empty means
	// the option goes back to inheriting.
	own, _ := tmuxCommand("show-options", "-pv", "allow-passthrough")
	if _, err := tmuxCommand("set-option", "-p", "allow-passthrough", "all"); err != nil {
		if _, err := tmuxCommand("set-option", "-p", "allow-passthrough", "on"); err != nil {
			return restore
		}
	}
	return func() {
		time.Sleep(tmuxPassthroughSettle)
		if own == "" {
			tmuxCommand("set-option", "-pu", "allow-passthrough")
		} else {
			tmuxCommand("set-option", "-p", "allow-passthrough", own)
		}
	}
}
