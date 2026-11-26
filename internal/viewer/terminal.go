package viewer

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/term"
)

func (d *DocumentViewer) getTerminalSize() (int, int) {
	// Try stdout first - it's connected to the correct PTY in Kitty splits
	if width, height, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 && height > 0 {
		return width, height
	}
	// Fallback to /dev/tty
	if tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0); err == nil {
		defer tty.Close()
		if width, height, err := term.GetSize(int(tty.Fd())); err == nil {
			return width, height
		}
	}
	return 80, 24 // Fallback default
}

// The terminal is identified once per process: the answer cannot change while
// the viewer runs, finding it out can mean a round trip to the terminal, and
// that round trip has to happen before the goroutine that reads keys starts.
var (
	termOnce sync.Once
	termKind string

	// termViaTmux is set when images go to a kitty-protocol terminal through
	// tmux: every graphics command is wrapped for passthrough and pages are
	// shown with unicode placeholders (see tmux.go, placeholder.go).
	termViaTmux bool
)

// detectTerminalType names the terminal the viewer draws on. "kitty" means the
// kitty graphics protocol is available, whichever terminal provides it and
// whether or not tmux is in between.
func (d *DocumentViewer) detectTerminalType() string {
	termOnce.Do(func() {
		termKind, termViaTmux = resolveTerminal(os.Getenv, inTmux(), tmuxOuterTerminal, probeKittyGraphics)
	})
	return termKind
}

// resolveTerminal decides the terminal kind and whether graphics go through
// tmux. Its inputs are passed in so the decision can be tested without a
// terminal: getenv reads the environment, outerTerminal asks tmux what is
// attached, probe asks the terminal itself whether it speaks kitty graphics.
//
// DOCVIEWER_GRAPHICS overrides the detection: "kitty" forces the kitty
// protocol (through tmux when inside it), "termimg" forces the generic
// go-termimg path.
func resolveTerminal(getenv func(string) string, tmux bool, outerTerminal func() string, probe func() bool) (kind string, viaTmux bool) {
	envKind := envTerminalType(getenv)
	switch getenv("DOCVIEWER_GRAPHICS") {
	case "kitty":
		return "kitty", tmux
	case "termimg":
		if envKind == "kitty" {
			return "xterm", false
		}
		return envKind, false
	}
	if tmux {
		// Inside tmux the environment describes tmux (or whatever terminal the
		// server happened to be started from), and a query would be answered by
		// tmux or come back as keystrokes. tmux knows what is attached.
		if speaksKittyPlaceholders(outerTerminal()) {
			return "kitty", true
		}
		if envKind == "kitty" {
			return "tmux", false
		}
		return envKind, false
	}
	if envKind == "xterm" || envKind == "unknown" {
		// Over ssh a kitty-protocol terminal looks like any xterm: TERM is
		// rewritten and TERM_PROGRAM is not forwarded. Ask it.
		if probe() {
			return "kitty", false
		}
	}
	return envKind, false
}

// envTerminalType guesses the terminal from the environment alone.
func envTerminalType(getenv func(string) string) string {
	if termProgram := getenv("TERM_PROGRAM"); termProgram != "" {
		switch termProgram {
		case "WezTerm":
			return "wezterm"
		case "ghostty":
			// Ghostty (and agterm, which embeds libghostty) implements the kitty
			// graphics protocol; treat as kitty so it gets the 300-DPI render path.
			return "kitty"
		case "iTerm.app":
			return "iterm2"
		case "Apple_Terminal":
			return "apple_terminal"
		}
	}
	if getenv("KITTY_WINDOW_ID") != "" || getenv("KITTY_PID") != "" {
		return "kitty"
	}
	term := getenv("TERM")
	switch {
	case strings.Contains(term, "kitty"):
		return "kitty"
	case strings.Contains(term, "ghostty"):
		return "kitty" // kitty graphics protocol; check before the "xterm" case
	case strings.Contains(term, "foot"):
		return "foot"
	case strings.Contains(term, "alacritty"):
		return "alacritty"
	case strings.Contains(term, "wezterm"):
		return "wezterm"
	case strings.Contains(term, "xterm"):
		return "xterm"
	case strings.Contains(term, "tmux"):
		return "tmux"
	case strings.Contains(term, "screen"):
		return "screen"
	}
	return "unknown"
}

