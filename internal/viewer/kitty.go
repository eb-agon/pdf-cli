package viewer

// Kitty graphics transmission, bypassing go-termimg for the kitty path.
//
// go-termimg's kitty renderer transmits f=32 raw RGBA: a fullscreen retina page
// at the 2x supersample is ~12 MP ≈ 48 MB of pixels ≈ 64 MB of base64 escape
// data through the PTY on EVERY page display, even render-cache hits (plus a
// full PNG decode and an RGBA copy per display). That, not MuPDF rendering, is
// what made agterm/ghostty feel slow.
//
// Instead we hand the terminal the PNG that savePageAsImage already wrote:
//   - t=f (file transmission): send just the base64-encoded *path* (~200 bytes);
//     the terminal opens and decodes the PNG itself. Probed once at startup.
//   - fallback t=d (direct): stream the on-disk PNG bytes as chunked base64.
//     Still 20-40x less data than raw RGBA, and no decode/re-encode round trip.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// kittyImageID is a process-wide counter for kitty graphics image ids, seeded
// with pid+time so ids from a previous run still held by the terminal are not
// reused (and then wrongly deleted by the swap logic).
var kittyImageID = uint32(os.Getpid()<<16) + uint32(time.Now().UnixMicro()&0xFFFF)

// nextKittyImageID returns a fresh image id. Through tmux the id has to be one
// a placeholder cell can spell (see placeholderImageID).
func nextKittyImageID() uint32 {
	n := atomic.AddUint32(&kittyImageID, 1)
	if termViaTmux {
		return placeholderImageID(uint32(os.Getpid()), n)
	}
	return n
}

// kittyCommand formats one kitty graphics command, wrapped for tmux
// passthrough when the terminal is reached through tmux.
func kittyCommand(control, payload string) string {
	cmd := "\x1b_G" + control + ";" + payload + "\x1b\\"
	if termViaTmux {
		return tmuxWrap(cmd)
	}
	return cmd
}

const (
	kittyXferDirect = iota // chunked base64 of the PNG bytes (mandatory protocol surface)
	kittyXferFile          // t=f: terminal reads the PNG from disk itself
)

var (
	kittyXferMode = kittyXferDirect
	kittyXferOnce sync.Once
)

// probeKittyTransferMode decides, once per process, how to ship pages to a
// kitty-protocol terminal. Must run before the stdin-reading goroutine starts,
// because the probe reads a query response from the tty. DOCVIEWER_KITTY_XFER
// (file|direct) overrides the probe.
func (d *DocumentViewer) probeKittyTransferMode() {
	kittyXferOnce.Do(func() {
		switch os.Getenv("DOCVIEWER_KITTY_XFER") {
		case "file":
			kittyXferMode = kittyXferFile
			return
		case "direct":
			return
		}
		// Through tmux the terminal is usually on another machine (the viewer
		// on a remote box, the terminal at the far end of ssh) and cannot be
		// asked without its reply coming back as keystrokes; stay on direct.
		if d.detectTerminalType() != "kitty" || termViaTmux {
			return
		}
		if probeKittyFileXfer(d.tempDir) {
			kittyXferMode = kittyXferFile
		}
	})
}

// probeKittyFileXfer asks the terminal whether it accepts t=f file transmission
// by sending an a=q query for a tiny probe PNG and reading the ;OK response.
// ghostty/agterm support t=f, but this keeps us safe on terminals that don't
// (the query is defined to not display or retain anything).
func probeKittyFileXfer(tempDir string) bool {
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return false
	}
	probePath := filepath.Join(tempDir, "probe.png")
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		return false
	}
	if err := os.WriteFile(probePath, buf.Bytes(), 0o644); err != nil {
		return false
	}
	absPath, err := filepath.Abs(probePath)
	if err != nil {
		return false
	}

	// Success response: ESC _ G i=<id> ; OK ESC \  — anything else (an error
	// message, as when the terminal is at the far end of ssh and has no such
	// file; no reply at all) means we stay on direct transmission.
	id := nextKittyImageID()
	reply, ok := ttyQuery(fmt.Sprintf("\x1b_Ga=q,f=100,t=f,i=%d;%s\x1b\\",
		id, base64.StdEncoding.EncodeToString([]byte(absPath))))
	return ok && strings.Contains(reply, ";OK")
}

