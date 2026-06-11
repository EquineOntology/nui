package render_test

import (
	"strings"
	"testing"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/render"
	"github.com/mattn/go-runewidth"
)

// newRenderer builds a Renderer for the block-level unit tests.
func newRenderer() *render.Renderer {
	return render.NewRenderer(render.DefaultTheme(), doc.LayoutOpts{})
}

// lineWidth returns the visible display width of a laid-out line (indent + all
// segment text). Used to assert wrapping respects the width budget.
func lineWidth(ln doc.Line) int {
	w := ln.Indent
	for _, s := range ln.Segments {
		w += runewidth.StringWidth(s.Text)
	}
	return w
}

// TestParagraphWrapsToWidth asserts a long paragraph wraps so no line exceeds the
// width, and that the lines reconstruct the original words.
func TestParagraphWrapsToWidth(t *testing.T) {
	r := newRenderer()
	const width = 20
	b := &doc.Block{
		ID:       "p1",
		Type:     doc.BlockParagraph,
		RichText: []doc.RichText{{Text: "the quick brown fox jumps over the lazy dog repeatedly"}},
	}
	lines := r.RenderBlock(b, width, 0)
	if len(lines) < 2 {
		t.Fatalf("expected wrapping into multiple lines, got %d", len(lines))
	}
	for i, ln := range lines {
		if lineWidth(ln) > width {
			t.Errorf("line %d width %d exceeds %d: %q", i, lineWidth(ln), width, ln.Text())
		}
		if ln.BlockID != "p1" {
			t.Errorf("line %d lost block id: %q", i, ln.BlockID)
		}
	}
}

// TestHardSplitLongToken asserts a single token wider than the line is split
// rather than overflowing.
func TestHardSplitLongToken(t *testing.T) {
	r := newRenderer()
	const width = 10
	b := &doc.Block{
		ID:       "p",
		Type:     doc.BlockParagraph,
		RichText: []doc.RichText{{Text: "supercalifragilisticexpialidocious"}},
	}
	for i, ln := range r.RenderBlock(b, width, 0) {
		if lineWidth(ln) > width {
			t.Errorf("hard-split line %d width %d exceeds %d", i, lineWidth(ln), width)
		}
	}
}

// TestHeadingMarksOutline asserts a heading sets IsHeading/HeadingLevel so the
// layout walk flows it into the Outline.
func TestHeadingMarksOutline(t *testing.T) {
	r := newRenderer()
	b := &doc.Block{ID: "h", Type: doc.BlockHeading2, HeadingLvl: 2, RichText: []doc.RichText{{Text: "Section"}}}
	lines := r.RenderBlock(b, 40, 0)
	if len(lines) == 0 || !lines[0].IsHeading || lines[0].HeadingLevel != 2 {
		t.Fatalf("heading not marked: %+v", lines)
	}
}

// TestTodoCheckboxGlyph asserts checked/unchecked to_do items render the right
// glyph and that a checked item strikes its body but not its checkbox.
func TestTodoCheckboxGlyph(t *testing.T) {
	r := newRenderer()
	unchecked := r.RenderBlock(&doc.Block{ID: "t", Type: doc.BlockToDo, RichText: []doc.RichText{{Text: "do it"}}}, 40, 0)
	if !strings.HasPrefix(unchecked[0].Text(), "[ ] ") {
		t.Errorf("unchecked glyph wrong: %q", unchecked[0].Text())
	}
	checked := r.RenderBlock(&doc.Block{ID: "t", Type: doc.BlockToDo, Checked: true, RichText: []doc.RichText{{Text: "done"}}}, 40, 0)
	if !strings.HasPrefix(checked[0].Text(), "[x] ") {
		t.Errorf("checked glyph wrong: %q", checked[0].Text())
	}
	// the body segment is struck, the checkbox segment is not.
	if checked[0].Segments[0].Style.Strike {
		t.Error("checkbox glyph must not be struck")
	}
	struck := false
	for _, s := range checked[0].Segments[1:] {
		if s.Style.Strike {
			struck = true
		}
	}
	if !struck {
		t.Error("checked to_do body must be struck through")
	}
}

