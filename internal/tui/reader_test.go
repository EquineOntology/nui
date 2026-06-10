package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/notion"
)

// keyRunes builds a rune KeyMsg for the given (single- or multi-char) string.
func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// openedReader opens page "p" in the reader with a multi-line document loaded,
// so scroll/jump key tests run against real laid-out lines.
func openedReader(t *testing.T, src *fakeSource) Model {
	t.Helper()
	m := withResults(src, []notion.Result{{ID: "p", Title: "P", Kind: "page"}})
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	blocks := make([]doc.Block, 60)
	for i := range blocks {
		blocks[i] = doc.Block{ID: "b", Type: doc.BlockParagraph, RichText: []doc.RichText{{Text: "paragraph"}}}
	}
	d := &doc.Document{ID: "p", Title: "P", Blocks: blocks}
	mm, _ = m.Update(docResultMsg{gen: m.reader.docGen, id: "p", title: "P", doc: d})
	return mm.(Model)
}

// readerWithLines builds a reader sub-model holding a synthetic Rendered of n
// plain lines (no document), sized to a known viewport, so scroll/clamp tests do
// not depend on the layout pipeline.
func readerWithLines(n, w, h int) readerModel {
	r := newReaderModel()
	r.width = w
	r.height = h
	lines := make([]doc.Line, n)
	for i := range lines {
		lines[i] = doc.Line{Segments: []doc.Segment{{Text: "line"}}}
	}
	r.rendered = &doc.Rendered{Lines: lines}
	return r
}

func TestScrollClampsAtBounds(t *testing.T) {
	r := readerWithLines(100, 80, 12) // bodyHeight = 12 - 1 - 1 = 10
	if r.bodyHeight() != 10 {
		t.Fatalf("bodyHeight = %d, want 10", r.bodyHeight())
	}

	// Scroll up at the top stays at 0.
	r.scroll(-5)
	if r.offset != 0 {
		t.Fatalf("offset after up at top = %d, want 0", r.offset)
	}

	// Scroll down moves the offset.
	r.scroll(5)
	if r.offset != 5 {
		t.Fatalf("offset = %d, want 5", r.offset)
	}

	// Scroll past the end clamps at maxOffset (100 - 10 = 90).
	r.scroll(1000)
	if r.offset != 90 {
		t.Fatalf("offset clamped = %d, want 90", r.offset)
	}
	if r.maxOffset() != 90 {
		t.Fatalf("maxOffset = %d, want 90", r.maxOffset())
	}
}

func TestShortDocumentDoesNotScroll(t *testing.T) {
	r := readerWithLines(3, 80, 20) // fewer lines than the viewport
	if r.maxOffset() != 0 {
		t.Fatalf("maxOffset for short doc = %d, want 0", r.maxOffset())
	}
	r.scroll(50)
	if r.offset != 0 {
		t.Fatalf("short doc should not scroll, offset = %d", r.offset)
	}
	if r.scrollPercent() != "All" {
		t.Fatalf("short doc scrollPercent = %q, want All", r.scrollPercent())
	}
}

func TestStickyHeadingComputation(t *testing.T) {
	// Outline in document order: a level-1 at line 0, a level-2 at line 10, a
	// level-1 at line 30.
	outline := []doc.Heading{
		{Level: 1, Text: "Top", LineIdx: 0},
		{Level: 2, Text: "Middle", LineIdx: 10},
		{Level: 1, Text: "End", LineIdx: 30},
	}

	cases := []struct {
		offset   int
		wantText string
		wantOK   bool
	}{
		// stickyHeading pins a heading only once it has scrolled OFF the top
		// (LineIdx < offset); a heading at exactly the top row is visible in the
		// body and must not be pinned (else it draws twice).
		{offset: 0, wantText: "", wantOK: false},       // Top is the first body line, not pinned
		{offset: 5, wantText: "Top", wantOK: true},     // Top scrolled off
		{offset: 10, wantText: "Top", wantOK: true},    // Middle is the top body line; Top still pinned
		{offset: 11, wantText: "Middle", wantOK: true}, // Middle scrolled off
		{offset: 29, wantText: "Middle", wantOK: true},
		{offset: 30, wantText: "Middle", wantOK: true}, // End is the top body line; Middle still pinned
		{offset: 100, wantText: "End", wantOK: true},
	}
	for _, c := range cases {
		h, ok := stickyHeading(outline, c.offset)
		if ok != c.wantOK {
			t.Fatalf("offset %d: ok = %v, want %v", c.offset, ok, c.wantOK)
		}
		if h.Text != c.wantText {
			t.Fatalf("offset %d: heading = %q, want %q", c.offset, h.Text, c.wantText)
		}
	}
}

func TestStickyHeadingBeforeFirstHeading(t *testing.T) {
	// A heading that begins below the offset means nothing is enclosing yet.
	outline := []doc.Heading{{Level: 1, Text: "Later", LineIdx: 5}}
	if _, ok := stickyHeading(outline, 0); ok {
		t.Fatalf("no heading should enclose offset 0 when the first is at line 5")
	}
	if _, ok := stickyHeading(nil, 0); ok {
		t.Fatalf("empty outline should yield no sticky heading")
	}
}

func TestReaderGtoTopGtoBottom(t *testing.T) {
	src := &fakeSource{}
	m := openedReader(t, src)
	if m.reader.maxOffset() == 0 {
		t.Fatalf("test setup: document should be taller than the viewport")
	}

	// G jumps to bottom, g back to top.
	mm, _ := m.Update(keyRunes("G"))
	got := mm.(Model)
	if got.reader.offset != got.reader.maxOffset() {
		t.Fatalf("G should jump to bottom: offset %d, max %d", got.reader.offset, got.reader.maxOffset())
	}
	mm, _ = got.Update(keyRunes("g"))
	if mm.(Model).reader.offset != 0 {
		t.Fatalf("g should jump to top")
	}
}
