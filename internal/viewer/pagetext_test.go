package viewer

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

// A line as MuPDF's HTML dump writes it, and the glyphs its SVG dump draws for
// it: no glyph for the space, and one glyph for the "fi" of "fix".
const (
	fixtureHTML = `<div id="page0" style="width:595.3pt;height:841.9pt">
<p style="top:92.0pt;left:50.0pt;line-height:10.0pt"><span style="font-family:X,serif;font-size:10.0pt">The </span><b><span style="font-family:X,serif;font-size:10.0pt">fix &amp; </span></b></p>
</div>`
	fixtureSVG = `<use data-text="T" xlink:href="#font_1_1" transform="matrix(10,0,0,-10,50,100)" fill="#000"/>
<use data-text="h" xlink:href="#font_1_2" transform="matrix(10,0,0,-10,56,100)" fill="#000"/>
<use data-text="e" xlink:href="#font_1_3" transform="matrix(10,0,0,-10,61,100)" fill="#000"/>
<use data-text="f" xlink:href="#font_1_4" transform="matrix(10,0,0,-10,70,100)" fill="#000"/>
<use data-text="x" xlink:href="#font_1_5" transform="matrix(10,0,0,-10,78,100)" fill="#000"/>
<use data-text="&amp;" xlink:href="#font_1_6" transform="matrix(10,0,0,-10,86,100)" fill="#000"/>
<use data-text="R" xlink:href="#font_1_7" transform="matrix(0,10,10,0,300,400)" fill="#000"/>`
)

func TestLinesAreReadFromTheHTMLDump(t *testing.T) {
	lines := parseTextLines(fixtureHTML)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	l := lines[0]
	if string(l.runes) != "The fix &" {
		t.Errorf("text = %q, want the markup stripped, the entity decoded and the trailing space gone", string(l.runes))
	}
	if l.top != 92 || l.left != 50 || l.height != 10 {
		t.Errorf("box = top %v left %v height %v, want 92 50 10", l.top, l.left, l.height)
	}
}

func TestGlyphsAreReadFromTheSVGDump(t *testing.T) {
	glyphs := parseGlyphs(fixtureSVG)
	if len(glyphs) != 6 {
		t.Fatalf("got %d glyphs, want 6: the rotated one is left out", len(glyphs))
	}
	if g := glyphs[0]; g.r != 'T' || g.x != 50 || g.y != 100 || g.size != 10 {
		t.Errorf("first glyph = %+v, want T at (50,100) size 10", g)
	}
	if g := glyphs[5]; g.r != '&' {
		t.Errorf("last glyph = %q, want the entity decoded to &", g.r)
	}
}

func TestCharactersTakeTheirPlaceFromTheGlyphs(t *testing.T) {
	lines := parseTextLines(fixtureHTML)
	placeChars(lines, parseGlyphs(fixtureSVG))
	l := lines[0]
	// T h e _ f i x _ &
	want := []float64{50, 56, 61, 65.5, 70, 74, 78, 82, 86, 91}
	if len(l.x) != len(want) {
		t.Fatalf("got %d positions, want %d", len(l.x), len(want))
	}
	for i, w := range want {
		if !near(l.x[i], w) {
			t.Errorf("x[%d] = %v, want %v (spaces and the i of the ligature sit between their neighbours)", i, l.x[i], w)
		}
	}
	if !near(l.left, 50) || !near(l.right, 91) {
		t.Errorf("line runs %v..%v, want 50..91", l.left, l.right)
	}
	if !near(l.top, 92) || !near(l.height, 11) {
		t.Errorf("line box top %v height %v, want 92 and 11: 0.8 of the size above the baseline, 0.3 below", l.top, l.height)
	}
}

func glyphRun(text string, x, y, size, advance float64) []pageGlyph {
	var glyphs []pageGlyph
	for _, r := range text {
		if r != ' ' {
			glyphs = append(glyphs, pageGlyph{r: r, x: x, y: y, size: size})
		}
		x += advance
	}
	return glyphs
}

func TestALineIsFollowedOffItsFirstBaseline(t *testing.T) {
	// A footnote: a small raised mark, then the note on a lower baseline that
	// the box the HTML dump gives the line (the mark's) does not reach.
	lines := []textLine{{top: 723.1, left: 49.3, height: 6, runes: []rune("*Note here")}}
	glyphs := append([]pageGlyph{{r: '*', x: 49.3, y: 727.9, size: 6}},
		glyphRun("Note here", 53, 730.8, 8, 4)...)
	placeChars(lines, glyphs)
	l := lines[0]
	if !near(l.x[1], 53) || !near(l.x[6], 53+5*4) {
		t.Errorf("N at %v and h at %v, want 53 and 73: the note was not followed", l.x[1], l.x[6])
	}
	if bottom := l.top + l.height; bottom < 730.8 {
		t.Errorf("line box ends at %v, above the note's baseline 730.8", bottom)
	}
}

