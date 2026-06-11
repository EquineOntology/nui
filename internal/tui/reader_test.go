package tui

import (
	"strings"
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
	// No outline → stickyReserve 0 → bodyHeight = 12 - 0 - 1 = 11.
	r := readerWithLines(100, 80, 12)
	if r.bodyHeight() != 11 {
		t.Fatalf("bodyHeight = %d, want 11", r.bodyHeight())
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

	// Scroll past the end clamps at maxOffset (100 - 11 = 89).
	r.scroll(1000)
	if r.offset != 89 {
		t.Fatalf("offset clamped = %d, want 89", r.offset)
	}
	if r.maxOffset() != 89 {
		t.Fatalf("maxOffset = %d, want 89", r.maxOffset())
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

func chainText(hs []doc.Heading) string {
	parts := make([]string, len(hs))
	for i, h := range hs {
		parts[i] = h.Text
	}
	return strings.Join(parts, ">")
}

func TestStickyChain(t *testing.T) {
	// H1 at 0, H2 at 10, H3 at 20, then a new H1 at 30 (closes the prior chain).
	outline := []doc.Heading{
		{Level: 1, Text: "H1", LineIdx: 0},
		{Level: 2, Text: "H2", LineIdx: 10},
		{Level: 3, Text: "H3", LineIdx: 20},
		{Level: 1, Text: "H1b", LineIdx: 30},
	}
	cases := []struct {
		offset int
		want   string
	}{
		{0, ""},          // top: nothing scrolled off, no chain
		{5, "H1"},        // inside H1
		{10, "H1"},       // H2 is the top body line (visible); only H1 pinned
		{15, "H1>H2"},    // inside H2: additive — H1 AND H2 pinned
		{25, "H1>H2>H3"}, // inside H3: the full ancestor chain
		{35, "H1b"},      // a sibling H1 closes the H1>H2>H3 chain
	}
	for _, c := range cases {
		if got := chainText(stickyChain(outline, c.offset)); got != c.want {
			t.Errorf("offset %d: chain = %q, want %q", c.offset, got, c.want)
		}
	}
}

func TestStickyChainCapAndEmpty(t *testing.T) {
	if got := stickyChain(nil, 10); len(got) != 0 {
		t.Fatalf("empty outline should yield no chain, got %v", got)
	}
	// A chain deeper than maxStickyRows keeps the innermost levels.
	deep := []doc.Heading{
		{Level: 1, Text: "L1", LineIdx: 0},
		{Level: 2, Text: "L2", LineIdx: 1},
		{Level: 3, Text: "L3", LineIdx: 2},
		{Level: 4, Text: "L4", LineIdx: 3},
		{Level: 5, Text: "L5", LineIdx: 4},
	}
	got := stickyChain(deep, 100)
	if len(got) != maxStickyRows {
		t.Fatalf("chain should be capped at %d, got %d", maxStickyRows, len(got))
	}
	if got[len(got)-1].Text != "L5" {
		t.Fatalf("capped chain must keep the innermost heading, got %q", got[len(got)-1].Text)
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
