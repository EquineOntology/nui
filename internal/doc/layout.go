package doc

import "strings"

// layout.go defines the layout IR — the structured, ANSI-free line model that the
// reader scrolls and overlays consume (SPEC §6) — and Layout, the pure function
// that walks a Document into it.
//
// These types live in doc (not render) on purpose: they are consumed by tui as
// well as produced by render, and putting them here lets render and tui share
// them without render<->tui coupling. render imports doc; the reverse is
// forbidden (SPEC §4). The per-block rendering logic itself lives in render and
// is injected into Layout as a BlockRenderer, so doc never imports render.

// Rendered is the laid-out document the viewport scrolls. Lines are pre-wrapped
// to the layout width; the viewport only slices them by scroll offset. Outline
// and Anchors are the navigation indices overlays consume (SPEC §6).
type Rendered struct {
	Lines   []Line
	Outline []Heading        // for jump-to-heading, sticky headings, minimap/ToC
	Anchors map[string][]int // blockID -> line indices (comments, highlight, search)
}

// Heading is one entry of the document outline, pointing at the line where the
// heading text was laid out.
type Heading struct {
	Level   int
	Text    string
	LineIdx int // index into Rendered.Lines
}

// Line is one visual row: a sequence of styled Segments plus the metadata the
// viewport and overlays need. Indent is leading columns already applied during
// layout (the painter does not re-indent); BlockID anchors the line back to its
// source Block.
type Line struct {
	Segments     []Segment
	Indent       int    // leading columns (heading nesting / list / column offset)
	BlockID      string // source block — the anchor back into the Document
	IsHeading    bool
	HeadingLevel int // 0 if not a heading
}

// Segment is a run of text sharing one Style. ANSI is never stored here; it is
// produced once, at paint time, from Style (SPEC §6 PAINT RULE).
type Segment struct {
	Text  string
	Style Style
}

// Style is the resolved inline style of a Segment: theme color names (or hex),
// the boolean annotations, and an optional OSC-8 link target. Empty Fg/Bg means
// inherit. This is the render-layer projection of doc.Annotations onto concrete
// colors (done in render/richtext.go against the theme).
type Style struct {
	Fg, Bg                                string // "" = inherit; else theme color name/hex
	Bold, Italic, Underline, Strike, Code bool
	Href                                  string // OSC-8 link target; "" = none
}

// Text returns the concatenated plain text of a Line (no styling). Useful for
// width math, search, and golden serialization.
func (l Line) Text() string {
	var b strings.Builder
	for _, s := range l.Segments {
		b.WriteString(s.Text)
	}
	return b.String()
}

// LayoutOpts carries the knobs that change how a Document is laid out without
// changing the Document itself. Nest mirrors the bash NUI_NEST: indent body
// content under its enclosing heading depth. ShowProps renders the page-property
// block at the top.
type LayoutOpts struct {
	Nest      bool
	ShowProps bool
}

// BlockRenderer turns one Block into its visual lines at the given width and
// nesting depth. render implements this (render.Renderer); Layout calls it so the
// per-block rendering knowledge lives in render while the tree walk and the IR
// stay pure in doc. depth is the block's nesting level (0 at the top); width is
// the columns available after the caller's own indentation.
type BlockRenderer interface {
	RenderBlock(b *Block, width, depth int) []Line
}

