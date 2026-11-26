package viewer

import (
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

// twoColumnPage is a page of two columns, 10pt lines at 5pt a character:
//
//	y=100  left: "Taking control of the model"   right: "Since we cannot verify"
//	y=112  left: "not work."                     right: "trusting the people"
//	y=124  left: "caught 29%." + "This is one"   (one printed line, split at a gap)
func twoColumnPage() []textLine {
	lines := []textLine{
		placedLine("Taking control of the model", 50, 100),
		placedLine("not work.", 50, 112),
		placedLine("caught 29%.", 50, 124),
		placedLine("This is one", 130, 124),
		placedLine("Since we cannot verify", 320, 100),
		placedLine("trusting the people", 320, 112),
	}
	groupColumns(lines)
	return lines
}

func TestAPointBesideAShortLineMeansThatLine(t *testing.T) {
	lines := twoColumnPage()
	// Level with "not work.", far to its right but still in the left column.
	pos, ok := nearestChar(lines, 170, 117)
	if !ok || pos.line != 1 || pos.char != len("not work.")-1 {
		t.Errorf("got line %d char %d, want the last character of line 1", pos.line, pos.char)
	}
	// In the left margin, level with the first line.
	if pos, _ := nearestChar(lines, 20, 105); pos.line != 0 || pos.char != 0 {
		t.Errorf("got line %d char %d, want the first character of line 0", pos.line, pos.char)
	}
}

func TestAPointInTheOtherColumnMeansThatColumn(t *testing.T) {
	lines := twoColumnPage()
	pos, ok := nearestChar(lines, 331, 117)
	if !ok || pos.line != 5 || pos.char != 2 {
		t.Errorf("got line %d char %d, want line 5 (right column) char 2", pos.line, pos.char)
	}
}

func TestAPointPicksThePieceOfASplitLineItIsOn(t *testing.T) {
	lines := twoColumnPage()
	if pos, _ := nearestChar(lines, 141, 129); pos.line != 3 || pos.char != 2 {
		t.Errorf("got line %d char %d, want line 3 char 2", pos.line, pos.char)
	}
}

func TestNoTextNoCharacter(t *testing.T) {
	if _, ok := nearestChar(nil, 10, 10); ok {
		t.Error("found a character on a page without text")
	}
}

func TestWordsAreFoundFromAnyOfTheirLetters(t *testing.T) {
	r := []rune("the  model runs")
	for _, c := range []struct{ at, start, end int }{
		{0, 0, 2}, {2, 0, 2}, // in "the"
		{6, 5, 9},    // in "model"
		{14, 11, 14}, // last letter
		{3, 5, 2},    // on a space: the word after it starts, the word before it ends
	} {
		if got := wordStart(r, c.at); got != c.start {
			t.Errorf("wordStart(%d) = %d, want %d", c.at, got, c.start)
		}
		if got := wordEnd(r, c.at); got != c.end {
			t.Errorf("wordEnd(%d) = %d, want %d", c.at, got, c.end)
		}
	}
}

func onePage(lines []textLine) (pages []int, get func(int) []textLine) {
	return []int{7}, func(int) []textLine { return lines }
}

func TestADragSelectsWholeWordsInReadingOrder(t *testing.T) {
	pages, get := onePage(twoColumnPage())
	// From inside "control" to inside "work.".
	a := pagePoint{page0: 7, x: 50 + 9*5, y: 105}
	b := pagePoint{page0: 7, x: 50 + 5*5, y: 117}
	want := "control of the model\nnot work."
	text, rects := textBetween(pages, get, a, b)
	if text != want {
		t.Errorf("got %q, want %q", text, want)
	}
	if len(rects) != 2 {
		t.Fatalf("got %d boxes, want one per line", len(rects))
	}
	if r := rects[0]; !near(r.x0, 50+7*5) || !near(r.x1, 50+27*5) || !near(r.y0, 100) || !near(r.y1, 110) || r.page0 != 7 {
		t.Errorf("first box %+v, want x 85..185, y 100..110 on page 7", r)
	}
	if r := rects[1]; !near(r.x0, 50) || !near(r.x1, 50+9*5) {
		t.Errorf("second box runs x %v..%v, want 50..95", r.x0, r.x1)
	}

	back, _ := textBetween(pages, get, b, a)
	if back != want {
		t.Errorf("dragged backwards got %q, want the same %q", back, want)
	}
}

func TestPiecesOfOnePrintedLineAreJoinedByASpace(t *testing.T) {
	pages, get := onePage(twoColumnPage())
	text, _ := textBetween(pages, get, pagePoint{page0: 7, x: 51, y: 117}, pagePoint{page0: 7, x: 157, y: 129})
	if want := "not work.\ncaught 29%. This is"; text != want {
		t.Errorf("got %q, want %q", text, want)
	}
}

func TestADragAcrossColumnsFollowsTheTextNotTheRectangle(t *testing.T) {
	pages, get := onePage(twoColumnPage())
	text, _ := textBetween(pages, get, pagePoint{page0: 7, x: 51, y: 129}, pagePoint{page0: 7, x: 321, y: 105})
	if want := "caught 29%. This is one\nSince"; text != want {
		t.Errorf("got %q, want %q", text, want)
	}
}

func TestADragAcrossASpreadRunsToTheEndOfTheFirstPage(t *testing.T) {
	left := []textLine{placedLine("left page one", 50, 100), placedLine("left page two", 50, 112)}
	right := []textLine{placedLine("right page one", 50, 100), placedLine("right page two", 50, 112)}
	groupColumns(left)
	groupColumns(right)
	get := func(p int) []textLine {
		if p == 3 {
			return left
		}
		return right
	}
	a := pagePoint{page0: 3, x: 50 + 5*5, y: 117} // "page" on the left page's second line
	b := pagePoint{page0: 4, x: 50 + 1*5, y: 105} // "right" on the right page's first line
	want := "page two\nright"
	if text, _ := textBetween([]int{3, 4}, get, a, b); text != want {
		t.Errorf("got %q, want %q", text, want)
	}
	if text, _ := textBetween([]int{3, 4}, get, b, a); text != want {
		t.Errorf("dragged right to left got %q, want %q", text, want)
	}
}

func TestACellOffTheImageCountsAsTheNearestPointOfThePage(t *testing.T) {
	m := singleMap(0, 1, 0, 1) // cols 11..90, rows 2..49, a 612x792pt page
	in, ok := m.nearestPagePoint(cell{11, 2})
	if _, x, y, _ := m.cellToPDF(11, 2); !ok || in.page0 != 4 || !near(in.x, x) || !near(in.y, y) {
		t.Fatalf("a cell on the image maps to %+v, want what cellToPDF gives (%v, %v)", in, x, y)
	}
	for _, c := range []struct {
		at   cell
		x, y float64
	}{
		{cell{1, 1}, 0, 0},        // above and left: the page's corner
		{cell{11, 1}, in.x, 0},    // above: the top edge, same column
		{cell{3, 2}, 0, in.y},     // left: the left edge, same row
		{cell{200, 80}, 612, 792}, // far past the end: the opposite corner
	} {
		got, ok := m.nearestPagePoint(c.at)
		if !ok || !near(got.x, c.x) || !near(got.y, c.y) {
			t.Errorf("cell %v maps to (%v, %v) ok %v, want (%v, %v)", c.at, got.x, got.y, ok, c.x, c.y)
		}
	}
	if _, ok := (&clickMap{}).nearestPagePoint(cell{5, 5}); ok {
		t.Error("a text page has no map, but a cell was mapped")
	}
}

func TestACellInTheGapOfASpreadMeansTheNearerPage(t *testing.T) {
	// Two 100x100pt pages side by side in a 220px-wide image, 20px apart.
	m := clickMap{originCol: 1, originRow: 1, cols: 22, rows: 10, pxW: 220, pxH: 100,
		targets: []clickTarget{
			{page0: 6, x0: 0, x1: 100, y1: 100, pageW: 100, pageH: 100, fx1: 1, fy1: 1},
			{page0: 7, x0: 120, x1: 220, y1: 100, pageW: 100, pageH: 100, fx1: 1, fy1: 1},
		}}
	if _, _, _, ok := m.cellToPDF(11, 5); ok {
		t.Fatal("the gap maps to a page")
	}
	if p, ok := m.nearestPagePoint(cell{11, 5}); !ok || p.page0 != 6 || !near(p.x, 100) {
		t.Errorf("left of the gap's middle maps to %+v, want the right edge of page 6", p)
	}
	if p, ok := m.nearestPagePoint(cell{12, 5}); !ok || p.page0 != 7 || !near(p.x, 0) {
		t.Errorf("right of the gap's middle maps to %+v, want the left edge of page 7", p)
	}
}

func TestSelectionBoxesLandOnTheRightPixels(t *testing.T) {
	// Whole page shown: 612x792pt in 1600x2071px.
	m := singleMap(0, 1, 0, 1)
	box, ok := m.pdfRectToPixels(pageRect{page0: 4, x0: 61.2, y0: 79.2, x1: 306, y1: 158.4})
	if !ok || box != image.Rect(160, 207, 800, 415) {
		t.Errorf("box = %v (ok %v), want (160,207)-(800,415)", box, ok)
	}
	// Bottom half shown: a box in the top half is off screen, one across the
	// split is clipped at the top of the image.
	m = singleMap(0, 1, 0.5, 1)
	if _, ok := m.pdfRectToPixels(pageRect{page0: 4, x0: 10, y0: 100, x1: 200, y1: 200}); ok {
		t.Error("a box on the hidden half was mapped")
	}
	box, ok = m.pdfRectToPixels(pageRect{page0: 4, x0: 0, y0: 300, x1: 612, y1: 594})
	if !ok || box.Min.Y != 0 || box.Max.Y != 1036 {
		t.Errorf("box = %v (ok %v), want rows 0..1036", box, ok)
	}
	if _, ok := m.pdfRectToPixels(pageRect{page0: 9, x1: 10, y1: 10}); ok {
		t.Error("a box on a page not shown was mapped")
	}
}

func TestTintStaysInsideItsBox(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	tintRect(img, image.Rect(1, 1, 3, 9)) // runs off the bottom
	if c := img.RGBAAt(0, 0); c.R != 255 || c.G != 255 || c.B != 255 {
		t.Errorf("pixel outside the box changed: %v", c)
	}
	if c := img.RGBAAt(3, 2); c.R != 255 {
		t.Errorf("pixel right of the box changed: %v", c)
	}
	c := img.RGBAAt(1, 3)
	if !(c.R < c.G && c.G < c.B && c.B == 255 && c.A == 255) {
		t.Errorf("tinted white = %v, want a blue cast, still opaque", c)
	}
}

func readKeys(t *testing.T, d *DocumentViewer, in string, want int) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	if _, err := w.WriteString(in); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Close()
	saved := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = saved }()
	keys := make([]byte, 0, want)
	for i := 0; i < want; i++ {
		keys = append(keys, d.readSingleChar())
	}
	return keys
}

