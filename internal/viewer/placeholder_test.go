package viewer

import (
	"strings"
	"testing"
)

func TestDiacriticsTableIsKittys(t *testing.T) {
	// Spot values from kitty's gen/rowcolumn-diacritics.txt. U+0653 and U+0654
	// are in Unicode's class-230 list but excluded from kitty's; a table that
	// includes them numbers every row and column from the 63rd on wrongly.
	if got := len(rowColumnDiacritics); got != 297 {
		t.Fatalf("table has %d entries, want 297", got)
	}
	want := map[int]rune{0: 0x0305, 1: 0x030D, 2: 0x030E, 61: 0x0617, 62: 0x0657, 296: 0x1D244}
	for i, r := range want {
		if rowColumnDiacritics[i] != r {
			t.Errorf("entry %d = U+%04X, want U+%04X", i, rowColumnDiacritics[i], r)
		}
	}
	seen := map[rune]bool{}
	for _, r := range rowColumnDiacritics {
		if r == 0x0653 || r == 0x0654 {
			t.Errorf("table contains U+%04X, which kitty excludes", r)
		}
		if seen[r] {
			t.Errorf("table repeats U+%04X", r)
		}
		seen[r] = true
	}
}

func TestPlaceholderIDsFitA256ColourCell(t *testing.T) {
	for _, seed := range []uint32{0, 1, 254, 255, 256, 70000} {
		for n := uint32(0); n < 1000; n++ {
			id := placeholderImageID(seed, n)
			high, low := id>>24, id&0xFF
			if id&0x00FFFF00 != 0 {
				t.Fatalf("id %#x uses bytes a 256-colour cell cannot carry", id)
			}
			if high == 0 || low < 16 {
				t.Fatalf("id %#x: top byte %d must be non-zero, low byte %d must be 16 or more", id, high, low)
			}
		}
	}
	if placeholderImageID(7, 1) == placeholderImageID(7, 2) {
		t.Error("consecutive images share an id")
	}
	if placeholderImageID(7, 1)>>24 == placeholderImageID(8, 1)>>24 {
		t.Error("two processes share a top byte")
	}
}

func TestPlaceholderGridNamesEveryCell(t *testing.T) {
	const id = 0x2A000064 // top byte 42, low byte 100
	got := placeholderGrid(id, 3, 2, 5)

	cell := func(row, col int) string {
		return string([]rune{placeholderRune, rowColumnDiacritics[row], rowColumnDiacritics[col], rowColumnDiacritics[42]})
	}
	want := "\x1b[38;5;100m" + cell(0, 0) + cell(0, 1) + cell(0, 2) + "\x1b[39m" +
		"\r\x1b[1B\x1b[5C" +
		"\x1b[38;5;100m" + cell(1, 0) + cell(1, 1) + cell(1, 2) + "\x1b[39m"
	if got != want {
		t.Errorf("grid =\n%q\nwant\n%q", got, want)
	}
}

func TestPlaceholderGridAtTheLeftEdgeNeedsNoIndent(t *testing.T) {
	got := placeholderGrid(0x01000010, 1, 2, 0)
	if strings.Contains(got, "C") {
		t.Errorf("grid %q moves right with no indent", got)
	}
	if strings.Count(got, string(placeholderRune)) != 2 {
		t.Errorf("grid %q does not have 2 cells", got)
	}
}

func TestPlaceholderGridWiderThanTheTableContinuesBare(t *testing.T) {
	cols := len(rowColumnDiacritics) + 3
	got := []rune(placeholderGrid(0x01000010, cols, 1, 0))
	cells, bare := 0, 0
	for i, r := range got {
		if r != placeholderRune {
			continue
		}
		cells++
		if i+1 == len(got) || got[i+1] == placeholderRune || got[i+1] == '\x1b' {
			bare++
		}
	}
	if cells != cols || bare != 3 {
		t.Errorf("%d cells, %d without marks; want %d and 3", cells, bare, cols)
	}
}

func TestPlaceholderGridOfNothingIsEmpty(t *testing.T) {
	if got := placeholderGrid(0x01000010, 0, 5, 0); got != "" {
		t.Errorf("grid for zero columns = %q, want empty", got)
	}
}

func TestImageBoxIsClippedToTheScreen(t *testing.T) {
	cases := []struct {
		name                  string
		cols, rows            int
		originCol, originRow  int
		termWidth, termHeight int
		wantCols, wantRows    int
	}{
		{"fits", 52, 34, 25, 2, 100, 40, 52, 34},
		{"wider than the screen", 107, 38, 1, 1, 100, 39, 100, 38},
		{"taller than the screen", 60, 50, 1, 1, 100, 39, 60, 39},
		{"overflows from an indent", 90, 10, 21, 2, 100, 40, 80, 10},
		{"origin not recorded", 10, 10, 0, 0, 100, 40, 10, 10},
		{"entirely off screen", 10, 10, 120, 1, 100, 40, 0, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows := visibleCells(tc.cols, tc.rows, tc.originCol, tc.originRow, tc.termWidth, tc.termHeight)
			if cols != tc.wantCols || rows != tc.wantRows {
				t.Errorf("visible = %dx%d, want %dx%d", cols, rows, tc.wantCols, tc.wantRows)
			}
		})
	}
}