// Layout is the load-bearing pure seam (SPEC §6): Document + width -> Rendered.
// It walks the block tree, dispatches each block to the injected BlockRenderer,
// accumulates Lines, and builds the Outline (heading lines) and Anchors (blockID
// -> line indices). It touches no TTY and no network; resize re-runs it cheaply
// against the cached raw JSON.
//
// The renderer is injected (rather than imported) to keep doc pure: render
// depends on doc, so doc cannot depend on render. A nil renderer yields an empty
// Rendered rather than a panic (degrade gracefully).
func Layout(d *Document, width int, r BlockRenderer, opts LayoutOpts) *Rendered {
	out := &Rendered{Anchors: map[string][]int{}}
	if d == nil || r == nil || width <= 0 {
		return out
	}

	// Title line (with page icon) sits above the body, anchored to the page id.
	if d.Title != "" || d.Icon != nil {
		titleBlock := &Block{
			ID:         d.ID,
			Type:       BlockHeading1,
			HeadingLvl: 1,
			Icon:       d.Icon,
			RichText:   plainTitle(d.Title),
		}
		appendBlockLines(out, r.RenderBlock(titleBlock, width, 0))
		out.Lines = append(out.Lines, Line{}) // breathing room under the title
	}

	// Page properties render as a labeled block at the top (SPEC §11 T1).
	if opts.ShowProps && len(d.Props) > 0 {
		propsBlock := propsToBlock(d.ID, d.Props)
		appendBlockLines(out, r.RenderBlock(propsBlock, width, 0))
		out.Lines = append(out.Lines, Line{}) // separate props from the body
	}

	layoutBlocks(out, d.Blocks, width, 0, 0, r, opts)
	return out
}

const (
	// headingIndentStep is the columns of indent added per heading level when Nest
	// is on, and maxHeadingIndent caps the cascade so deep nesting stays readable
	// on a narrow terminal. These mirror the bash _indent_by_heading INDENT/RESERVE.
	headingIndentStep = 2
	maxHeadingIndent  = 8
)

// capIndent clamps a heading-derived indent to [0, maxHeadingIndent].
func capIndent(c int) int {
	switch {
	case c < 0:
		return 0
	case c > maxHeadingIndent:
		return maxHeadingIndent
	default:
		return c
	}
}

// indentedWidth reduces the wrap width by the indent columns, keeping a sane
// minimum so deeply-indented content on a narrow terminal still wraps rather
// than collapsing to nothing.
func indentedWidth(width, cols int) int {
	if w := width - cols; w >= 8 {
		return w
	}
	return 8
}

// shiftIndent moves every line right by cols columns (the heading-section indent
// is applied here, after the renderer has wrapped to the reduced width, so the
// renderer's own structural indent / bullet-depth logic is untouched).
func shiftIndent(lines []Line, cols int) {
	if cols <= 0 {
		return
	}
	for i := range lines {
		lines[i].Indent += cols
	}
}

// isHeading reports whether a block is any heading level.
func isHeading(b *Block) bool {
	switch b.Type {
	case BlockHeading1, BlockHeading2, BlockHeading3,
		BlockHeading4, BlockHeading5, BlockHeading6:
		return true
	}
	return false
}

// layoutBlocks walks a sibling sequence. depth is the structural (block-tree)
// nesting level — it drives the renderer's own indent and bullet-glyph cycling.
// baseCols is the heading-section indent (in columns) inherited from the
// enclosing context. Within this sequence, sectionCols tracks the indent
// contributed by the most recent heading, producing the progressive cascade.
func layoutBlocks(out *Rendered, blocks []Block, width, depth, baseCols int, r BlockRenderer, opts LayoutOpts) {
	ordinal := 0     // running count within a contiguous numbered-list run
	sectionCols := 0 // body indent (cols) from the most recent heading in this run
	for i := range blocks {
		b := &blocks[i]
		// Breathing room between sibling blocks: one blank line, except between
		// consecutive items of the same list (which should stay visually tight).
		if i > 0 {
			for g := 0; g < blockGap(&blocks[i-1], b); g++ {
				out.Lines = append(out.Lines, Line{})
			}
		}
		// Sequential numbering: a numbered item's Ordinal is its position in the
		// current run; any other block type breaks the run and resets the count.
		if b.Type == BlockNumbered {
			ordinal++
			b.Ordinal = ordinal
		} else {
			ordinal = 0
		}

		// Progressive heading-depth indentation (Nest, mirroring the bash
		// _indent_by_heading): a level-N heading sits at (N-1) steps and its body
		// indents to N steps (capped), so the document structure reads as a cascade.
		// The heading line itself is shallower than the body it introduces. The
		// indent is applied by shifting the rendered lines, leaving the renderer's
		// structural depth / bullet logic untouched.
		lvl := 0
		if isHeading(b) {
			if lvl = b.HeadingLvl; lvl == 0 {
				lvl = headingLevel(b.Type)
			}
		}
		extraCols := sectionCols
		if opts.Nest && lvl > 0 {
			extraCols = capIndent((lvl - 1) * headingIndentStep)
		}
		totalCols := baseCols + extraCols

		lines := r.RenderBlock(b, indentedWidth(width, totalCols), depth)
		shiftIndent(lines, totalCols)
		appendBlockLines(out, lines)

		if opts.Nest && lvl > 0 {
			sectionCols = capIndent(lvl * headingIndentStep)
		}

		// Recurse into children. table_row cells are rendered inline by the table
		// renderer, so a table's rows are NOT walked as independent blocks here.
		if b.Type == BlockTable {
			continue
		}
		if len(b.Children) > 0 {
			// Children inherit the current section indent and add a structural level.
			layoutBlocks(out, b.Children, width, depth+1, baseCols+sectionCols, r, opts)
		}
	}
}