func TestALeftButtonDragIsReportedOnRelease(t *testing.T) {
	d := &DocumentViewer{}
	// Press, a motion report, release — then a key.
	keys := readKeys(t, d, "\x1b[<0;30;6M\x1b[<32;38;7M\x1b[<0;45;8mj", 2)
	if keys[0] != keyMouseSelect || keys[1] != 'j' {
		t.Fatalf("keys = %v, want the select key then j", keys)
	}
	if d.dragFrom != (cell{30, 6}) || d.dragTo != (cell{45, 8}) {
		t.Errorf("drag %v to %v, want {30 6} to {45 8}", d.dragFrom, d.dragTo)
	}
}

func TestOtherMouseReportsSelectNothing(t *testing.T) {
	d := &DocumentViewer{}
	// A release with no press (the press went to another pane), a right-button
	// click, the wheel.
	keys := readKeys(t, d, "\x1b[<0;45;8m\x1b[<2;10;5M\x1b[<2;10;5m\x1b[<64;10;5M\x1b[<65;10;5Mk", 1)
	if keys[0] != 'k' {
		t.Errorf("got key %#x, want only the k", keys[0])
	}
	// Opt+click is still its own key, and does not start a drag.
	keys = readKeys(t, d, "\x1b[<8;10;5M\x1b[<8;10;5mk", 2)
	if keys[0] != keyMouseAltClick || keys[1] != 'k' {
		t.Errorf("keys = %v, want Opt+click then k", keys)
	}
}

