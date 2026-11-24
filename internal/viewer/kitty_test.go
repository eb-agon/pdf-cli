package viewer

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClearKittyGraphicsDeletesOwnedPlacements(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	saved := os.Stdout
	os.Stdout = w
	d := &DocumentViewer{lastKittyImageID: 41, flashRuleID: 42}
	d.clearKittyGraphics()
	os.Stdout = saved
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	for _, id := range []string{"i=41", "i=42"} {
		if !strings.Contains(string(out), id) {
			t.Errorf("cleanup output %q does not delete %s", out, id)
		}
	}
	if d.lastKittyImageID != 0 || d.flashRuleID != 0 {
		t.Errorf("ids after cleanup: page=%d rule=%d, want both zero",
			d.lastKittyImageID, d.flashRuleID)
	}
}

// captureStdout returns what f writes to standard output.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	saved := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		out, _ := io.ReadAll(r)
		done <- out
	}()
	f()
	os.Stdout = saved
	w.Close()
	return string(<-done)
}

// writeTestPNG writes a PNG of incompressible pixels, large enough that its
// transmission takes several chunks.
func writeTestPNG(t *testing.T) (path string, data []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 96, 96))
	seed := uint32(1)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = byte(seed >> 24)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	path = filepath.Join(t.TempDir(), "page.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path, buf.Bytes()
}

// kittyCommands splits terminal output into kitty graphics commands, each as
// its control keys and its payload.
func kittyCommands(t *testing.T, out string) (controls []map[string]string, payloads []string) {
	t.Helper()
	for _, part := range strings.Split(out, "\x1b\\") {
		if part == "" {
			continue
		}
		body, ok := strings.CutPrefix(part, "\x1b_G")
		if !ok {
			t.Fatalf("output between commands is not a kitty command: %q", part)
		}
		control, payload, _ := strings.Cut(body, ";")
		keys := map[string]string{}
		for _, kv := range strings.Split(control, ",") {
			k, v, _ := strings.Cut(kv, "=")
			keys[k] = v
		}
		controls = append(controls, keys)
		payloads = append(payloads, payload)
	}
	return controls, payloads
}

func TestPageIsPlacedAtTheCursorOutsideTmux(t *testing.T) {
	useTerminal(t, "kitty", false)
	path, data := writeTestPNG(t)

	out := captureStdout(t, func() {
		if err := kittySendPNGDirect(path, 77, 40, 20, 0); err != nil {
			t.Errorf("send: %v", err)
		}
	})

	controls, payloads := kittyCommands(t, out)
	if len(controls) < 2 {
		t.Fatalf("%d commands, want the image split into several chunks", len(controls))
	}
	first := controls[0]
	if first["a"] != "T" || first["i"] != "77" || first["c"] != "40" || first["r"] != "20" || first["z"] != "0" {
		t.Errorf("first command %v does not place image 77 over 40x20 cells", first)
	}
	if _, virtual := first["U"]; virtual {
		t.Errorf("first command %v is a virtual placement outside tmux", first)
	}
	got, err := base64.StdEncoding.DecodeString(strings.Join(payloads, ""))
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("chunks do not reassemble into the PNG (err %v, %d of %d bytes)", err, len(got), len(data))
	}
}

func TestPageIsUploadedThroughTmuxAsAVirtualPlacement(t *testing.T) {
	useTerminal(t, "kitty", true)
	path, data := writeTestPNG(t)

	out := captureStdout(t, func() {
		if err := kittySendPNG(path, 0x2A000064, 40, 20, 0); err != nil {
			t.Errorf("send: %v", err)
		}
	})

	// Every command travels in its own passthrough wrapper; unwrap them.
	var unwrapped strings.Builder
	for _, part := range strings.SplitAfter(out, "\x1b\x1b\\\x1b\\") {
		if part == "" {
			continue
		}
		body, ok := strings.CutPrefix(part, "\x1bPtmux;")
		if !ok || !strings.HasSuffix(body, "\x1b\\") {
			t.Fatalf("command not wrapped for tmux: %q", part[:min(len(part), 60)])
		}
		unwrapped.WriteString(strings.ReplaceAll(strings.TrimSuffix(body, "\x1b\\"), "\x1b\x1b", "\x1b"))
	}

	controls, payloads := kittyCommands(t, unwrapped.String())
	if len(controls) < 2 {
		t.Fatalf("%d commands, want the image split into several chunks", len(controls))
	}
	first := controls[0]
	if first["a"] != "T" || first["U"] != "1" || first["i"] != "704643172" || first["c"] != "40" || first["r"] != "20" {
		t.Errorf("first command %v is not a virtual placement of the image over 40x20 cells", first)
	}
	for i, keys := range controls {
		// A reply would come back through tmux as typed keys.
		if keys["q"] != "2" {
			t.Errorf("command %d %v asks the terminal for a reply", i, keys)
		}
	}
	got, err := base64.StdEncoding.DecodeString(strings.Join(payloads, ""))
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("chunks do not reassemble into the PNG (err %v, %d of %d bytes)", err, len(got), len(data))
	}
}

func TestImagesAreDeletedThroughTmux(t *testing.T) {
	useTerminal(t, "kitty", true)
	out := captureStdout(t, func() { kittyDeleteImage(9) })
	if want := tmuxWrap("\x1b_Ga=d,d=I,i=9,q=2;\x1b\\"); out != want {
		t.Errorf("delete = %q, want %q", out, want)
	}
}

func TestImageIDsThroughTmuxAreOnesAPlaceholderCanName(t *testing.T) {
	useTerminal(t, "kitty", true)
	for i := 0; i < 600; i++ {
		if id := nextKittyImageID(); id&0x00FFFF00 != 0 || id>>24 == 0 || id&0xFF < 16 {
			t.Fatalf("id %#x cannot be named by a 256-colour placeholder cell", id)
		}
	}
}
