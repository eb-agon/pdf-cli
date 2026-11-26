<h1 align=center>pdf-cli</h1>
<div align=center>

![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=for-the-badge&logo=go&labelColor=181825)
![Stars](https://img.shields.io/github/stars/Yujonpradhananga/pdf-cli?style=for-the-badge&labelColor=181825&color=f9e2af)
![Latest Release](https://img.shields.io/github/v/release/Yujonpradhananga/pdf-cli?style=for-the-badge&labelColor=181825&color=b4befe)
![Last Commit](https://img.shields.io/github/last-commit/Yujonpradhananga/pdf-cli?style=for-the-badge&labelColor=181825&color=a6e3a1)

</div>
A terminal-based PDF, EPUB, DOCX, HTML, PNG, and JPEG viewer with fuzzy file search, high-resolution rendering, fast page caching, LaTeX auto-reload, intelligent text reflow, and multi-page viewing modes.

<https://github.com/user-attachments/assets/7ba77b3f-7f7a-48aa-bf70-1f432650bdf1>

## Features

- **Fuzzy File Search**: Interactive file picker with fuzzy search to quickly find your PDFs and EPUBs
- **Smart Content Detection**: Automatically detects and displays text, images, or mixed content pages
- **High-Resolution Image Rendering**: Uses terminal graphics protocols (Sixel/Kitty/iTerm2) for crisp image display
- **Render Cache and Prefetch**: Caches rendered pages and prepares neighboring pages in the background
- **Half Page View**: Supports screen splitting to display half pages at high quality
- **Image Invert**: Inverts the Image while preserving the core colors of the image.
- **HiDPI/Retina Support**: Dynamic cell size detection for sharp rendering on high-DPI displays
- **Auto-Reload**: Automatically reloads when the PDF changes (perfect for LaTeX compilation with `latexmk -pvc`)
- **Fit Modes**: Toggle between height-fit, width-fit, and auto-fit modes
- **Manual Zoom**: Adjust zoom from 10% to 200%
- **In-Document Search**: Search for text within documents
- **Copy Text**: Drag over a page to copy its text to the system clipboard, including over ssh and inside tmux
- **Navigation Tools**: Follow document links, browse jump history, and fuzzy-search the table of contents
- **Viewer Control**: Inspect and control running viewers with `dvctl` or `pdf-cli ctl`
- **Intelligent Text Reflow**: Automatically reformats text to fit your terminal width while preserving paragraphs
- **Terminal-Aware**: Detects your terminal type and optimizes rendering accordingly
- **Multiple Formats**: Supports PDF, EPUB, DOCX, HTML, PNG, and JPEG files

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `j` / `Space` / `Down` / `Right` | Next page |
| `k` / `Up` / `Left` | Previous page |
| `g` | Go to specific page |
| `T` | Table of contents overlay (fuzzy search, Enter jumps) |
| `Ctrl+Click` | Follow links, references, citations, and URLs |
| Drag | Select text and copy it to the clipboard (click to clear) |
| `y` | Copy the text of the page on screen |
| `Cmd+Left` / `Cmd+Right` | Move backward or forward through jump history |
| `b` | Back to file picker |
| `/` | Search in document |
| `n` | Next search result |
| `N` | Previous search result |
| `t` | Toggle text/image/auto mode |
| `f` | Cycle fit modes (height/width/auto) |
| `D` | Cycle page tint (white/gray/dark/invert) |
| `+` / `=` | Zoom in |
| `-` | Zoom out |
| `r` | Refresh display (re-detect cell size) |
| `d` | Show debug info |
| `h` | Show help |
| `q` | Quit |
| `2` | Cycle page modes |

## Installation

### NixOSNixOS Installation

in your configuration.nix file add pdf-cli

```nix
environment.systemPackages = with pkgs; [
  pdf-cli
];
```

### Arch Linux Installation

Using yay

```bash
yay -S pdf-cli
```

Using paru

```
paru -S pdf-cli
```

### Building from source

```bash
# Clone this repository
git clone https://github.com/Yujonpradhananga/pdf-cli

# Install dependencies
go mod tidy

# Build
go build -o pdf-cli .

# Optionally move to your PATH
mv pdf-cli ~/local/bin/

# Or build and install pdf-cli plus the dvctl symlink
make install
```

### Usage

```bash
# Search current directory (default)
pdf-cli

# Search specific directory
pdf-cli ~/Documents/papers/

# Open a specific file directly
pdf-cli paper.pdf

# Inspect a running viewer
pdf-cli ctl status
```

## LaTeX Workflow

The auto-reload feature makes this viewer ideal for LaTeX editing:

1. Open your PDF: `pdf-cli paper.pdf`
2. Run LaTeX compiler in another terminal: `latexmk -pvc paper.tex`
3. The viewer automatically reloads when the PDF updates, preserving your page position

The viewer handles partially-written PDFs gracefully, waiting for the file to stabilize before reloading.

## Dependencies

- Go 1.21+
- [go-fitz](https://github.com/gen2brain/go-fitz) - PDF/EPUB parsing (MuPDF)
- [go-termimg](https://github.com/blacktop/go-termimg) - Terminal image rendering
- [fuzzy](https://github.com/sahilm/fuzzy) - Fuzzy search
- [golang.org/x/term](https://golang.org/x/term) - Terminal control

## Supported Terminals

Optimized for terminals with graphics support:

- **Kitty** (recommended) - Native cell size detection via escape sequences
- **Ghostty** - Kitty graphics protocol, including over ssh and inside tmux
- Foot
- WezTerm
- iTerm2
- Alacritty
- xterm (with Sixel support)

Works in any terminal, but image rendering quality depends on terminal capabilities.

### Ghostty and Kitty over ssh

Over ssh these terminals look like a plain xterm (`TERM` is rewritten and
`TERM_PROGRAM` is not forwarded), so the viewer asks the terminal itself whether
it speaks the kitty graphics protocol. Pages are then sent as PNG data through
the connection, since the terminal cannot read files on the remote machine.

### Inside tmux

When the terminal attached to tmux is Ghostty or Kitty, pages are drawn at full
resolution through tmux:

- The viewer asks tmux which terminal is attached, turns `allow-passthrough` on
  for its own pane while it runs, and puts the pane's setting back on exit.
  Nothing needs to be added to `tmux.conf`. Tested with tmux 3.4.
- Pages are shown with the kitty protocol's unicode placeholders, so they stay
  inside their pane and follow it through splits, zoom, window switches and
  resizes.
- The viewer draws on the alternate screen and leaves the pane's scrollback as
  it found it.
- If you attach from a different terminal while a page is on screen, press `r`
  (or any key that redraws) to send the page to the new terminal.
- Clicks and selections are matched to the page using the size of a terminal
  cell in pixels. tmux only knows it when the connection carries pixel sizes;
  some do not (Coder's ssh, for one), so at startup the viewer asks the
  terminal itself, through tmux. That needs the viewer's pane to be the active
  one when it starts. The debug screen (`d`) says when the size is a guess; if
  clicks land beside the pointer, restart the viewer in the active pane, or set
  `DOCVIEWER_CELL_SIZE`. Restart it too after changing the terminal's font size.
- The thin rule that marks a forward-sync jump is not drawn inside tmux; the
  margin arrows are.

With any other terminal attached, tmux gets the generic renderer as before.

### Selecting and copying text

A page is drawn as a picture, so the terminal has no text of its own to select
there. In Ghostty and Kitty the viewer does the selecting:

- Drag with the left button across the page. The text between the two ends is
  selected in reading order (down the column, then on to the next), tinted on
  the page, and copied. Click anywhere to clear it.
- `y` copies all the text of the page on screen (both pages of a spread).
- The copy goes to the clipboard of the machine your terminal runs on, through
  the terminal (OSC 52), so it works over ssh. Inside tmux it also becomes
  tmux's newest paste buffer (`prefix ]`).
- The mouse is reported per character cell, not per pixel. A cell is wider
  than a letter, so a selection starts and ends on whole words. A cell is often
  taller than a line of print too, and a row of cells can then hold two lines:
  the selection takes both, so that it always includes the one you pointed at.
  The status line shows how the copied text begins. For a finer aim make the
  page larger: `+`, `f` for fit-width, or the half-page view (`2`).
- In a scan with a hidden text layer the letters' positions are estimated, and
  a drag can be a few words off; `y` is exact.
- In text view (`t`) the page is ordinary terminal text and the viewer lets go
  of the mouse: select it the way you select anything else in your terminal or
  in tmux.
- Hold Shift while dragging for your terminal's own selection at any time.

### Environment variables

| Variable | Effect |
|----------|--------|
| `DOCVIEWER_GRAPHICS` | `kitty` forces the kitty graphics protocol (through tmux when inside it); `termimg` forces the generic renderer. Default: detect. |
| `DOCVIEWER_KITTY_XFER` | `file` or `direct`: how pages reach a kitty-protocol terminal. Default: probe; always `direct` inside tmux. |
| `DOCVIEWER_CELL_SIZE` | Cell size in pixels as `WxH` (e.g. `16x32`), when detection gets it wrong. |
| `DOCVIEWER_SUPERSAMPLE` | How many times larger than its on-screen size a page is rasterized (1 to 8, default 1.6). Lower it on a slow link. |

## How It Works

The reader scans the current directory (or specified directory) for supported documents and images. Use the fuzzy search to quickly filter and select a file. The viewer intelligently detects whether document pages contain text, images, or both, and renders them appropriately for terminal display.

PDFs are rendered as images by default (essential for math, diagrams, and formatted content) at a DPI calculated to match your terminal's pixel dimensions for optimal sharpness.

## License

MIT

---

By [Yujon Pradhananga](https://github.com/Yujonpradhananga)