// selectingViewer is a viewer showing page 4 whole (singleMap), whose text is
// one line across the top of the page.
func selectingViewer(t *testing.T) *DocumentViewer {
	t.Helper()
	t.Setenv("TMUX", "")
	useTerminal(t, "kitty", false)
	line := placedLine("Taking control of the model", 61.2, 79.2)
	lines := []textLine{line}
	groupColumns(lines)
	return &DocumentViewer{
		clickMap:      singleMap(0, 1, 0, 1),
		textLineCache: map[int][]textLine{4: lines},
	}
}

func TestDraggingCopiesTheTextAndTintsIt(t *testing.T) {
	d := selectingViewer(t)
	// 612pt across 80 columns from column 11: a column is 7.65pt, a row 16.5pt.
	// "Taking" starts at 61.2pt = column 19; the line is on row 6.
	d.dragFrom, d.dragTo = cell{20, 6}, cell{25, 6}
	var redraw bool
	out := captureStdout(t, func() { redraw = d.handleMouseSelect() })
	if want := clipboardSequence("Taking control"); out != want {
		t.Errorf("clipboard got %q, want %q", out, want)
	}
	if !redraw || !d.skipClear {
		t.Errorf("redraw %v skipClear %v, want the page redrawn in place for the tint", redraw, d.skipClear)
	}
	if !d.selection.active || len(d.selection.rects) != 1 {
		t.Fatalf("selection = %+v, want one box", d.selection)
	}
	if !strings.Contains(d.notice, "Copied 14 characters") || !strings.Contains(d.notice, "Taking control") {
		t.Errorf("notice = %q, want the count and the text", d.notice)
	}
	if d.dragFrom != (cell{}) || d.dragTo != (cell{}) {
		t.Error("the drag was not consumed")
	}
}