// ttyQueryTimeout bounds one query round trip. It is a ceiling for a terminal
// that never answers, not the expected wait: ttyQuery returns as soon as the
// reply is complete, so a slow ssh link costs its own latency and no more.
const ttyQueryTimeout = time.Second

// da1Reply matches a primary device attributes report, which every terminal
// sends in answer to CSI c.
var da1Reply = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)

// ttyQuery sends a query to the terminal and returns what came back before the
// terminal's device-attributes report. The query is followed by a
// device-attributes request, which every terminal answers and answers in
// order, so the report marks the end of the reply whether or not the terminal
// understood the query: nothing is left in the input to be read as keys later,
// and an unsupported query costs one round trip rather than a full timeout. ok
// is false when the terminal could not be asked or did not report in time.
//
// Must not run while another goroutine reads the terminal: the two would split
// the reply between them, and the half that reached the key reader would be
// taken for typing.
func ttyQuery(query string) (reply string, ok bool) {
	acc, end := ttyExchange(query+"\x1b[c", da1Reply)
	if end == nil {
		return acc, false
	}
	return acc[:end[0]], true
}

// ttyExchange writes send to the terminal and reads what comes back until it
// contains a match of end, whose location it returns, or until
// ttyQueryTimeout, when the location is nil. The same warning as ttyQuery
// applies: nothing else may be reading the terminal.
func ttyExchange(send string, end *regexp.Regexp) (reply string, loc []int) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", nil
	}
	defer tty.Close()

	// The descriptor is reached through SyscallConn rather than Fd: Fd puts
	// the file back into blocking mode, and a blocking read ignores the
	// deadline below — a terminal that stays silent would hang the viewer
	// until the next keypress, and then swallow it.
	conn, err := tty.SyscallConn()
	if err != nil {
		return "", nil
	}
	var oldState *term.State
	if cerr := conn.Control(func(fd uintptr) { oldState, err = term.MakeRaw(int(fd)) }); cerr != nil || err != nil {
		return "", nil
	}
	defer conn.Control(func(fd uintptr) { term.Restore(int(fd), oldState) })

	if err := tty.SetReadDeadline(time.Now().Add(ttyQueryTimeout)); err != nil {
		return "", nil
	}
	if _, err := tty.WriteString(send); err != nil {
		return "", nil
	}

	var acc []byte
	buf := make([]byte, 256)
	for {
		n, err := tty.Read(buf)
		acc = append(acc, buf[:n]...)
		if loc := end.FindIndex(acc); loc != nil {
			return string(acc), loc
		}
		if err != nil {
			return string(acc), nil
		}
	}
}