// blockGap reports how many blank lines to insert between two adjacent sibling
// blocks. Consecutive items of the same list type stay tight (0); every other
// pair gets one blank line so paragraphs, headings, callouts, and lists are
// visually separated rather than running together.
func blockGap(prev, next *Block) int {
	if prev == nil || next == nil {
		return 0
	}
	if isListItem(prev.Type) && prev.Type == next.Type {
		return 0
	}
	return 1
}

// isListItem reports whether a block is one of the list-item types, which group
// tightly with their same-type neighbours.
func isListItem(t BlockType) bool {
	return t == BlockBulleted || t == BlockNumbered || t == BlockToDo
}

// appendBlockLines appends a renderer's lines to the Rendered output, updating the
// Outline (for heading lines) and Anchors (blockID -> line idx) as it goes. This
// is the single place line indices are assigned, so the navigation indices stay
// consistent with Lines.
func appendBlockLines(out *Rendered, lines []Line) {
	for _, ln := range lines {
		idx := len(out.Lines)
		out.Lines = append(out.Lines, ln)
		if ln.BlockID != "" {
			out.Anchors[ln.BlockID] = append(out.Anchors[ln.BlockID], idx)
		}
		if ln.IsHeading {
			out.Outline = append(out.Outline, Heading{
				Level:   ln.HeadingLevel,
				Text:    ln.Text(),
				LineIdx: idx,
			})
		}
	}
}

// propsToBlock packs the page properties into a synthetic block the renderer can
// lay out as a labeled property table at the top of the document. It carries the
// properties on a dedicated field-free path: each Property becomes one Cell row
// ([name, value]) so the renderer reuses its labeled-rows logic. The block id is
// the page id so the property block anchors to the page.
func propsToBlock(pageID string, props []Property) *Block {
	cells := make([][]RichText, 0, len(props))
	for _, p := range props {
		name := RichText{Text: p.Name, Ann: Annotations{Bold: true, Color: "default"}}
		value := RichText{Text: joinRichText(p.Value), Ann: Annotations{Color: "default"}}
		cells = append(cells, []RichText{name, value})
	}
	return &Block{
		ID:    pageID,
		Type:  BlockPageProps,
		Cells: cells,
	}
}

// BlockPageProps is a pseudo-type used only inside Layout to hand the page
// properties to the renderer as a single labeled block. It is not a Notion block
// type and never appears in a Document built from the API; it exists so the
// property block flows through the same RenderBlock dispatch as everything else.
// It is exported so render can match on it.
const BlockPageProps BlockType = "_page_properties"

// joinRichText concatenates the plain text of a rich-text run with single spaces
// preserved. Used to flatten a property value to one display string.
func joinRichText(rts []RichText) string {
	var b strings.Builder
	for _, rt := range rts {
		b.WriteString(rt.Text)
	}
	return b.String()
}
