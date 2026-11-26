package viewer

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Selecting text with the mouse.
//
// The page is a picture, so neither the terminal nor tmux can select its text,
// and while the viewer listens for clicks they do not see the mouse at all.
// The viewer does it instead: a drag with the left button selects the text
// between the two ends in reading order, as a drag does in any PDF viewer,
// copies it to the clipboard (clipboard.go) and tints it on the page.
//
// The mouse is reported per cell, and a cell covers more than one character
// and often more than one line. So the ends snap to whole words, a cell that
// holds two lines selects both (see rangesOnPage), and the notice names the
// first words copied.

// keyMouseSelect is the synthetic key delivered when the left button is
// released; the cells of the press and the release are in d.dragFrom and
// d.dragTo. A click (both on one cell) is delivered too: it drops the
// selection.
const keyMouseSelect byte = 0xEE

// cell is a 1-based terminal cell.
type cell struct{ col, row int }

// pagePoint is a point of a page in PDF points, top-left origin: the middle of
// the cell the mouse was reported in. halfH is half the height of that cell on
// the page, so the cell covers y-halfH..y+halfH; 0 means the point is exact.
type pagePoint struct {
	page0 int
	x, y  float64
	halfH float64
}

// pageRect is a rectangle of a page in PDF points, top-left origin.
type pageRect struct {
	page0          int
	x0, y0, x1, y1 float64
}

// selection is the text last selected: the boxes to tint on the pages showing
// it. It lasts until its pages leave the screen, a click or a reload.
type selection struct {
	active bool
	rects  []pageRect
}

// textPos names a character of a page: a line and a character of it.
type textPos struct{ line, char int }

func (p textPos) before(q textPos) bool {
	return p.line < q.line || (p.line == q.line && p.char < q.char)
}

// nearestChar finds the character a point means: the nearest line, then the
// character of it under the point's x, or the line's first or last character
// for a point beside it. ok is false for a page without text.
//
// Nearest is by height first. A line counts as wide as its column, and beyond
// that sideways distance counts a quarter of vertical distance: someone
// pointing past the end of a heading means the heading, not the paragraph
// under it, while a point well inside the other column means that column.
func nearestChar(lines []textLine, x, y float64) (pos textPos, ok bool) {
	best := math.Inf(1)
	for i := range lines {
		l := &lines[i]
		if len(l.runes) == 0 {
			continue
		}
		dx := math.Max(0, math.Max(l.colLeft-x, x-l.colRight)) / 4
		dy := math.Max(0, math.Max(l.top-y, y-(l.top+l.height)))
		// Among lines the point is equally far from (two pieces of one
		// printed line), the one it is beside.
		own := math.Max(0, math.Max(l.left-x, x-l.right)) / 1000
		if dist := dx*dx + dy*dy + own; dist < best {
			best, pos.line, ok = dist, i, true
		}
	}
	if !ok {
		return pos, false
	}
	l := &lines[pos.line]
	pos.char = len(l.runes) - 1
	for i := range l.runes {
		if x < l.x[i+1] {
			pos.char = i
			break
		}
	}
	return pos, true
}

// wordStart is the index of the first character of the word at i; on a space,
// of the word after it.
func wordStart(runes []rune, i int) int {
	for i < len(runes)-1 && unicode.IsSpace(runes[i]) {
		i++
	}
	for i > 0 && !unicode.IsSpace(runes[i-1]) {
		i--
	}
	return i
}

// wordEnd is the index of the last character of the word at i; on a space, of
// the word before it.
func wordEnd(runes []rune, i int) int {
	for i > 0 && unicode.IsSpace(runes[i]) {
		i--
	}
	for i < len(runes)-1 && !unicode.IsSpace(runes[i+1]) {
		i++
	}
	return i
}