// probeKittyGraphics asks the terminal whether it implements the kitty
// graphics protocol, with the query the protocol defines for that: a 1x1 image
// that is checked and discarded. A terminal that implements it answers; any
// other ignores the unknown sequence.
func probeKittyGraphics() bool {
	reply, ok := ttyQuery("\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\")
	return ok && strings.Contains(reply, "\x1b_Gi=31;OK")
}

func (d *DocumentViewer) getTerminalCellSize() (float64, float64) {
	// Check if terminal dimensions changed (resolution/monitor switch)
	cols, rows := d.getTerminalSize()
	if cols != d.lastTermCols || rows != d.lastTermRows {
		// Terminal changed - invalidate cache and re-detect
		d.cellWidth = 0
		d.cellHeight = 0
		d.lastTermCols = cols
		d.lastTermRows = rows
	}

	// Use cached values if available
	if d.cellWidth > 0 && d.cellHeight > 0 {
		return d.cellWidth, d.cellHeight
	}

	// Detect and cache
	d.cellWidth, d.cellHeight = d.detectCellSize()
	return d.cellWidth, d.cellHeight
}

// refreshCellSize forces re-detection of cell size (useful after resolution change)
func (d *DocumentViewer) refreshCellSize() {
	d.cellWidth = 0
	d.cellHeight = 0
	d.lastTermCols = 0
	d.lastTermRows = 0
}

// detectCellSize detects cell size - call before entering raw mode
func (d *DocumentViewer) detectCellSize() (float64, float64) {
	// Check for environment variable override first (most reliable for multi-resolution)
	// Format: DOCVIEWER_CELL_SIZE=WxH (e.g., "12x26")
	if cellSize := os.Getenv("DOCVIEWER_CELL_SIZE"); cellSize != "" {
		var w, h float64
		if _, err := fmt.Sscanf(cellSize, "%fx%f", &w, &h); err == nil && w > 0 && h > 0 {
			return w, h
		}
	}

	// Try Kitty-specific query first (most accurate)
	if kw, kh := d.getKittyCellSize(); kw > 0 && kh > 0 {
		return kw, kh
	}

	// Through tmux the pane's pixel size below cannot be trusted (see
	// tmuxCellSize), so what tmux or the terminal said comes first.
	if termViaTmux {
		tw, th := d.tmuxCellSize()
		d.cellSizeGuessed = tw <= 0 || th <= 0
		if !d.cellSizeGuessed {
			return tw, th
		}
	}

	// Try TIOCGWINSZ pixel size
	pixelWidth, pixelHeight := d.getTerminalPixelSize()
	charWidth, charHeight := d.getTerminalSize()

	if pixelWidth > 0 && pixelHeight > 0 && charWidth > 0 && charHeight > 0 {
		cellWidth := float64(pixelWidth) / float64(charWidth)
		cellHeight := float64(pixelHeight) / float64(charHeight)
		if cellWidth > 4 && cellHeight > 8 {
			return cellWidth, cellHeight
		}
	}

	// What the terminal said at startup, when it can no longer be asked.
	if d.askedCellWidth > 0 && d.askedCellHeight > 0 {
		return d.askedCellWidth, d.askedCellHeight
	}

	// Fallback to hardcoded values
	termType := d.detectTerminalType()
	switch termType {
	case "kitty":
		return 18.0, 36.0
	case "foot":
		return 15.0, 25.0
	case "alacritty":
		return 14.0, 28.0
	case "wezterm":
		return 18.0, 36.0
	case "iterm2":
		return 16.0, 32.0
	case "xterm":
		return 7.0, 14.0
	default:
		return 15.0, 30.0
	}
}

func (d *DocumentViewer) getTerminalPixelSize() (int, int) {
	ws := struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}{}

	// Try stdout first - correct PTY in Kitty splits
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdout),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)))

	if errno == 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
		return int(ws.Xpixel), int(ws.Ypixel)
	}

	// Fallback to /dev/tty
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err == nil {
		defer tty.Close()
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
			uintptr(tty.Fd()),
			uintptr(syscall.TIOCGWINSZ),
			uintptr(unsafe.Pointer(&ws)))

		if errno == 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
			return int(ws.Xpixel), int(ws.Ypixel)
		}
	}

	return 0, 0
}

// cellSizeReply matches the answer to CSI 16 t: CSI 6 ; height ; width t.
var cellSizeReply = regexp.MustCompile(`\x1b\[6;(\d+);(\d+)t`)

// tmuxCellSize finds the size in pixels of a cell of the terminal attached to
// tmux, or 0, 0 when it cannot be had.
//
// It matters more here than anywhere: through tmux a page is fitted into its
// box of cells keeping its proportions (see clickMap.fitBox), so a wrong cell
// shape leaves the page smaller than its box and every click, selection and
// margin marker beside the point.
//
// tmux knows the size only if the connection carries the window's size in
// pixels. Some do not (Coder's ssh among them), and then tmux gives its panes
// a made-up 16x32 with nothing to tell it from a real answer; hence the
// question to tmux about what its client reported, and not the pane's pixel
// size. When tmux does not know, the terminal is asked past tmux. The answer
// comes back as typing into whichever pane is active, so the question is put
// only when that pane is this one, and only before the key reader starts (see
// ttyQuery); later re-detections reuse the answer.
func (d *DocumentViewer) tmuxCellSize() (float64, float64) {
	out, err := tmuxCommand("display-message", "-p", tmuxCellInfoFormat)
	if err != nil {
		return d.askedCellWidth, d.askedCellHeight
	}
	w, h, mayAsk := parseTmuxCellInfo(out)
	if w > 0 && h > 0 {
		return w, h
	}
	if d.askedCellWidth > 0 && d.askedCellHeight > 0 {
		return d.askedCellWidth, d.askedCellHeight
	}
	if !mayAsk || d.readingKeys {
		return 0, 0
	}
	reply, _ := ttyExchange(tmuxWrap("\x1b[16t"), cellSizeReply)
	if w, h := parseCellSizeReply(reply); w > 0 && h > 0 {
		d.askedCellWidth, d.askedCellHeight = w, h
	}
	return d.askedCellWidth, d.askedCellHeight
}