func TestAClickDropsTheSelection(t *testing.T) {
	d := selectingViewer(t)
	d.dragFrom, d.dragTo = cell{20, 6}, cell{20, 6}
	out := captureStdout(t, func() {
		if d.handleMouseSelect() {
			t.Error("a click with nothing selected asked for a redraw")
		}
	})
	if out != "" {
		t.Errorf("a click wrote %q to the terminal", out)
	}

	d.selection = selection{active: true, rects: []pageRect{{page0: 4, x1: 10, y1: 10}}}
	d.dragFrom, d.dragTo = cell{20, 6}, cell{20, 6}
	if !d.handleMouseSelect() || d.selection.active || !d.skipClear {
		t.Errorf("a click on a selection: active %v skipClear %v, want it dropped and the page redrawn in place",
			d.selection.active, d.skipClear)
	}
}

func TestSelectionIsForgottenWhenItsPageLeavesTheScreen(t *testing.T) {
	d := selectingViewer(t)
	d.selection = selection{active: true, rects: []pageRect{{page0: 4, x1: 10, y1: 10}}}
	d.dropStaleSelection()
	if !d.selection.active {
		t.Fatal("selection dropped while its page is on screen")
	}
	d.clickMap.targets[0].page0 = 5
	d.dropStaleSelection()
	if d.selection.active {
		t.Error("selection kept after its page left the screen")
	}
}