// selectRange returns the text from character a to character b of a page,
// both included and in reading order, and the box of each line's share of it.
// Pieces of one printed line — MuPDF splits a line at a wide gap — are joined
// by a space, lines by a newline.
func selectRange(page0 int, lines []textLine, a, b textPos) (string, []pageRect) {
	var text strings.Builder
	var rects []pageRect
	for i := a.line; i <= b.line && i < len(lines); i++ {
		l := &lines[i]
		from, to := 0, len(l.runes)-1
		if i == a.line {
			from = a.char
		}
		if i == b.line {
			to = b.char
		}
		if from > to || to >= len(l.runes) {
			continue
		}
		if text.Len() > 0 {
			if prev := &lines[i-1]; math.Abs(prev.top-l.top) < 0.5*l.height && l.left >= prev.right-1 {
				text.WriteByte(' ')
			} else {
				text.WriteByte('\n')
			}
		}
		text.WriteString(strings.TrimRightFunc(string(l.runes[from:to+1]), unicode.IsSpace))
		rects = append(rects, pageRect{page0: page0,
			x0: l.x[from], x1: l.x[to+1],
			y0: l.top, y1: l.top + l.height})
	}
	return text.String(), rects
}

// textRange is a run of characters of a page in reading order, both ends
// included.
type textRange struct{ from, to textPos }

// cellLines finds the characters a cell could mean. A terminal row is often
// taller than a line of print, so the row the mouse is in can hold two lines
// of a column, and nothing says which was meant: first is the character under
// the cell on the upper one, last on the lower one. They are the same
// character when the row holds one line.
func cellLines(lines []textLine, p pagePoint) (first, last textPos, ok bool) {
	n, ok := nearestChar(lines, p.x, p.y)
	if !ok {
		return n, n, false
	}
	first, last = n, n
	centre := func(l *textLine) float64 { return l.top + l.height/2 }
	nl := &lines[n.line]
	if p.halfH <= 0 || math.Abs(centre(nl)-p.y) > p.halfH {
		return first, last, true
	}
	top, bottom := centre(nl), centre(nl)
	for i := range lines {
		l := &lines[i]
		if l.colLeft != nl.colLeft || l.colRight != nl.colRight || math.Abs(centre(l)-p.y) > p.halfH {
			continue
		}
		top, bottom = math.Min(top, centre(l)), math.Max(bottom, centre(l))
	}
	// Pieces of one printed line have centres a hair apart; another line is at
	// least a fair part of a line away.
	if top < centre(nl)-0.25*nl.height {
		first, _ = nearestChar(lines, p.x, top)
	}
	if bottom > centre(nl)+0.25*nl.height {
		last, _ = nearestChar(lines, p.x, bottom)
	}
	return first, last, true
}

// rangesOnPage returns what a drag selects on one page. a is where it starts
// on this page, nil when it came from the page before; b where it ends, nil
// when it goes on to the next.
//
// Where a cell holds two lines the selection takes both, so it always
// includes the text under the pointer: it starts on the upper line of its
// first cell and ends on the lower line of its last. A drag along a single
// row, the way one line is selected, takes the stretch between its ends on
// each of that row's lines instead of running from one into the other.
func rangesOnPage(ls []textLine, a, b *pagePoint) []textRange {
	last := len(ls) - 1
	r := textRange{textPos{0, 0}, textPos{last, len(ls[last].runes) - 1}}
	var aFirst, aLast, bFirst, bLast textPos
	var okA, okB bool
	if a != nil {
		aFirst, aLast, okA = cellLines(ls, *a)
	}
	if b != nil {
		bFirst, bLast, okB = cellLines(ls, *b)
	}
	if okA && okB {
		if bFirst.before(aFirst) || (bFirst == aFirst && bLast.before(aLast)) { // dragged backwards
			aFirst, aLast, bFirst, bLast = bFirst, bLast, aFirst, aLast
		}
		if a.y == b.y && aFirst.line != aLast.line && bFirst.line != bLast.line {
			return []textRange{ordered(aFirst, bFirst), ordered(aLast, bLast)}
		}
	}
	if okA {
		r.from = aFirst
	}
	if okB {
		r.to = bLast
	}
	return []textRange{ordered(r.from, r.to)}
}

