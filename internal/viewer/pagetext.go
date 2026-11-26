package viewer

import (
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Where the characters of a page are.
//
// A page is on screen as a picture, so the terminal has no text to select;
// selecting with the mouse (see selection.go) needs to know which character
// sits under a point of the page. go-fitz exposes no character boxes, but two
// of its page dumps add up to them:
//
//   - HTML gives MuPDF's structured text: one <p> per line in reading order,
//     with the line's top-left corner and height, and the text as it should be
//     copied (spaces inferred, ligatures spelled out).
//   - SVG gives every glyph drawn, in drawing order, with the point it was
//     drawn at: <use data-text="R" … transform="matrix(a,b,c,d,x,y)"/>.
//
// A structured-text line is a run of consecutive glyphs, so walking a line's
// characters along the glyph list from the glyph drawn at the line's corner
// gives each character its x position.

// textLine is one line of a page's text. Coordinates are PDF points with a
// top-left origin, the space clickMap.cellToPDF maps clicks into. top and
// height box the line's letters, descenders included.
type textLine struct {
	top, height float64
	left, right float64
	runes       []rune
	// x[i] is the left edge of runes[i], and x[len(runes)] the right edge of
	// the line, so runes[i] spans x[i]..x[i+1].
	x []float64
	// colLeft..colRight is how far the column of text the line belongs to
	// reaches (see groupColumns): a point beside a short line is still in it.
	colLeft, colRight float64
}

// pageGlyph is one glyph of the SVG dump: the character it stands for, the
// point of the baseline it was drawn at, and its font size.
type pageGlyph struct {
	r          rune
	x, y, size float64
}

// htmlTextLineRe matches one line of MuPDF's HTML page dump, capturing top,
// left, line height and the marked-up text.
var htmlTextLineRe = regexp.MustCompile(
	`(?s)<p style="top:([0-9.-]+)pt;left:([0-9.-]+)pt;line-height:([0-9.-]+)pt">(.*?)</p>`)

// parseTextLines reads the lines of a page from MuPDF's HTML dump, in reading
// order. Their characters are not placed yet (see placeChars).
func parseTextLines(pageHTML string) []textLine {
	var lines []textLine
	for _, m := range htmlTextLineRe.FindAllStringSubmatch(pageHTML, -1) {
		top, err1 := strconv.ParseFloat(m[1], 64)
		left, err2 := strconv.ParseFloat(m[2], 64)
		height, err3 := strconv.ParseFloat(m[3], 64)
		if err1 != nil || err2 != nil || err3 != nil || height <= 0 {
			continue
		}
		text := html.UnescapeString(htmlTagRe.ReplaceAllString(m[4], ""))
		text = strings.TrimRightFunc(text, unicode.IsSpace)
		if strings.TrimSpace(text) == "" {
			continue
		}
		lines = append(lines, textLine{top: top, height: height, left: left, runes: []rune(text)})
	}
	return lines
}

// svgGlyphRe matches one glyph of MuPDF's SVG page dump.
var svgGlyphRe = regexp.MustCompile(`<use data-text="([^"]*)"[^>]*transform="matrix\(([^)]*)\)"`)

// parseGlyphs reads the glyphs of a page from MuPDF's SVG dump, in drawing
// order. Glyphs on a slanted baseline are left out: lines are matched to
// glyphs by height on the page, which means nothing for rotated text.
func parseGlyphs(pageSVG string) []pageGlyph {
	var glyphs []pageGlyph
	for _, m := range svgGlyphRe.FindAllStringSubmatch(pageSVG, -1) {
		text := []rune(html.UnescapeString(m[1]))
		fields := strings.Split(m[2], ",")
		if len(text) == 0 || len(fields) != 6 {
			continue
		}
		var v [6]float64
		ok := true
		for i, f := range fields {
			n, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
			if err != nil {
				ok = false
				break
			}
			v[i] = n
		}
		if !ok || math.Abs(v[1]) > 0.02*math.Abs(v[0]) {
			continue
		}
		glyphs = append(glyphs, pageGlyph{r: text[0], x: v[4], y: v[5], size: math.Abs(v[0])})
	}
	return glyphs
}

// estimatedAdvance is a character's width as a fraction of the font size when
// nothing better is known: about right for the average letter of a text face.
const estimatedAdvance = 0.5

// placeChars gives every character of every line its x position from the
// glyphs drawn for it. A line whose glyphs cannot be found is laid out by
// estimate instead, so it can still be selected, only less exactly.
func placeChars(lines []textLine, glyphs []pageGlyph) {
	next := 0 // lines follow one another in the glyph list; start looking there
	for i := range lines {
		l := &lines[i]
		start := findLineStart(l, glyphs, next)
		if start < 0 || !placeLine(l, glyphs, start, &next) {
			estimateLine(l)
		}
	}
	groupColumns(lines)
}

// groupColumns gives each line the width of the column it is set in: the
// reach of the run of lines around it that follow one another down the page
// and overlap sideways. The last line of a paragraph stops short, and a point
// to the right of it belongs to that line, not to the full line under it; a
// point in the other column of a two-column page does not.
func groupColumns(lines []textLine) {
	for start := 0; start < len(lines); {
		left, right := lines[start].left, lines[start].right
		end := start + 1
		for ; end < len(lines); end++ {
			prev, l := &lines[end-1], &lines[end]
			h := math.Min(prev.height, l.height)
			if math.Abs(l.top-prev.top) < 0.5*h {
				// The same printed line, split at a gap: one column if the
				// gap is a few letters, two if it is a gutter.
				if gap := l.left - prev.right; gap < -1 || gap > 4*h {
					break
				}
			} else {
				gap := l.top - (prev.top + prev.height)
				if gap < -0.5*h || gap > 1.2*h || l.right < left || l.left > right {
					break
				}
			}
			left, right = math.Min(left, l.left), math.Max(right, l.right)
		}
		for i := start; i < end; i++ {
			lines[i].colLeft, lines[i].colRight = left, right
		}
		start = end
	}
}

// onLine reports whether a glyph's baseline lies inside the box the HTML dump
// gives the line. That box is the height of the line's first character, from
// the top of its tall letters to just under its baseline, so the baselines of
// the line above and of the line below fall outside it even when lines are set
// solid. It is how a line's first glyph is told from another glyph drawn at
// the same x; the rest of the line is followed by order instead, because a
// line that opens with a footnote mark or runs through a formula has
// characters on other baselines.
func onLine(l *textLine, g pageGlyph) bool {
	return g.y >= l.top+0.3*l.height && g.y <= l.top+1.15*l.height
}

// findLineStart finds the glyph a line begins with: drawn at the line's left
// edge, on its baseline, and showing its first character. The search starts
// at from and wraps, because the next line usually follows the last one.
func findLineStart(l *textLine, glyphs []pageGlyph, from int) int {
	first := firstNonSpace(l.runes)
	if first < 0 {
		return -1
	}
	tolerance := math.Max(1.5, 0.15*l.height)
	loose := -1
	for n := 0; n < len(glyphs); n++ {
		i := (from + n) % len(glyphs)
		g := glyphs[i]
		if !onLine(l, g) || math.Abs(g.x-l.left) > tolerance {
			continue
		}
		if g.r == l.runes[first] {
			return i
		}
		if loose < 0 {
			loose = i
		}
	}
	return loose
}

func firstNonSpace(runes []rune) int {
	for i, r := range runes {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return -1
}

// placeLine walks the line's characters along the glyph list from start.
// Characters with no glyph of their own — spaces, which are mostly gaps rather
// than glyphs, and the later letters of a ligature — are spread between their
// placed neighbours. The line's box is then redrawn around the glyphs found.
// It reports false when too few characters found a glyph for the positions to
// be trusted; *next is advanced past the glyphs used.
func placeLine(l *textLine, glyphs []pageGlyph, start int, next *int) bool {
	const lookahead = 3 // glyphs the text does not mention: accents, rules
	x := make([]float64, len(l.runes)+1)
	known := make([]bool, len(l.runes))
	g, placed, wanted := start, 0, 0
	lastSize := l.height
	top, bottom := math.Inf(1), math.Inf(-1)
	for i, r := range l.runes {
		if unicode.IsSpace(r) {
			continue
		}
		wanted++
		for g < len(glyphs) && unicode.IsSpace(glyphs[g].r) {
			g++ // a space that was drawn; the text's spaces are not looked up
		}
		if g < len(glyphs) && glyphs[g].r != r {
			// The glyph at hand is the following character's: this one was
			// drawn as part of the previous glyph (the "i" of an "fi").
			if n := firstNonSpace(l.runes[i+1:]); n >= 0 && l.runes[i+1+n] == glyphs[g].r {
				continue
			}
		}
		for skip := 0; skip <= lookahead && g+skip < len(glyphs); skip++ {
			c := glyphs[g+skip]
			if c.r != r {
				continue
			}
			x[i], known[i] = c.x, true
			lastSize = c.size
			// Letters reach about 0.8 of the font size above the baseline
			// (the figure MuPDF's own line box uses) and 0.3 below it.
			top = math.Min(top, c.y-0.8*c.size)
			bottom = math.Max(bottom, c.y+0.3*c.size)
			g += skip + 1
			placed++
			break
		}
	}
	if placed == 0 || placed*2 < wanted {
		return false
	}
	*next = g

	// Spread the characters in between, and run off both ends by estimate.
	prev := -1
	for i := range l.runes {
		if !known[i] {
			continue
		}
		if prev < 0 {
			for k := 0; k < i; k++ {
				x[k] = x[i] - float64(i-k)*estimatedAdvance*lastSize
			}
		} else {
			for k := prev + 1; k < i; k++ {
				x[k] = x[prev] + (x[i]-x[prev])*float64(k-prev)/float64(i-prev)
			}
		}
		prev = i
	}
	for k := prev + 1; k <= len(l.runes); k++ {
		x[k] = x[prev] + float64(k-prev)*estimatedAdvance*lastSize
	}
	// A line never runs backwards; out-of-order glyphs would make a character
	// impossible to hit.
	for k := 1; k < len(x); k++ {
		x[k] = math.Max(x[k], x[k-1])
	}
	l.x = x
	l.left, l.right = x[0], x[len(l.runes)]
	l.top, l.height = top, bottom-top
	return true
}

// estimateLine lays a line out from its left edge at an average width per
// character.
func estimateLine(l *textLine) {
	l.x = make([]float64, len(l.runes)+1)
	for k := range l.x {
		l.x[k] = l.left + float64(k)*estimatedAdvance*l.height
	}
	l.right = l.x[len(l.runes)]
	l.height *= 1.1 // the dump's box stops short of the descenders
}

// pageTextLines returns the positioned text of a page, cached per page: the
// two dumps run the page through MuPDF twice. Cleared on reload by
// findContentPages. Main goroutine only.
func (d *DocumentViewer) pageTextLines(page0 int) []textLine {
	if lines, ok := d.textLineCache[page0]; ok {
		return lines
	}
	if d.doc == nil {
		return nil
	}
	pageHTML, err := d.doc.HTML(page0, false)
	if err != nil {
		return nil
	}
	lines := parseTextLines(pageHTML)
	var glyphs []pageGlyph
	if len(lines) > 0 {
		if pageSVG, err := d.doc.SVG(page0); err == nil {
			glyphs = parseGlyphs(pageSVG)
		}
	}
	placeChars(lines, glyphs)
	if d.textLineCache == nil {
		d.textLineCache = make(map[int][]textLine)
	}
	d.textLineCache[page0] = lines
	return lines
}
