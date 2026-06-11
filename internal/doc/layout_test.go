package doc

import "testing"

// fakeRenderer is a minimal BlockRenderer for testing the Layout walk in
// isolation (doc must not import render — SPEC §4 — so the test supplies its own).
// It emits one line per block carrying the block's type as text, marking headings
// so Outline/Anchors assembly can be asserted without the real renderer.
type fakeRenderer struct{}

func (fakeRenderer) RenderBlock(b *Block, width, depth int) []Line {
	ln := Line{
		Segments: []Segment{{Text: string(b.Type)}},
		Indent:   depth * 2,
		BlockID:  b.ID,
	}
	switch b.Type {
	case BlockHeading1, BlockHeading2, BlockHeading3:
		ln.IsHeading = true
		ln.HeadingLevel = b.HeadingLvl
		ln.Segments = []Segment{{Text: plainText(b.RichText)}}
	}
	return []Line{ln}
}

func plainText(rts []RichText) string {
	s := ""
	for _, rt := range rts {
		s += rt.Text
	}
	return s
}

// TestLayoutWalk asserts the Layout tree walk: every block becomes a line, nesting
// increases depth/indent, headings flow into the Outline at the right line index,
// and Anchors map each block id to its line indices.
func TestLayoutWalk(t *testing.T) {
	d := &Document{
		ID:    "page1",
		Title: "Doc",
		Blocks: []Block{
			{ID: "h1", Type: BlockHeading2, HeadingLvl: 2, RichText: []RichText{{Text: "Intro"}}},
			{ID: "p1", Type: BlockParagraph, RichText: []RichText{{Text: "body"}}},
			{
				ID:   "tog",
				Type: BlockToggle,
				Children: []Block{
					{ID: "nested", Type: BlockParagraph},
				},
			},
		},
	}
	r := fakeRenderer{}
	got := Layout(d, 80, r, LayoutOpts{})

	// Content lines: title + h2 + p1 + tog + nested = 5. Plus blank-line spacing:
	// one after the title, and one between each adjacent non-list sibling pair
	// (h2|p1 and p1|tog) = 3 blanks. The toggle→nested parent/child pair is tight.
	// Total 8.
	if len(got.Lines) != 8 {
		t.Fatalf("expected 8 lines (5 content + 3 spacing blanks), got %d", len(got.Lines))
	}
	// nested block is one depth deeper than its toggle parent.
	var nestedIdx = -1
	for i, ln := range got.Lines {
		if ln.BlockID == "nested" {
			nestedIdx = i
		}
	}
	if nestedIdx < 0 {
		t.Fatal("nested block not laid out")
	}
	if got.Lines[nestedIdx].Indent != 2 {
		t.Errorf("nested indent = %d, want 2", got.Lines[nestedIdx].Indent)
	}

	// Outline: the page title (h1) + the h2 heading.
	if len(got.Outline) != 2 {
		t.Fatalf("expected 2 outline entries (title + h2), got %d: %+v", len(got.Outline), got.Outline)
	}
	if got.Outline[1].Text != "Intro" || got.Outline[1].Level != 2 {
		t.Errorf("outline[1] = %+v, want Intro/h2", got.Outline[1])
	}
	if got.Lines[got.Outline[1].LineIdx].BlockID != "h1" {
		t.Errorf("outline line index points at wrong block: %q", got.Lines[got.Outline[1].LineIdx].BlockID)
	}

	// Anchors: each block id maps to its line index.
	if idxs := got.Anchors["p1"]; len(idxs) != 1 {
		t.Errorf("anchor for p1 = %v, want one index", idxs)
	}
}

// TestLayoutNilSafe asserts Layout degrades to an empty Rendered rather than
// panicking on nil/zero inputs (no TTY, no network, never panic — SPEC §4/§5).
func TestLayoutNilSafe(t *testing.T) {
	if got := Layout(nil, 80, fakeRenderer{}, LayoutOpts{}); len(got.Lines) != 0 {
		t.Errorf("nil document must yield no lines, got %d", len(got.Lines))
	}
	if got := Layout(&Document{ID: "x"}, 0, fakeRenderer{}, LayoutOpts{}); len(got.Lines) != 0 {
		t.Errorf("zero width must yield no lines, got %d", len(got.Lines))
	}
	if got := Layout(&Document{ID: "x", Title: "t"}, 80, nil, LayoutOpts{}); len(got.Lines) != 0 {
		t.Errorf("nil renderer must yield no lines, got %d", len(got.Lines))
	}
}

// TestLayoutProps asserts the page-property block is laid out at the top only when
// ShowProps is set, via the synthetic BlockPageProps pseudo-type.
func TestLayoutProps(t *testing.T) {
	d := &Document{
		ID:    "p",
		Props: []Property{{Name: "Status", Kind: "select", Value: []RichText{{Text: "Active"}}}},
		Blocks: []Block{
			{ID: "b", Type: BlockParagraph},
		},
	}
	withProps := Layout(d, 80, fakeRenderer{}, LayoutOpts{ShowProps: true})
	withoutProps := Layout(d, 80, fakeRenderer{}, LayoutOpts{ShowProps: false})

	var sawProps bool
	for _, ln := range withProps.Lines {
		if ln.Text() == string(BlockPageProps) {
			sawProps = true
		}
	}
	if !sawProps {
		t.Error("ShowProps=true did not lay out the property block")
	}
	for _, ln := range withoutProps.Lines {
		if ln.Text() == string(BlockPageProps) {
			t.Error("ShowProps=false laid out a property block")
		}
	}
}