func TestLinesStartingAtTheSameXAreToldApartByHeight(t *testing.T) {
	lines := []textLine{
		{top: 100, left: 50, height: 10, runes: []rune("abc")},
		{top: 112, left: 50, height: 10, runes: []rune("abd")},
	}
	// Drawn bottom line first, to make order no help.
	glyphs := append(glyphRun("abd", 50, 120, 10, 7), glyphRun("abc", 50, 108, 10, 5)...)
	placeChars(lines, glyphs)
	if !near(lines[0].x[1], 55) || !near(lines[1].x[1], 57) {
		t.Errorf("second characters at %v and %v, want 55 and 57", lines[0].x[1], lines[1].x[1])
	}
}

func TestDrawnSpacesDoNotThrowTheLineOff(t *testing.T) {
	lines := []textLine{{top: 100, left: 50, height: 10, runes: []rune("a     b")}}
	glyphs := []pageGlyph{{'a', 50, 108, 10}}
	for i := 1; i <= 5; i++ {
		glyphs = append(glyphs, pageGlyph{' ', 50 + float64(i)*5, 108, 10})
	}
	glyphs = append(glyphs, pageGlyph{'b', 80, 108, 10})
	placeChars(lines, glyphs)
	if !near(lines[0].x[6], 80) {
		t.Errorf("b at %v, want 80", lines[0].x[6])
	}
}

func TestALineWithoutGlyphsIsLaidOutByEstimate(t *testing.T) {
	// The hidden text layer of a scan draws nothing.
	lines := []textLine{{top: 100, left: 50, height: 10, runes: []rune("scan")}}
	placeChars(lines, nil)
	l := lines[0]
	if len(l.x) != 5 || !near(l.x[0], 50) || !near(l.x[4], 70) {
		t.Errorf("positions %v, want 50 to 70 at half the font size per character", l.x)
	}
	if !near(l.right, 70) {
		t.Errorf("right = %v, want 70", l.right)
	}
}

func placedLine(text string, left, top float64) textLine {
	l := textLine{top: top, left: left, height: 10, runes: []rune(text)}
	estimateLine(&l)
	l.height = 10
	return l
}

func TestAShortLineIsAsWideAsItsColumn(t *testing.T) {
	lines := []textLine{
		placedLine("a full line of the left column", 50, 100),
		placedLine("short.", 50, 112),
		placedLine("a full line of the right column", 320, 100),
		placedLine("another line on the right here", 320, 112),
	}
	groupColumns(lines)
	if got, want := lines[1].colRight, lines[0].right; !near(got, want) {
		t.Errorf("short line's column ends at %v, want %v, the end of the line above it", got, want)
	}
	if lines[1].colRight >= 320 {
		t.Errorf("left column reaches %v, into the right column at 320", lines[1].colRight)
	}
	if !near(lines[2].colLeft, 320) || !near(lines[3].colLeft, 320) {
		t.Errorf("right column starts at %v and %v, want 320", lines[2].colLeft, lines[3].colLeft)
	}
}

func TestALigatureDoesNotBorrowALaterLetter(t *testing.T) {
	// "fit in" with fi drawn as one glyph: the i it lacks must not be taken
	// for the i two glyphs on.
	lines := []textLine{{top: 100, left: 50, height: 10, runes: []rune("fit in")}}
	glyphs := []pageGlyph{{'f', 50, 108, 10}, {'t', 58, 108, 10}, {'i', 66, 108, 10}, {'n', 70, 108, 10}}
	placeChars(lines, glyphs)
	want := []float64{50, 54, 58, 62, 66, 70, 75}
	for i, w := range want {
		if !near(lines[0].x[i], w) {
			t.Errorf("x[%d] = %v, want %v", i, lines[0].x[i], w)
		}
	}
}

func TestAGutterOnOnePrintedLineSeparatesColumns(t *testing.T) {
	// Two columns whose first lines are level and follow one another in the
	// text: the gap between them is a gutter, not a space.
	lines := []textLine{
		placedLine("left column", 50, 100),
		placedLine("right column", 320, 100),
	}
	groupColumns(lines)
	if !near(lines[0].colRight, lines[0].right) || !near(lines[1].colLeft, 320) {
		t.Errorf("columns run to %v and from %v, want %v and 320", lines[0].colRight, lines[1].colLeft, lines[0].right)
	}
}