func TestQuoteStartIsOneShortLine(t *testing.T) {
	if got := quoteStart("two\nlines  here", 40); got != "“two lines here”" {
		t.Errorf("got %s", got)
	}
	if got := quoteStart("abcdefghij", 4); got != "“abcd…”" {
		t.Errorf("got %s", got)
	}
}

func TestAPointPastTheEndOfAHeadingMeansTheHeading(t *testing.T) {
	// A heading stands apart from the paragraph under it, so it is no wider
	// than itself; a point just past its end is still nearer to it than to
	// the full line below.
	lines := []textLine{
		placedLine("A heading", 50, 100),
		placedLine("the first full line of the paragraph under it", 50, 130),
	}
	groupColumns(lines)
	if lines[0].colRight != lines[0].right {
		t.Fatalf("heading's column ends at %v, want its own end %v", lines[0].colRight, lines[0].right)
	}
	if pos, _ := nearestChar(lines, lines[0].right+30, 105); pos.line != 0 {
		t.Errorf("got line %d, want the heading", pos.line)
	}
	// Level with the paragraph, it means the paragraph.
	if pos, _ := nearestChar(lines, lines[0].right+30, 128); pos.line != 1 {
		t.Errorf("got line %d, want the paragraph", pos.line)
	}
}

func TestTheSelectionIsTintedIntoThePageImage(t *testing.T) {
	// A white page of 200x100 pixels showing a 200x100pt page whole.
	white := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for i := range white.Pix {
		white.Pix[i] = 255
	}
	d := &DocumentViewer{tempDir: t.TempDir()}
	src, err := d.saveEphemeralPNG("page", white)
	if err != nil {
		t.Fatal(err)
	}
	d.clickMap = clickMap{originCol: 1, originRow: 1, cols: 20, rows: 5, pxW: 200, pxH: 100,
		targets: []clickTarget{{page0: 0, x1: 200, y1: 100, pageW: 200, pageH: 100, fx1: 1, fy1: 1}}}

	if _, ok := d.highlightedPage(src); ok {
		t.Fatal("a page was redrawn with nothing selected")
	}
	d.selection = selection{active: true, rects: []pageRect{{page0: 3, x0: 10, y0: 10, x1: 50, y1: 20}}}
	if _, ok := d.highlightedPage(src); ok {
		t.Fatal("a page was redrawn for a selection on another page")
	}

	d.selection = selection{active: true, rects: []pageRect{{page0: 0, x0: 10, y0: 10, x1: 50, y1: 20}}}
	path, ok := d.highlightedPage(src)
	if !ok || path == src {
		t.Fatalf("highlightedPage = %q, %v; want a new image", path, ok)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds() != white.Bounds() {
		t.Errorf("image is %v, want the page's %v", img.Bounds(), white.Bounds())
	}
	if r, g, b, _ := img.At(30, 15).RGBA(); !(r < g && g < b) {
		t.Errorf("pixel inside the selection is %d,%d,%d, want a blue cast", r>>8, g>>8, b>>8)
	}
	for _, p := range []image.Point{{5, 15}, {55, 15}, {30, 5}, {30, 25}} {
		if r, g, b, _ := img.At(p.X, p.Y).RGBA(); r != 0xffff || g != 0xffff || b != 0xffff {
			t.Errorf("pixel %v outside the selection is not white", p)
		}
	}
}

// tallRowPage is one column of 10pt lines 12pt apart, read through cells 20pt
// tall: every other row of cells holds two lines.
func tallRowPage() []textLine {
	lines := []textLine{
		placedLine("alpha beta gamma delta", 50, 100), // centre 105
		placedLine("epsilon zeta eta theta", 50, 112), // centre 117
		placedLine("iota kappa lambda mu", 50, 124),   // centre 129
		placedLine("nu xi omicron pi rho", 50, 136),   // centre 141
	}
	groupColumns(lines)
	return lines
}

func TestADragAlongARowOfTwoLinesTakesThatStretchOfBoth(t *testing.T) {
	pages, get := onePage(tallRowPage())
	// The row centred on y=111 holds the first two lines. From x=92 ("beta",
	// "zeta") to x=118 ("gamma", "eta").
	a := pagePoint{page0: 7, x: 92, y: 111, halfH: 10}
	b := pagePoint{page0: 7, x: 118, y: 111, halfH: 10}
	want := "beta gamma\nzeta eta"
	text, rects := textBetween(pages, get, a, b)
	if text != want {
		t.Errorf("got %q, want %q", text, want)
	}
	if len(rects) != 2 {
		t.Errorf("got %d boxes, want one on each line", len(rects))
	}
	if back, _ := textBetween(pages, get, b, a); back != want {
		t.Errorf("dragged backwards got %q, want %q", back, want)
	}
}

func TestADragStartsOnTheUpperLineOfItsCellAndEndsOnTheLower(t *testing.T) {
	pages, get := onePage(tallRowPage())
	// From the row holding lines 1 and 2 to the row holding lines 3 and 4.
	a := pagePoint{page0: 7, x: 82, y: 111, halfH: 10}
	b := pagePoint{page0: 7, x: 82, y: 135, halfH: 10}
	want := "beta gamma delta\nepsilon zeta eta theta\niota kappa lambda mu\nnu xi omicron"
	if text, _ := textBetween(pages, get, a, b); text != want {
		t.Errorf("got %q, want %q", text, want)
	}
	if back, _ := textBetween(pages, get, b, a); back != want {
		t.Errorf("dragged backwards got %q, want %q", back, want)
	}
}

func TestACellThatHoldsOneLineSelectsOnlyThatLine(t *testing.T) {
	pages, get := onePage(tallRowPage())
	// Rows 8pt tall: finer than the lines.
	a := pagePoint{page0: 7, x: 92, y: 116, halfH: 4}
	b := pagePoint{page0: 7, x: 118, y: 116, halfH: 4}
	if text, _ := textBetween(pages, get, a, b); text != "zeta eta" {
		t.Errorf("got %q, want %q", text, "zeta eta")
	}
}

func TestACellDoesNotReachIntoTheOtherColumn(t *testing.T) {
	lines := twoColumnPage()
	pages, get := onePage(lines)
	// A tall cell over "not work." in the left column, level with both lines
	// of the right one: only the left column's lines are in question.
	a := pagePoint{page0: 7, x: 51, y: 111, halfH: 10}
	b := pagePoint{page0: 7, x: 90, y: 111, halfH: 10}
	if text, _ := textBetween(pages, get, a, b); text != "Taking control\nnot work." {
		t.Errorf("got %q, want the two left-column lines only", text)
	}
}

func TestACellKnowsHowMuchOfThePageItCovers(t *testing.T) {
	m := singleMap(0, 1, 0, 1) // 792pt over 48 rows: 16.5pt a row
	p, ok := m.nearestPagePoint(cell{20, 10})
	if !ok || !near(p.halfH, 8.25) {
		t.Errorf("halfH = %v (ok %v), want 8.25", p.halfH, ok)
	}
}

func TestALineInTheOtherColumnDoesNotWidenACell(t *testing.T) {
	// The cell over "second line" is level with a line of the right column
	// that sits higher than anything the left column has in that cell. It must
	// not pull the selection up to the left column's line above.
	lines := []textLine{
		placedLine("first line", 50, 90),
		placedLine("second line", 50, 112),
		placedLine("right column", 320, 100),
	}
	groupColumns(lines)
	pages, get := onePage(lines)
	a := pagePoint{page0: 7, x: 51, y: 111, halfH: 10}
	b := pagePoint{page0: 7, x: 100, y: 111, halfH: 10}
	if text, _ := textBetween(pages, get, a, b); text != "second line" {
		t.Errorf("got %q, want %q", text, "second line")
	}
}
