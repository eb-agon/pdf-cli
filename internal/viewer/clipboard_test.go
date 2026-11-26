package viewer

import (
	"encoding/base64"
	"strings"
	"testing"
)

const helloOSC52 = "\x1b]52;c;aGVsbG8=\x1b\\" // base64("hello")

func stubTmuxBuffer(t *testing.T) *[]string {
	t.Helper()
	var got []string
	saved := setTmuxBuffer
	setTmuxBuffer = func(text string) { got = append(got, text) }
	t.Cleanup(func() { setTmuxBuffer = saved })
	return &got
}

func TestClipboardSequenceCarriesTheTextAsBase64(t *testing.T) {
	if got := clipboardSequence("hello"); got != helloOSC52 {
		t.Errorf("got %q, want %q", got, helloOSC52)
	}
	text := "naïve — “quoted”\nsecond line"
	seq := clipboardSequence(text)
	body := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b]52;c;"), "\x1b\\")
	if dec, err := base64.StdEncoding.DecodeString(body); err != nil || string(dec) != text {
		t.Errorf("body decodes to %q (%v), want the text back", dec, err)
	}
}

func TestCopyOutsideTmuxWritesTheSequenceToTheTerminal(t *testing.T) {
	t.Setenv("TMUX", "")
	useTerminal(t, "kitty", false)
	buffers := stubTmuxBuffer(t)
	out := captureStdout(t, func() { copyToClipboard("hello") })
	if out != helloOSC52 {
		t.Errorf("wrote %q, want %q", out, helloOSC52)
	}
	if len(*buffers) != 0 {
		t.Errorf("tmux was given a buffer outside tmux")
	}
}

func TestCopyThroughTmuxSetsItsBufferAndReachesPastIt(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-test/default,1,0")
	useTerminal(t, "kitty", true)
	buffers := stubTmuxBuffer(t)
	out := captureStdout(t, func() { copyToClipboard("hello") })
	if want := tmuxWrap(helloOSC52); out != want {
		t.Errorf("wrote %q, want the sequence wrapped for tmux %q", out, want)
	}
	if len(*buffers) != 1 || (*buffers)[0] != "hello" {
		t.Errorf("tmux buffers set: %q, want just \"hello\"", *buffers)
	}
}

func TestCopyInTmuxWithoutPassthroughLeavesItToTmux(t *testing.T) {
	// A terminal the viewer does not talk past tmux to: a raw sequence would
	// be tmux's to interpret, and tmux has already been handed the text.
	t.Setenv("TMUX", "/tmp/tmux-test/default,1,0")
	useTerminal(t, "termimg", false)
	buffers := stubTmuxBuffer(t)
	out := captureStdout(t, func() { copyToClipboard("hello") })
	if out != "" {
		t.Errorf("wrote %q to the terminal, want nothing", out)
	}
	if len(*buffers) != 1 {
		t.Errorf("tmux buffers set: %d, want 1", len(*buffers))
	}
}
