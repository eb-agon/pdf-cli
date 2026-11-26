package viewer

import (
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
)

// clipboardSequence is the escape sequence (OSC 52) that asks a terminal to
// put text on the system clipboard. It is what makes copying work over ssh:
// the clipboard set is the one of the machine the terminal runs on.
func clipboardSequence(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x1b\\"
}

// copyToClipboard puts text on the system clipboard through the terminal.
//
// Inside tmux it goes two ways. tmux is given the text as a paste buffer and
// told to pass it on (-w), which is how its own copy mode reaches the
// clipboard; and where the viewer already talks past tmux to the terminal, the
// sequence is sent that way too, because whether tmux passes a copy on depends
// on what it believes the terminal can do.
func copyToClipboard(text string) {
	seq := clipboardSequence(text)
	if inTmux() {
		setTmuxBuffer(text)
		if !termViaTmux {
			return
		}
		seq = tmuxWrap(seq)
	}
	os.Stdout.WriteString(seq)
}

// setTmuxBuffer is tmuxSetBuffer, replaceable so tests do not run tmux.
var setTmuxBuffer = tmuxSetBuffer

// tmuxSetBuffer makes text tmux's newest paste buffer and has tmux forward it
// to the clipboard of the attached terminal. A tmux older than 3.2 has no -w;
// it still gets the buffer.
func tmuxSetBuffer(text string) {
	for _, args := range [][]string{{"load-buffer", "-w", "-"}, {"load-buffer", "-"}} {
		cmd := exec.Command("tmux", args...)
		cmd.Stdin = strings.NewReader(text)
		if cmd.Run() == nil {
			return
		}
	}
}