func ordered(p, q textPos) textRange {
	if q.before(p) {
		p, q = q, p
	}
	return textRange{p, q}
}

// textBetween returns the text between two points of the pages on screen, and
// the boxes covering it. pages lists the pages in the order they read; lines
// gives a page's text. The ends snap outwards to whole words.
func textBetween(pages []int, lines func(page0 int) []textLine, a, b pagePoint) (string, []pageRect) {
	ia, ib := indexOf(pages, a.page0), indexOf(pages, b.page0)
	if ia < 0 || ib < 0 {
		return "", nil
	}
	if ia > ib {
		a, b, ia, ib = b, a, ib, ia
	}
	var texts []string
	var rects []pageRect
	for i := ia; i <= ib; i++ {
		ls := lines(pages[i])
		if len(ls) == 0 {
			continue
		}
		var from, to *pagePoint
		if i == ia {
			from = &a
		}
		if i == ib {
			to = &b
		}
		for _, r := range rangesOnPage(ls, from, to) {
			r.from.char = wordStart(ls[r.from.line].runes, r.from.char)
			r.to.char = wordEnd(ls[r.to.line].runes, r.to.char)
			if t, boxes := selectRange(pages[i], ls, r.from, r.to); t != "" {
				texts = append(texts, t)
				rects = append(rects, boxes...)
			}
		}
	}
	return strings.Join(texts, "\n"), rects
}

func indexOf(s []int, v int) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// pages lists the pages the displayed image shows, in the order they read.
func (m *clickMap) pages() []int {
	var pages []int
	for _, t := range m.targets {
		if indexOf(pages, t.page0) < 0 {
			pages = append(pages, t.page0)
		}
	}
	return pages
}

// nearestPagePoint maps a cell to the point of a page it shows, like
// cellToPDF, but a cell off the page counts as the closest point on it: a drag
// that starts in the margin, runs past the end of a line and off the image, or
// ends in the gap between the two pages of a spread, means the text beside it.
func (m *clickMap) nearestPagePoint(c cell) (pagePoint, bool) {
	if m.cols <= 0 || m.rows <= 0 || m.pxW <= 0 || m.pxH <= 0 {
		return pagePoint{}, false
	}
	ix, iy := m.boxToImage(
		(float64(c.col-m.originCol)+0.5)/float64(m.cols),
		(float64(c.row-m.originRow)+0.5)/float64(m.rows))
	px, py := ix*m.pxW, iy*m.pxH
	var nearest *clickTarget
	best := math.Inf(1)
	for i := range m.targets {
		t := &m.targets[i]
		if t.x1 <= t.x0 || t.y1 <= t.y0 {
			continue
		}
		dx := math.Max(0, math.Max(t.x0-px, px-t.x1))
		dy := math.Max(0, math.Max(t.y0-py, py-t.y1))
		if dist := dx*dx + dy*dy; dist < best {
			best, nearest = dist, t
		}
	}
	if nearest == nil {
		return pagePoint{}, false
	}
	x, y := nearest.pagePoint(
		math.Min(math.Max(px, nearest.x0), nearest.x1),
		math.Min(math.Max(py, nearest.y0), nearest.y1))
	p := pagePoint{page0: nearest.page0, x: x, y: y}
	if _, h, ok := m.cellSizePDF(nearest.page0); ok {
		p.halfH = h / 2
	}
	return p, true
}