// tmuxCellInfoFormat asks tmux for the cell size its client reported, and for
// whether this pane is the one the client's typing goes to.
const tmuxCellInfoFormat = "#{client_cell_width},#{client_cell_height},#{pane_active},#{window_active},#{session_attached}"

// parseTmuxCellInfo reads tmux's answer to tmuxCellInfoFormat: the cell size
// tmux knows (0, 0 when it does not, or is too old to say), and whether the
// terminal may be asked — this pane is the active pane of the current window
// of an attached session, so a reply typed into tmux arrives here.
func parseTmuxCellInfo(out string) (w, h float64, mayAsk bool) {
	f := strings.Split(strings.TrimSpace(out), ",")
	if len(f) != 5 {
		return 0, 0, false
	}
	w, _ = strconv.ParseFloat(f[0], 64)
	h, _ = strconv.ParseFloat(f[1], 64)
	if w <= 0 || h <= 0 {
		w, h = 0, 0
	}
	attached, _ := strconv.Atoi(f[4])
	return w, h, f[2] == "1" && f[3] == "1" && attached >= 1
}

// parseCellSizeReply reads a cell size out of the answer to CSI 16 t.
func parseCellSizeReply(reply string) (w, h float64) {
	m := cellSizeReply.FindStringSubmatch(reply)
	if m == nil {
		return 0, 0
	}
	h, _ = strconv.ParseFloat(m[1], 64)
	w, _ = strconv.ParseFloat(m[2], 64)
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	return w, h
}

// getKittyCellSize asks a kitty-protocol terminal for its cell size in pixels.
// Not through tmux, which has its own way (tmuxCellSize). Only asked before
// the key reader starts (see ttyQuery). A later re-detection, after a resize
// or the 'r' key, takes the pane's pixel size, and failing that the answer
// from startup.
func (d *DocumentViewer) getKittyCellSize() (float64, float64) {
	if d.detectTerminalType() != "kitty" || termViaTmux {
		return 0, 0
	}
	if d.readingKeys {
		return 0, 0
	}
	reply, _ := ttyQuery("\x1b[16t")
	if w, h := parseCellSizeReply(reply); w > 0 && h > 0 {
		d.askedCellWidth, d.askedCellHeight = w, h
		return w, h
	}
	return 0, 0
}

// silenceStderr redirects the process's standard error (fd 2) to /dev/null for
// the whole interactive session and returns a duplicate of the original fd plus
// the opened /dev/null handle so restoreStderr can put it back. go-fitz wraps
// MuPDF, whose C code writes PDF parser warnings ("syntax error", "N 0 R",
// "object in xref", ...) straight to fd 2 whenever it reads a damaged or
// partially written file — e.g. a PDF being rewritten by pdflatex. Those bytes
// bypass our synchronized-update frame and scatter across the screen. Nothing in
// this program writes anything meaningful to stderr, so silencing it for the
// session is safe; a panic in the main goroutine still shows its trace because
// restoreStderr runs as a deferred call before the runtime prints it. Returns
// (-1, nil) if redirection could not be set up, in which case restoreStderr is a
// no-op.
func silenceStderr() (int, *os.File) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return -1, nil
	}
	saved, err := syscall.Dup(2)
	if err != nil {
		devNull.Close()
		return -1, nil
	}
	if err := syscall.Dup2(int(devNull.Fd()), 2); err != nil {
		syscall.Close(saved)
		devNull.Close()
		return -1, nil
	}
	return saved, devNull
}

// restoreStderr reverses silenceStderr, reinstating the original fd 2.
func restoreStderr(saved int, devNull *os.File) {
	if saved >= 0 {
		syscall.Dup2(saved, 2)
		syscall.Close(saved)
	}
	if devNull != nil {
		devNull.Close()
	}
}