// TestCalloutBox asserts a callout renders a top border, a body line carrying the
// icon, and a bottom border, all tinted with the callout background.
func TestCalloutBox(t *testing.T) {
	r := newRenderer()
	b := &doc.Block{
		ID:       "c",
		Type:     doc.BlockCallout,
		Color:    "blue_background",
		Icon:     &doc.Icon{Emoji: "🔔"},
		RichText: []doc.RichText{{Text: "heads up"}},
	}
	lines := r.RenderBlock(b, 40, 0)
	if len(lines) < 3 {
		t.Fatalf("callout needs >=3 lines (top/body/bottom), got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0].Text(), "╭") {
		t.Errorf("missing top border: %q", lines[0].Text())
	}
	if !strings.HasPrefix(lines[len(lines)-1].Text(), "╰") {
		t.Errorf("missing bottom border: %q", lines[len(lines)-1].Text())
	}
	if !strings.Contains(lines[1].Text(), "🔔") {
		t.Errorf("icon missing from first body line: %q", lines[1].Text())
	}
	// every border segment carries the background tint.
	if lines[0].Segments[0].Style.Bg == "" {
		t.Error("callout border lost background tint")
	}
}

// TestCodeBlockFramed asserts a code block is framed, the language labels the top
// border, and chroma highlighting produced styled segments (not one flat run) —
// while never emitting ANSI in the structured output.
func TestCodeBlockFramed(t *testing.T) {
	r := newRenderer()
	b := &doc.Block{
		ID:       "code",
		Type:     doc.BlockCode,
		Language: "go",
		RichText: []doc.RichText{{Text: "func main() {\n\treturn\n}"}},
	}
	lines := r.RenderBlock(b, 50, 0)
	if len(lines) < 3 {
		t.Fatalf("code block too short: %d lines", len(lines))
	}
	if !strings.Contains(lines[0].Text(), "go") {
		t.Errorf("language label missing from top border: %q", lines[0].Text())
	}
	// some segment must carry a non-empty style (keyword/string highlight).
	styled := false
	for _, ln := range lines {
		for _, s := range ln.Segments {
			if s.Style.Fg != "" || s.Style.Bold {
				styled = true
			}
			if strings.Contains(s.Text, "\x1b") {
				t.Fatalf("code segment contains raw ANSI (paint-rule violation): %q", s.Text)
			}
		}
	}
	if !styled {
		t.Error("chroma highlighting produced no styled segments")
	}
}

// TestUnsupportedDegrades asserts an unsupported block renders a single muted
// placeholder rather than vanishing or panicking.
func TestUnsupportedDegrades(t *testing.T) {
	r := newRenderer()
	lines := r.RenderBlock(&doc.Block{ID: "u", Type: doc.BlockUnsupported}, 40, 0)
	if len(lines) != 1 || !strings.Contains(lines[0].Text(), "unsupported") {
		t.Errorf("unsupported placeholder wrong: %+v", lines)
	}
}

// TestRenderBlockNilSafe asserts the renderer never panics on degenerate input.
func TestRenderBlockNilSafe(t *testing.T) {
	r := newRenderer()
	if got := r.RenderBlock(nil, 40, 0); got != nil {
		t.Errorf("nil block must yield nil, got %v", got)
	}
	if got := r.RenderBlock(&doc.Block{Type: doc.BlockParagraph}, 0, 0); got != nil {
		t.Errorf("zero width must yield nil, got %v", got)
	}
}

// TestHeading4Renders guards the fix for headings deeper than the documented
// heading_3: real pages carry heading_4 (they were degrading to "unsupported").
// It must render as a level-4 heading (bold, dim), not the unsupported placeholder.
func TestHeading4Renders(t *testing.T) {
	r := newRenderer()
	b := &doc.Block{ID: "h", Type: doc.BlockHeading4, HeadingLvl: 4, RichText: []doc.RichText{{Text: "Deep heading"}}}
	lines := r.RenderBlock(b, 72, 0)
	if len(lines) == 0 {
		t.Fatal("heading_4 produced no lines")
	}
	if !lines[0].IsHeading || lines[0].HeadingLevel != 4 {
		t.Fatalf("heading_4 not marked as a level-4 heading: %+v", lines[0])
	}
	text := lines[0].Text()
	if !strings.Contains(text, "Deep heading") {
		t.Fatalf("heading_4 text missing, got %q", text)
	}
	if strings.Contains(strings.ToLower(text), "unsupported") {
		t.Fatalf("heading_4 degraded to unsupported: %q", text)
	}
}

// TestNumberedListSequences guards sequential numbering: a contiguous run of
// numbered items must render 1., 2., 3. (Layout assigns Ordinal from sibling
// order), and a non-numbered block must reset the run.
func TestNumberedListSequences(t *testing.T) {
	r := render.NewRenderer(render.DefaultTheme(), doc.LayoutOpts{})
	d := &doc.Document{
		ID: "p",
		Blocks: []doc.Block{
			{ID: "n1", Type: doc.BlockNumbered, RichText: []doc.RichText{{Text: "first"}}},
			{ID: "n2", Type: doc.BlockNumbered, RichText: []doc.RichText{{Text: "second"}}},
			{ID: "n3", Type: doc.BlockNumbered, RichText: []doc.RichText{{Text: "third"}}},
			{ID: "p1", Type: doc.BlockParagraph, RichText: []doc.RichText{{Text: "break"}}},
			{ID: "n4", Type: doc.BlockNumbered, RichText: []doc.RichText{{Text: "reset"}}},
		},
	}
	got := doc.Layout(d, 72, r, doc.LayoutOpts{})
	var markers []string
	for _, ln := range got.Lines {
		t := ln.Text()
		for _, want := range []string{"first", "second", "third", "reset"} {
			if strings.Contains(t, want) {
				markers = append(markers, strings.TrimSpace(strings.SplitN(t, " ", 2)[0]))
			}
		}
	}
	want := []string{"1.", "2.", "3.", "1."} // run of three, then reset after the paragraph
	if strings.Join(markers, ",") != strings.Join(want, ",") {
		t.Fatalf("numbered markers = %v, want %v", markers, want)
	}
}

// TestHeadingMarkerByLevel guards the markdown-style level markers: "# " for H1,
// "## " for H2, …, and that a heading renders as a single line (no underline rule).
func TestHeadingMarkerByLevel(t *testing.T) {
	r := newRenderer()
	cases := []struct {
		typ    doc.BlockType
		lvl    int
		marker string
	}{
		{doc.BlockHeading1, 1, "# "},
		{doc.BlockHeading2, 2, "## "},
		{doc.BlockHeading3, 3, "### "},
		{doc.BlockHeading4, 4, "#### "},
	}
	for _, c := range cases {
		b := &doc.Block{ID: "h", Type: c.typ, HeadingLvl: c.lvl, RichText: []doc.RichText{{Text: "Title"}}}
		lines := r.RenderBlock(b, 60, 0)
		if len(lines) != 1 {
			t.Fatalf("level %d: expected a single heading line (no rule), got %d", c.lvl, len(lines))
		}
		if !lines[0].IsHeading {
			t.Errorf("level %d: line not marked as a heading", c.lvl)
		}
		if got := lines[0].Text(); !strings.HasPrefix(got, c.marker) {
			t.Errorf("level %d: heading = %q, want prefix %q", c.lvl, got, c.marker)
		}
	}
}