// kittyDeleteImage removes an image and its placements by id.
func kittyDeleteImage(id uint32) {
	fmt.Print(kittyCommand(fmt.Sprintf("a=d,d=I,i=%d,q=2", id), ""))
}

// clearKittyGraphics removes every placement owned by this viewer. Redraws
// delete the previous page as they swap in its replacement, but the final page
// has no successor to trigger that deletion. Leaving it behind lets a z=0
// placement cover parts of whatever the terminal draws after docviewer exits.
func (d *DocumentViewer) clearKittyGraphics() {
	d.clearFlashRule()
	if d.lastKittyImageID == 0 {
		return
	}
	kittyDeleteImage(d.lastKittyImageID)
	d.lastKittyImageID = 0
}

// syncRulePNG writes (once) the overlay drawn across the page at a forward-sync
// row: a translucent red band in the middle of an otherwise transparent tile.
// The tile is stretched to cols x 1 cells on placement, so its width is
// irrelevant and its height sets the band's share of a cell — 3/32 of a cell,
// thin enough to sit across a text line without hiding it.
func syncRulePNG(tempDir string) (string, error) {
	path := filepath.Join(tempDir, "syncrule.png")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return "", err
	}
	const w, h = 8, 32
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := h/2 - 1; y <= h/2+1; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 200, G: 30, B: 30, A: 150})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// kittySendPNG transmits an on-disk PNG at the current cursor position, scaled
// to cols x rows cells, using the transfer mode chosen by the startup probe.
// z is the stacking order: 0 for pages, a positive value for overlays drawn on
// top of them (the sync rule).
//
// Through tmux the image is only uploaded, as a virtual placement that draws
// nothing: the caller shows it by writing placeholderGrid for the same id and
// cell box.
func kittySendPNG(imagePath string, id uint32, cols, rows, z int) error {
	if kittyXferMode == kittyXferFile {
		if abs, err := filepath.Abs(imagePath); err == nil {
			_, err = fmt.Print(kittyCommand(
				kittyPlacement(id, cols, rows, z)+",f=100,t=f,q=2",
				base64.StdEncoding.EncodeToString([]byte(abs))))
			return err
		}
		// Path resolution failed; fall through to direct transmission.
	}
	return kittySendPNGDirect(imagePath, id, cols, rows, z)
}

// kittyPlacement is the part of a transmit command that says where the image
// goes: on screen at the cursor, or (through tmux) a virtual placement for
// placeholder cells to reference.
func kittyPlacement(id uint32, cols, rows, z int) string {
	if termViaTmux {
		return fmt.Sprintf("a=T,U=1,i=%d,c=%d,r=%d", id, cols, rows)
	}
	return fmt.Sprintf("a=T,i=%d,c=%d,r=%d,z=%d", id, cols, rows, z)
}

// kittySendPNGDirect streams the PNG file bytes as chunked base64 (t=d). The
// kitty protocol caps each escape's payload at 4096 bytes of encoded data.
func kittySendPNGDirect(imagePath string, id uint32, cols, rows, z int) error {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return err
	}
	enc := base64.StdEncoding.EncodeToString(data)

	const chunkSize = 4096
	var out strings.Builder
	out.Grow(len(enc) + (len(enc)/chunkSize+1)*48 + 64)

	first := true
	for len(enc) > 0 {
		n := min(chunkSize, len(enc))
		piece := enc[:n]
		enc = enc[n:]
		more := 0
		if len(enc) > 0 {
			more = 1
		}
		if first {
			first = false
			out.WriteString(kittyCommand(
				fmt.Sprintf("%s,f=100,q=2,m=%d", kittyPlacement(id, cols, rows, z), more), piece))
		} else {
			out.WriteString(kittyCommand(fmt.Sprintf("m=%d,q=2", more), piece))
		}
	}

	_, err = os.Stdout.WriteString(out.String())
	return err
}