func (d *DocumentViewer) setRawMode() (*term.State, error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}
	d.oldState = oldState
	return oldState, nil
}

func (d *DocumentViewer) restoreTerminal(old *term.State) {
	if old != nil {
		term.Restore(int(os.Stdin.Fd()), old)
	}
}

// Synthetic key codes for the "uncrop" hotkeys (Cmd+Opt+[ / Cmd+Opt+] and
// their Shift variants), delivered via the Kitty keyboard protocol's CSI u
// encoding — Cmd (Super) has no legacy terminal representation, so these
// combos can only be recognized through that protocol. Values are outside
// any byte a real keypress or paste could plausibly produce.
const (
	keyUncropLeft   byte = 0xE0 // Cmd+Opt+[      — inverse of '['
	keyUncropRight  byte = 0xE1 // Cmd+Opt+]      — inverse of ']'
	keyUncropTop    byte = 0xE2 // Cmd+Opt+Shift+[ — inverse of '{'
	keyUncropBottom byte = 0xE3 // Cmd+Opt+Shift+] — inverse of '}'
)

// keyMouseAltClick is the synthetic key delivered when readSingleChar parses
// an SGR mouse report for an Opt(Alt)+left button press. The clicked cell is
// stored in d.mouseCol/d.mouseRow — a single returned byte can't carry
// coordinates, so the viewer fields are the side channel to handleAltClick.
const keyMouseAltClick byte = 0xE8

// keyMouseCtrlClick is the Ctrl+left press counterpart: follow the hyperlink
// under the click (see handleCtrlClick). Same cell side channel.
const keyMouseCtrlClick byte = 0xE9

// Plain arrows are delivered as synthetic keys rather than translated to
// 'j'/'k' here: modal overlays that accept typed text (the TOC picker) must
// tell an arrow from a typed letter. handleInput maps them onto the same
// paging actions the letters trigger.
const (
	keyArrowPrev byte = 0xEC // Up / Left
	keyArrowNext byte = 0xED // Down / Right
)

// Back/forward through the jump history. Ctrl+I / Ctrl+O are the primary keys:
// as plain control bytes nothing between the keyboard and here can claim them,
// and they sit left-to-right on the keyboard the way back and forward do. That
// is the opposite of vim's jumplist, where Ctrl+O goes back. Cmd+Left /
// Cmd+Right stay as an alias, but macOS and the terminal both bind Cmd+arrow
// (line start/end, tab switching), so the app may never see them. Modified
// arrows keep their CSI 1;mods A-D shape (unlike Cmd+letter combos, which need
// the CSI u encoding); Super contributes bit 8 of mods-1.
const (
	keyHistoryBack    byte = 0xEA // Ctrl+I / Tab, or Cmd+Left
	keyHistoryForward byte = 0xEB // Ctrl+O, or Cmd+Right
)

// parseKittyCSIU parses the parameter bytes of a Kitty keyboard protocol
// "CSI u" sequence (the part between "ESC [" and the final 'u', e.g. "91;11"
// or with the optional shifted-key/event-type suffixes "91:97;11:2") and
// returns the base key code and the raw (1-based) modifier value. Returns
// mods == 0 if no modifier field was present.
func parseKittyCSIU(params []byte) (key int, mods int) {
	fields := strings.Split(string(params), ";")
	if len(fields) == 0 {
		return 0, 0
	}
	key = firstIntField(fields[0])
	if len(fields) > 1 {
		mods = firstIntField(fields[1])
	}
	return key, mods
}

// parseSGRMouse parses the parameter bytes of an SGR mouse report (the part
// between "ESC [ <" and the final 'M'/'m', e.g. "8;42;17") and returns the
// button/modifier code and the 1-based cell column and row.
func parseSGRMouse(params []byte) (btn, col, row int, ok bool) {
	fields := strings.Split(string(params), ";")
	if len(fields) != 3 {
		return 0, 0, 0, false
	}
	btn = firstIntField(fields[0])
	col = firstIntField(fields[1])
	row = firstIntField(fields[2])
	return btn, col, row, col > 0 && row > 0
}