// pdfRectToPixels maps a rectangle of a page to the pixels of the displayed
// image that show it, clipped to the part of the page on screen. ok is false
// when none of it is.
func (m *clickMap) pdfRectToPixels(r pageRect) (image.Rectangle, bool) {
	for _, t := range m.targets {
		if t.page0 != r.page0 || t.pageW <= 0 || t.pageH <= 0 || t.fx1 <= t.fx0 || t.fy1 <= t.fy0 {
			continue
		}
		px := func(x float64) float64 {
			g := math.Min(math.Max(x/t.pageW, t.fx0), t.fx1)
			return t.x0 + (g-t.fx0)/(t.fx1-t.fx0)*(t.x1-t.x0)
		}
		py := func(y float64) float64 {
			g := math.Min(math.Max(y/t.pageH, t.fy0), t.fy1)
			return t.y0 + (g-t.fy0)/(t.fy1-t.fy0)*(t.y1-t.y0)
		}
		box := image.Rect(int(math.Floor(px(r.x0))), int(math.Floor(py(r.y0))),
			int(math.Ceil(px(r.x1))), int(math.Ceil(py(r.y1))))
		if box.Empty() {
			return image.Rectangle{}, false
		}
		return box, true
	}
	return image.Rectangle{}, false
}

// handleMouseSelect acts on the drag readSingleChar last reported: it selects
// the text between the two ends and copies it. A click drops the selection.
// It reports whether the page has to be drawn again; a redraw it asks for goes
// over the page already on screen, since only the tint differs.
func (d *DocumentViewer) handleMouseSelect() (redraw bool) {
	d.mouseMu.Lock()
	from, to := d.dragFrom, d.dragTo
	d.dragFrom, d.dragTo = cell{}, cell{}
	d.mouseMu.Unlock()
	if from == (cell{}) || to == (cell{}) {
		return false
	}
	redraw = d.selection.active
	d.selection = selection{}
	d.skipClear = redraw
	if from == to {
		return redraw
	}
	a, okA := d.clickMap.nearestPagePoint(from)
	b, okB := d.clickMap.nearestPagePoint(to)
	if !okA || !okB {
		return redraw
	}
	text, rects := textBetween(d.clickMap.pages(), d.pageTextLines, a, b)
	if text == "" {
		d.notice = "No text to select here"
		return redraw
	}
	d.selection = selection{active: true, rects: rects}
	d.skipClear = true
	d.copyText(text, "")
	return true
}

// copyPageText copies the whole text of the pages on screen ('y').
func (d *DocumentViewer) copyPageText() {
	if d.doc == nil || len(d.textPages) == 0 {
		return
	}
	pages := d.clickMap.pages()
	if len(pages) == 0 { // a page shown as text has no map
		pages = []int{d.textPages[d.currentPage]}
	}
	var texts []string
	for _, p := range pages {
		if t, err := d.doc.Text(p); err == nil && strings.TrimSpace(t) != "" {
			texts = append(texts, strings.TrimRightFunc(t, unicode.IsSpace))
		}
	}
	if len(texts) == 0 {
		d.notice = "No text on this page"
		return
	}
	what := "page"
	if len(pages) > 1 {
		what = "both pages"
	}
	d.copyText(strings.Join(texts, "\n\n"), what)
}

// copyText puts text on the clipboard and says so in the status line. what
// names it ("page"); without a name the notice quotes how the text begins, so
// a selection made a cell at a time can be checked at a glance.
func (d *DocumentViewer) copyText(text, what string) {
	copyToClipboard(text)
	n := utf8.RuneCountInString(text)
	unit := "characters"
	if n == 1 {
		unit = "character"
	}
	if what != "" {
		d.notice = fmt.Sprintf("Copied %s (%d %s)", what, n, unit)
		return
	}
	d.notice = fmt.Sprintf("Copied %d %s: %s", n, unit, quoteStart(text, 40))
}

// quoteStart quotes the first words of text on one line, at most limit
// characters of it.
func quoteStart(text string, limit int) string {
	r := []rune(strings.Join(strings.Fields(text), " "))
	if len(r) > limit {
		return "“" + string(r[:limit]) + "…”"
	}
	return "“" + string(r) + "”"
}