// firstIntField parses the leading decimal digits of s, stopping at the
// first ':' (which separates shifted-key/base-key or modifier/event-type
// sub-fields in the Kitty protocol).
func firstIntField(s string) int {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// stringReplyLead reports whether lead, the byte after "ESC intro", opens a
// string a terminal sends in reply to a query rather than something typed: a
// kitty graphics response (ESC _ G ...) or a device-control report such as the
// terminal's name and version (ESC P > | ...) or a status report (ESC P 1 $ r).
// The lead is what tells them from a person pressing Esc and then '_' or 'P',
// which are commands.
func stringReplyLead(intro, lead byte) bool {
	switch intro {
	case '_':
		return lead == 'G'
	case 'P':
		return lead == '>' || lead == '!' || lead == '0' || lead == '1'
	}
	return false
}

// maxStringReply bounds how much input is discarded as one terminal reply, so
// a reply cut short cannot swallow a session's worth of keys.
const maxStringReply = 4096

// skipStringReply discards the rest of a terminal reply string, up to and
// including its terminator (ESC \ or BEL).
func skipStringReply() {
	b := make([]byte, 1)
	esc := false
	for i := 0; i < maxStringReply; i++ {
		if n, _ := os.Stdin.Read(b); n != 1 {
			return
		}
		if b[0] == 0x07 || (esc && b[0] == '\\') {
			return
		}
		esc = b[0] == 27
	}
}

func (d *DocumentViewer) readSingleChar() byte {
	buf := make([]byte, 1)
	b := make([]byte, 1)
	// Loop: mouse reports that carry no action (plain clicks, wheel, releases)
	// are consumed here and must not surface at all — even a synthetic 0 byte
	// would trigger a redraw per event, and a wheel gesture emits dozens.
	for {
		if d.unreadKey != 0 {
			// The byte read past an Esc that turned out not to open a reply.
			key := d.unreadKey
			d.unreadKey = 0
			return key
		}
		n, _ := os.Stdin.Read(buf)
		if n == 0 {
			return 0
		}

		// Escape sequence handling. Arrow/function keys arrive in several shapes
		// depending on terminal and mode: CSI (ESC [ ... final), SS3 (ESC O final),
		// and with modifier params (ESC [ 1 ; 2 C). agterm/ghostty can use any of
		// these. The key robustness rule: once we see an escape sequence we consume
		// it WHOLE (up to its final byte 0x40-0x7E) and either map it to a known key
		// or swallow it. We must never let leftover bytes ('[', ']', 'C', 'D', ...)
		// flow back as commands, because those are destructive (crop / dark mode).
		if buf[0] != 27 {
			// Bytes >= 0x80 (pasted or IME-typed UTF-8) must not surface as
			// commands: they would forge the 0xE0-0xED synthetic keys.
			if buf[0] >= 0x80 {
				continue
			}
			switch buf[0] {
			case 0x09: // Tab == Ctrl+I
				return keyHistoryBack
			case 0x0F: // Ctrl+O
				return keyHistoryForward
			}
			return buf[0]
		}

		n, _ = os.Stdin.Read(b)
		if n != 1 {
			return 27 // bare ESC
		}
		if b[0] == '_' || b[0] == 'P' {
			// A terminal's reply to a query can reach the input: one that
			// arrived after its query gave up waiting, or one tmux handed back
			// as keys. Its body is text — "Gi=31;OK" — and read as keys that
			// text is a string of commands. Discard the whole reply.
			intro := b[0]
			if n, _ = os.Stdin.Read(b); n != 1 {
				return 27
			}
			if stringReplyLead(intro, b[0]) {
				skipStringReply()
				continue
			}
			// Esc, then a key, then another key: the first two are dropped as
			// before, the third is a keypress in its own right.
			if b[0] < 0x80 && b[0] != 27 {
				d.unreadKey = b[0]
			}
			return 27
		}
		if b[0] != '[' && b[0] != 'O' {
			return 27 // ESC + something else; don't leak it as a crop/dark command
		}

		// Collect parameter/intermediate bytes until the final byte.
		var params []byte
		var final byte
		for i := 0; i < 32; i++ {
			if n, _ = os.Stdin.Read(b); n != 1 {
				break
			}
			if b[0] >= 0x40 && b[0] <= 0x7E { // final byte
				final = b[0]
				break
			}
			params = append(params, b[0])
		}

		// Arrow modifiers arrive as CSI 1 ; mods A-D, where mods-1 is a bit
		// set: 1 Shift, 2 Alt, 4 Ctrl, 8 Super (Cmd).
		shift, super := false, false
		for i := 0; i+1 < len(params); i++ {
			if params[i] == ';' {
				if modBits := firstIntField(string(params[i+1:])) - 1; modBits > 0 {
					shift = modBits&1 != 0
					super = modBits&8 != 0
				}
				break
			}
		}

		switch final {
		case 'A', 'D': // Up / Left -> previous page
			if super && final == 'D' { // Cmd+Left: history back
				return keyHistoryBack
			}
			if shift {
				return 'K'
			}
			return keyArrowPrev
		case 'B', 'C': // Down / Right -> next page
			if super && final == 'C' { // Cmd+Right: history forward
				return keyHistoryForward
			}
			if shift {
				return 'J'
			}
			return keyArrowNext
		case 'u': // Kitty keyboard protocol: CSI key ; modifiers u
			key, rawMods := parseKittyCSIU(params)
			// The disambiguate flag reports Esc as CSI 27 u rather than a bare
			// 0x1b; the TOC picker closes on it, so it must surface as a key.
			if key == 27 {
				return 27
			}
			if rawMods > 0 {
				const altSuper = 2 | 8 // Alt + Super (Cmd) bits, 0-based
				modBits := rawMods - 1
				// Ctrl+I collides with Tab and Ctrl+O with nothing, so the
				// disambiguate flag can report either one here instead of as
				// its control byte. Accept both encodings.
				if modBits&(1|2|4|8) == 4 {
					switch key {
					case 'i':
						return keyHistoryBack
					case 'o':
						return keyHistoryForward
					case 'c': // Ctrl+C, reported as CSI u under the same flag
						return 3
					}
				}
				if modBits&altSuper == altSuper {
					shiftHeld := modBits&1 != 0
					switch key {
					case '[':
						if shiftHeld {
							return keyUncropTop
						}
						return keyUncropLeft
					case ']':
						if shiftHeld {
							return keyUncropBottom
						}
						return keyUncropRight
					}
				}
			}
		case 'M', 'm': // mouse reports (enabled via ?1000h/?1006h in Run)
			if len(params) > 0 && params[0] == '<' {
				// SGR encoding: ESC [ < btn ; col ; row, final M=press m=release.
				// btn bits: low 2 = button (0=left), +4 shift, +8 alt, +16 ctrl,
				// +32 motion, 64/65 wheel. Besides the plain left button
				// (btn == 0, below), only Opt+left (btn == 0|8) and
				// Ctrl+left (btn == 0|16; also 2|16 for terminals that apply the
				// macOS Ctrl+click -> secondary-click translation before
				// reporting) presses become keys; every other mouse event is
				// consumed silently.
				btn, col, row, ok := parseSGRMouse(params[1:])
				if ok && btn == 0 {
					// Plain left button: a press notes where a drag starts,
					// the release ends it (see handleMouseSelect).
					if final == 'M' {
						d.pressAt = cell{col, row}
						continue
					}
					if d.pressAt != (cell{}) {
						d.mouseMu.Lock()
						d.dragFrom, d.dragTo = d.pressAt, cell{col, row}
						d.mouseMu.Unlock()
						d.pressAt = cell{}
						return keyMouseSelect
					}
					continue
				}
				if ok && final == 'M' {
					var key byte
					switch btn {
					case 8:
						key = keyMouseAltClick
					case 16, 18:
						key = keyMouseCtrlClick
					}
					if key != 0 {
						d.mouseMu.Lock()
						d.mouseCol, d.mouseRow = col, row
						d.mouseMu.Unlock()
						return key
					}
				}
				continue
			}
			if final == 'M' && len(params) == 0 {
				// Legacy X10 mouse encoding (terminal ignored ?1006h): three raw
				// payload bytes follow the final 'M'; consume them so they can't
				// leak into the input stream as commands.
				for i := 0; i < 3; i++ {
					if n, _ = os.Stdin.Read(b); n != 1 {
						break
					}
				}
				continue
			}
		}
		// Recognized escape sequence but not an arrow/known combo: swallow it.
		return 0
	}
}