// showNotice shows the pending notice without drawing the page again.
func (d *DocumentViewer) showNotice() {
	termWidth, _ := d.getTerminalSize()
	d.drawNotice(termWidth)
	fmt.Print("\033[9999;1H")
	os.Stdout.Sync()
}

// clearNotice wipes a notice off the status row before the page is drawn
// again: the status bar is centred text and would not cover all of it.
func (d *DocumentViewer) clearNotice(termHeight int) {
	if !d.noticeShown {
		return
	}
	d.noticeShown = false
	fmt.Printf("\033[%d;1H\033[2K", termHeight)
}

// drawNotice shows the pending notice in place of the status bar, once: the
// next redraw puts the status bar back.
func (d *DocumentViewer) drawNotice(termWidth int) {
	if d.notice == "" {
		return
	}
	msg := d.notice
	d.notice = ""
	d.noticeShown = true
	_, termHeight := d.getTerminalSize()
	if termWidth <= 2 || termHeight <= 0 {
		return
	}
	if r := []rune(msg); len(r) > termWidth-2 {
		msg = string(r[:termWidth-3]) + "…"
	}
	pad := (termWidth - utf8.RuneCountInString(msg) - 2) / 2
	fmt.Printf("\033[%d;1H\033[2K\033[%dG\033[7m %s \033[0m", termHeight, pad+1, msg)
}

// dropStaleSelection forgets the selection once none of its pages is on
// screen, so it is not there waiting when the page is next opened.
func (d *DocumentViewer) dropStaleSelection() {
	if !d.selection.active {
		return
	}
	for _, r := range d.selection.rects {
		if d.clickMap.hasPage(r.page0) {
			return
		}
	}
	d.selection = selection{}
}

// selectionTint is the colour laid over selected text, and how much of it:
// enough to show on a white page and on a dark one without hiding the words.
var selectionTint = [3]uint32{40, 120, 255}

const selectionTintAlpha = 90 // of 255

// highlightedPage returns a copy of the page image about to be displayed with
// the selection tinted in, or ok false when no selected text is on it. The
// image is redrawn rather than overlaid because through tmux a cell can name
// only one image (see drawFlashRule).
func (d *DocumentViewer) highlightedPage(imagePath string) (string, bool) {
	if !d.selection.active || d.clickMap.pxW <= 0 || d.clickMap.pxH <= 0 {
		return "", false
	}
	var boxes []image.Rectangle
	for _, r := range d.selection.rects {
		if box, ok := d.clickMap.pdfRectToPixels(r); ok {
			boxes = append(boxes, box)
		}
	}
	if len(boxes) == 0 {
		return "", false
	}
	f, err := os.Open(imagePath)
	if err != nil {
		return "", false
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		return "", false
	}
	img := toRGBACopy(src)
	// The map counts in the pixels the image was rendered at; the file should
	// agree, but scale rather than trust it.
	sx := float64(img.Bounds().Dx()) / d.clickMap.pxW
	sy := float64(img.Bounds().Dy()) / d.clickMap.pxH
	for _, box := range boxes {
		tintRect(img, image.Rect(
			int(float64(box.Min.X)*sx), int(float64(box.Min.Y)*sy),
			int(math.Ceil(float64(box.Max.X)*sx)), int(math.Ceil(float64(box.Max.Y)*sy))))
	}
	path, err := d.saveEphemeralPNG("sel", img)
	if err != nil {
		return "", false
	}
	return path, true
}

// tintRect blends selectionTint over a rectangle of img.
func tintRect(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(img.Bounds())
	const a = selectionTintAlpha
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
		for i := 0; i+3 < len(row); i += 4 {
			for c := 0; c < 3; c++ {
				row[i+c] = uint8((uint32(row[i+c])*(255-a) + selectionTint[c]*a) / 255)
			}
		}
	}
}
