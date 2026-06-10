package render

import (
	"strings"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/mattn/go-runewidth"
)

// Renderer is the per-block renderer injected into doc.Layout (it implements
// doc.BlockRenderer). It holds the theme + options and turns one doc.Block into
// its visual []doc.Line at a given width and nesting depth. Every method emits
// structured Lines/Segments only — no ANSI, no lipgloss .Render() (the paint
// rule, SPEC §6).
type Renderer struct {
	Theme *Theme
	Nest  bool // mirror doc.LayoutOpts.Nest: indent body under heading depth
}

// NewRenderer builds a Renderer with the default theme when none is supplied.
func NewRenderer(theme *Theme, opts doc.LayoutOpts) *Renderer {
	if theme == nil {
		theme = DefaultTheme()
	}
	return &Renderer{Theme: theme, Nest: opts.Nest}
}

// indentStep is the column width of one nesting level for list/child indentation.
const indentStep = 2

// RenderBlock dispatches a block to its type-specific renderer. depth is the
// block's nesting level (0 at top); width is the columns available. Unknown /
// unsupported types degrade to a muted placeholder rather than vanishing or
// panicking (SPEC §5 graceful degradation).
func (r *Renderer) RenderBlock(b *doc.Block, width, depth int) []doc.Line {
	if b == nil || width <= 0 {
		return nil
	}
	indent := depth * indentStep

	switch b.Type {
	case doc.BlockParagraph:
		return r.renderText(b, width, indent, nil)
	case doc.BlockHeading1, doc.BlockHeading2, doc.BlockHeading3:
		return r.renderHeading(b, width, indent)
	case doc.BlockBulleted:
		return r.renderListItem(b, width, indent, bulletMarker(depth))
	case doc.BlockNumbered:
		return r.renderListItem(b, width, indent, "1.")
	case doc.BlockToDo:
		return r.renderTodo(b, width, indent)
	case doc.BlockToggle:
		return r.renderToggle(b, width, indent)
	case doc.BlockQuote:
		return r.renderQuote(b, width, indent)
	case doc.BlockDivider:
		return r.renderDivider(b, width, indent)
	case doc.BlockCallout:
		return r.renderCallout(b, width, indent)
	case doc.BlockCode:
		return r.renderCode(b, width, indent)
	case doc.BlockTable:
		return r.renderTable(b, width, indent)
	case doc.BlockPageProps:
		return r.renderProps(b, width, indent)
	case doc.BlockChildPage, doc.BlockChildDatabase, doc.BlockLinkToPage:
		return r.renderChildRef(b, width, indent)
	case doc.BlockBookmark, doc.BlockLinkPreview:
		return r.renderBookmark(b, width, indent)
	case doc.BlockToC:
		// table_of_contents has no inline content; the live ToC overlay is G3.
		return nil
	case doc.BlockColumnList, doc.BlockColumn, doc.BlockSyncedBlock:
		// structural containers: no line of their own; children are laid out by
		// doc.Layout one depth deeper. (True side-by-side columns are G3.)
		return nil
	case doc.BlockEquation:
		return r.renderText(b, width, indent, nil)
	default:
		return r.renderUnsupported(b, width, indent)
	}
}

// --- text-bearing blocks --------------------------------------------------

// renderText lays out a block's rich text wrapped to width, with an optional
// leading prefix on the first line and a hanging indent on continuations. prefix
// segments (e.g. a list marker) are measured so the wrap width accounts for them.
func (r *Renderer) renderText(b *doc.Block, width, indent int, prefix []doc.Segment) []doc.Line {
	prefixWidth := segsWidth(prefix)
	bodyWidth := width - indent - prefixWidth
	if bodyWidth < 1 {
		bodyWidth = 1
	}

	segs := RichText(b.RichText, r.Theme)
	wrapped := wrapSegments(segs, bodyWidth)

	lines := make([]doc.Line, 0, len(wrapped))
	for i, ws := range wrapped {
		var lineSegs []doc.Segment
		if i == 0 && len(prefix) > 0 {
			lineSegs = append(lineSegs, prefix...)
		} else if prefixWidth > 0 {
			// hanging indent: pad continuations under the body, not the marker.
			lineSegs = append(lineSegs, doc.Segment{Text: strings.Repeat(" ", prefixWidth)})
		}
		lineSegs = append(lineSegs, ws...)
		lines = append(lines, doc.Line{
			Segments: lineSegs,
			Indent:   indent,
			BlockID:  b.ID,
		})
	}
	if len(lines) == 0 {
		// An empty paragraph still occupies a row (paragraph spacing).
		lines = append(lines, doc.Line{Indent: indent, BlockID: b.ID})
	}
	return lines
}

// renderHeading lays out a heading, marking IsHeading/HeadingLevel so the line
// flows into Rendered.Outline. The page-title block (heading level 1 carrying a
// page Icon) prepends the emoji so the icon sits in the title line (SPEC §11 T1).
func (r *Renderer) renderHeading(b *doc.Block, width, indent int) []doc.Line {
	level := b.HeadingLvl
	if level == 0 {
		level = headingLevelFromType(b.Type)
	}

	var prefix []doc.Segment
	if b.Icon != nil && b.Icon.Emoji != "" {
		prefix = append(prefix, doc.Segment{Text: b.Icon.Emoji + " "})
	}

	// Headings are bold; the style is applied per-segment so wrapping is clean.
	segs := RichText(b.RichText, r.Theme)
	for i := range segs {
		segs[i].Style.Bold = true
	}

	prefixWidth := segsWidth(prefix)
	bodyWidth := width - indent - prefixWidth
	if bodyWidth < 1 {
		bodyWidth = 1
	}
	wrapped := wrapSegments(segs, bodyWidth)

	lines := make([]doc.Line, 0, len(wrapped))
	for i, ws := range wrapped {
		var lineSegs []doc.Segment
		if i == 0 && len(prefix) > 0 {
			lineSegs = append(lineSegs, prefix...)
		} else if prefixWidth > 0 {
			lineSegs = append(lineSegs, doc.Segment{Text: strings.Repeat(" ", prefixWidth)})
		}
		lineSegs = append(lineSegs, ws...)
		lines = append(lines, doc.Line{
			Segments:     lineSegs,
			Indent:       indent,
			BlockID:      b.ID,
			IsHeading:    true,
			HeadingLevel: level,
		})
	}
	if len(lines) == 0 {
		lines = append(lines, doc.Line{
			Indent: indent, BlockID: b.ID, IsHeading: true, HeadingLevel: level,
		})
	}
	return lines
}

// renderListItem renders a bulleted/numbered item with its marker. Numbered items
// use a static "1." marker for now (true sequence numbering needs sibling context
// the per-block renderer does not have; that is a layout-walk enhancement, noted).
func (r *Renderer) renderListItem(b *doc.Block, width, indent int, marker string) []doc.Line {
	prefix := []doc.Segment{{Text: marker + " "}}
	return r.renderText(b, width, indent, prefix)
}

// renderTodo renders a to_do item with a checkbox glyph reflecting Checked.
func (r *Renderer) renderTodo(b *doc.Block, width, indent int) []doc.Line {
	glyph := "[ ] "
	if b.Checked {
		glyph = "[x] "
	}
	prefix := []doc.Segment{{Text: glyph}}
	lines := r.renderText(b, width, indent, prefix)
	if b.Checked {
		// Completed items render struck-through (over the body, not the box).
		for i := range lines {
			for j := range lines[i].Segments {
				if j == 0 && i == 0 {
					continue // leave the checkbox glyph un-struck
				}
				lines[i].Segments[j].Style.Strike = true
			}
		}
	}
	return lines
}

// renderToggle renders a toggle's header line with a disclosure triangle; the
// body (Children) is laid out beneath it by doc.Layout. Collapsing is a G3 TUI
// concern — here the toggle always renders expanded.
func (r *Renderer) renderToggle(b *doc.Block, width, indent int) []doc.Line {
	prefix := []doc.Segment{{Text: "▾ "}}
	return r.renderText(b, width, indent, prefix)
}

// renderQuote renders a quote with a left bar on every wrapped line, body styled
// italic.
func (r *Renderer) renderQuote(b *doc.Block, width, indent int) []doc.Line {
	const bar = "▎ "
	barWidth := runewidth.StringWidth(bar)
	bodyWidth := width - indent - barWidth
	if bodyWidth < 1 {
		bodyWidth = 1
	}
	segs := RichText(b.RichText, r.Theme)
	for i := range segs {
		segs[i].Style.Italic = true
	}
	wrapped := wrapSegments(segs, bodyWidth)
	if len(wrapped) == 0 {
		wrapped = [][]doc.Segment{nil}
	}
	barStyle := doc.Style{Fg: r.Theme.Foreground("gray")}
	lines := make([]doc.Line, 0, len(wrapped))
	for _, ws := range wrapped {
		lineSegs := []doc.Segment{{Text: bar, Style: barStyle}}
		lineSegs = append(lineSegs, ws...)
		lines = append(lines, doc.Line{Segments: lineSegs, Indent: indent, BlockID: b.ID})
	}
	return lines
}

// renderDivider renders a full-width horizontal rule.
func (r *Renderer) renderDivider(b *doc.Block, width, indent int) []doc.Line {
	w := width - indent
	if w < 1 {
		w = 1
	}
	style := doc.Style{Fg: r.Theme.Foreground("gray")}
	return []doc.Line{{
		Segments: []doc.Segment{{Text: strings.Repeat("─", w), Style: style}},
		Indent:   indent,
		BlockID:  b.ID,
	}}
}

// --- callout (manually drawn box) -----------------------------------------

// renderCallout draws a callout as a manual box: a top border, body lines each
// prefixed with a vertical border + the icon (on the first line) and tinted with
// the callout's background color, and a bottom border. The box is assembled as
// border Segments carrying a border Style (SPEC §6) — nothing is .Render()'d.
func (r *Renderer) renderCallout(b *doc.Block, width, indent int) []doc.Line {
	boxWidth := width - indent
	if boxWidth < 4 {
		boxWidth = 4
	}
	inner := boxWidth - 4 // "│ " + content + " │"
	if inner < 1 {
		inner = 1
	}

	bg := ""
	if r.Theme != nil {
		switch {
		case IsBackground(b.Color):
			bg = r.Theme.Background(b.Color)
		case b.Color != "" && b.Color != "default":
			// a plain callout color (e.g. "blue") tints the box background.
			bg = r.Theme.Background(b.Color + "_background")
		}
	}
	borderStyle := doc.Style{Fg: r.Theme.Foreground("gray"), Bg: bg}
	bodyStyle := doc.Style{Bg: bg}

	var icon string
	if b.Icon != nil && b.Icon.Emoji != "" {
		icon = b.Icon.Emoji
	} else {
		icon = "💡"
	}

	segs := RichText(b.RichText, r.Theme)
	// First content line is prefixed with the icon; reserve its width.
	iconW := runewidth.StringWidth(icon) + 1

	wrapped := wrapSegments(segs, inner)
	if len(wrapped) == 0 {
		wrapped = [][]doc.Segment{nil}
	}

	var lines []doc.Line
	// top border
	lines = append(lines, doc.Line{
		Segments: []doc.Segment{{Text: "╭" + strings.Repeat("─", boxWidth-2) + "╮", Style: borderStyle}},
		Indent:   indent, BlockID: b.ID,
	})
	for i, ws := range wrapped {
		lineSegs := []doc.Segment{{Text: "│ ", Style: borderStyle}}
		content := ws
		used := segsWidth(ws)
		if i == 0 {
			lineSegs = append(lineSegs, doc.Segment{Text: icon + " ", Style: bodyStyle})
			used += iconW
		}
		lineSegs = append(lineSegs, applyBg(content, bg)...)
		if pad := inner - used; pad > 0 {
			lineSegs = append(lineSegs, doc.Segment{Text: strings.Repeat(" ", pad), Style: bodyStyle})
		}
		lineSegs = append(lineSegs, doc.Segment{Text: " │", Style: borderStyle})
		lines = append(lines, doc.Line{Segments: lineSegs, Indent: indent, BlockID: b.ID})
	}
	// bottom border
	lines = append(lines, doc.Line{
		Segments: []doc.Segment{{Text: "╰" + strings.Repeat("─", boxWidth-2) + "╯", Style: borderStyle}},
		Indent:   indent, BlockID: b.ID,
	})
	return lines
}

// applyBg returns the segments with the given background tint applied where the
// segment has none, so callout body text sits on the callout color.
func applyBg(segs []doc.Segment, bg string) []doc.Segment {
	if bg == "" {
		return segs
	}
	out := make([]doc.Segment, len(segs))
	for i, s := range segs {
		if s.Style.Bg == "" {
			s.Style.Bg = bg
		}
		out[i] = s
	}
	return out
}

// --- code (chroma syntax highlight -> Segments, NEVER chroma -> ANSI) -----

// renderCode draws a framed code block. The source is tokenized by chroma and
// each token mapped to a doc.Style; chroma is NOT allowed to emit ANSI (that
// would violate the paint rule — paint.go owns ANSI). A language label sits on
// the top border. Lines are wrapped to the frame width on rune boundaries (code
// is not word-wrapped).
func (r *Renderer) renderCode(b *doc.Block, width, indent int) []doc.Line {
	boxWidth := width - indent
	if boxWidth < 4 {
		boxWidth = 4
	}
	inner := boxWidth - 2
	if inner < 1 {
		inner = 1
	}
	source := plainText(b.RichText)
	highlighted := highlightCode(source, b.Language, r.Theme)

	borderStyle := doc.Style{Fg: r.Theme.Foreground("gray")}
	label := b.Language
	if label == "" {
		label = "code"
	}
	top := "╭─ " + label + " "
	if rest := boxWidth - runewidth.StringWidth(top) - 1; rest > 0 {
		top += strings.Repeat("─", rest) + "╮"
	} else {
		top = "╭" + strings.Repeat("─", boxWidth-2) + "╮"
	}

	var lines []doc.Line
	lines = append(lines, doc.Line{
		Segments: []doc.Segment{{Text: top, Style: borderStyle}},
		Indent:   indent, BlockID: b.ID,
	})
	for _, row := range highlighted {
		for _, vis := range wrapCodeRow(row, inner) {
			lineSegs := []doc.Segment{{Text: "│ ", Style: borderStyle}}
			lineSegs = append(lineSegs, vis...)
			if pad := inner - segsWidth(vis) - 1; pad > 0 {
				lineSegs = append(lineSegs, doc.Segment{Text: strings.Repeat(" ", pad)})
			}
			lineSegs = append(lineSegs, doc.Segment{Text: "│", Style: borderStyle})
			lines = append(lines, doc.Line{Segments: lineSegs, Indent: indent, BlockID: b.ID})
		}
	}
	lines = append(lines, doc.Line{
		Segments: []doc.Segment{{Text: "╰" + strings.Repeat("─", boxWidth-2) + "╯", Style: borderStyle}},
		Indent:   indent, BlockID: b.ID,
	})
	return lines
}

// wrapCodeRow rune-splits one tokenized code row to width, preserving token
// styles. Code is not word-wrapped (a break mid-token is acceptable and expected).
func wrapCodeRow(row []doc.Segment, width int) [][]doc.Segment {
	if width <= 0 {
		return [][]doc.Segment{row}
	}
	var lines [][]doc.Segment
	var cur []doc.Segment
	w := 0
	for _, seg := range row {
		for _, piece := range hardSplit(seg.Text, width) {
			pw := runewidth.StringWidth(piece)
			if w > 0 && w+pw > width {
				lines = append(lines, cur)
				cur = nil
				w = 0
			}
			cur = append(cur, doc.Segment{Text: piece, Style: seg.Style})
			w += pw
		}
	}
	lines = append(lines, cur)
	return lines
}

// --- table (box-drawn grid fit to width) ----------------------------------

// renderTable draws a table as a box-drawn grid. Column widths are fit to the
// available width proportionally to the widest cell; the header row (if any) is
// bolded. Rows come from the table's table_row children (their Cells field). The
// grid is assembled as Segments with a border Style — no .Render().
func (r *Renderer) renderTable(b *doc.Block, width, indent int) []doc.Line {
	rows := tableRows(b)
	if len(rows) == 0 {
		return nil
	}
	ncols := b.TableWidth
	for _, row := range rows {
		if len(row) > ncols {
			ncols = len(row)
		}
	}
	if ncols == 0 {
		return nil
	}

	avail := width - indent
	colWidths := fitColumns(rows, ncols, avail)
	borderStyle := doc.Style{Fg: r.Theme.Foreground("gray")}

	var lines []doc.Line
	emit := func(segs []doc.Segment) {
		lines = append(lines, doc.Line{Segments: segs, Indent: indent, BlockID: b.ID})
	}

	emit([]doc.Segment{{Text: gridBorder(colWidths, "┌", "┬", "┐"), Style: borderStyle}})
	for ri, row := range rows {
		header := ri == 0 && b.HasColumnHeader
		for _, vis := range wrapTableRow(row, colWidths, header, r.Theme, borderStyle) {
			emit(vis)
		}
		if ri == 0 && b.HasColumnHeader {
			emit([]doc.Segment{{Text: gridBorder(colWidths, "├", "┼", "┤"), Style: borderStyle}})
		}
	}
	emit([]doc.Segment{{Text: gridBorder(colWidths, "└", "┴", "┘"), Style: borderStyle}})
	return lines
}

// tableRows returns the table's row cells from its table_row children.
func tableRows(b *doc.Block) [][][]doc.RichText {
	var rows [][][]doc.RichText
	for i := range b.Children {
		c := &b.Children[i]
		if c.Type == doc.BlockTableRow {
			rows = append(rows, c.Cells)
		}
	}
	return rows
}

// fitColumns computes per-column display widths that sum (with separators) to no
// more than avail, scaling down proportionally to each column's natural max width.
func fitColumns(rows [][][]doc.RichText, ncols, avail int) []int {
	natural := make([]int, ncols)
	for _, row := range rows {
		for c := 0; c < ncols && c < len(row); c++ {
			w := runewidth.StringWidth(plainText(row[c]))
			if w > natural[c] {
				natural[c] = w
			}
		}
	}
	// borders: ncols+1 vertical bars, each col padded by 2 ("│ x ").
	overhead := ncols + 1 + ncols*2
	budget := avail - overhead
	if budget < ncols {
		budget = ncols // at least 1 col each
	}
	total := 0
	for _, n := range natural {
		if n < 1 {
			n = 1
		}
		total += n
	}
	if total == 0 {
		total = ncols
	}
	out := make([]int, ncols)
	used := 0
	for c := 0; c < ncols; c++ {
		n := natural[c]
		if n < 1 {
			n = 1
		}
		w := n
		if total > budget {
			w = n * budget / total
			if w < 1 {
				w = 1
			}
		}
		out[c] = w
		used += w
	}
	// hand any rounding slack to the last column.
	if total > budget {
		if slack := budget - used; slack > 0 {
			out[ncols-1] += slack
		}
	}
	return out
}

// gridBorder builds a horizontal grid rule with the given corner/junction glyphs.
func gridBorder(widths []int, left, mid, right string) string {
	var b strings.Builder
	b.WriteString(left)
	for i, w := range widths {
		b.WriteString(strings.Repeat("─", w+2))
		if i < len(widths)-1 {
			b.WriteString(mid)
		}
	}
	b.WriteString(right)
	return b.String()
}

// wrapTableRow lays out one table row's cells into one or more visual lines,
// each cell clipped/wrapped to its column width. Header cells are bolded. Cells
// are top-aligned; shorter cells pad with blanks.
func wrapTableRow(row [][]doc.RichText, widths []int, header bool, theme *Theme, border doc.Style) [][]doc.Segment {
	cellLines := make([][][]doc.Segment, len(widths))
	maxLines := 1
	for c := range widths {
		var segs []doc.Segment
		if c < len(row) {
			segs = RichText(row[c], theme)
			if header {
				for i := range segs {
					segs[i].Style.Bold = true
				}
			}
		}
		wrapped := wrapSegments(segs, widths[c])
		if len(wrapped) == 0 {
			wrapped = [][]doc.Segment{nil}
		}
		cellLines[c] = wrapped
		if len(wrapped) > maxLines {
			maxLines = len(wrapped)
		}
	}

	out := make([][]doc.Segment, 0, maxLines)
	for li := 0; li < maxLines; li++ {
		lineSegs := []doc.Segment{{Text: "│ ", Style: border}}
		for c := range widths {
			var cell []doc.Segment
			if li < len(cellLines[c]) {
				cell = cellLines[c][li]
			}
			lineSegs = append(lineSegs, cell...)
			if pad := widths[c] - segsWidth(cell); pad > 0 {
				lineSegs = append(lineSegs, doc.Segment{Text: strings.Repeat(" ", pad)})
			}
			lineSegs = append(lineSegs, doc.Segment{Text: " │ ", Style: border})
		}
		// trim trailing space inside the last separator: "│ " not "│ ".
		out = append(out, lineSegs)
	}
	return out
}

// --- page properties, child refs, bookmarks, unsupported ------------------

// renderProps lays out the page-property block as labeled "Name: value" rows
// (one per Cell row built in doc.propsToBlock), the name bolded.
func (r *Renderer) renderProps(b *doc.Block, width, indent int) []doc.Line {
	var lines []doc.Line
	for _, cell := range b.Cells {
		if len(cell) < 2 {
			continue
		}
		segs := []doc.Segment{
			{Text: cell[0].Text + ": ", Style: doc.Style{Bold: true, Fg: r.Theme.Foreground("gray")}},
		}
		valueWidth := width - indent - runewidth.StringWidth(cell[0].Text+": ")
		if valueWidth < 1 {
			valueWidth = 1
		}
		wrapped := wrapSegments([]doc.Segment{{Text: cell[1].Text}}, valueWidth)
		if len(wrapped) > 0 {
			segs = append(segs, wrapped[0]...)
		}
		lines = append(lines, doc.Line{Segments: segs, Indent: indent, BlockID: b.ID})
		for _, cont := range wrapped[1:] {
			pad := doc.Segment{Text: strings.Repeat(" ", runewidth.StringWidth(cell[0].Text+": "))}
			lines = append(lines, doc.Line{
				Segments: append([]doc.Segment{pad}, cont...),
				Indent:   indent, BlockID: b.ID,
			})
		}
	}
	if len(lines) > 0 {
		// a divider under the properties separates them from the body.
		w := width - indent
		if w < 1 {
			w = 1
		}
		lines = append(lines, doc.Line{
			Segments: []doc.Segment{{Text: strings.Repeat("─", w), Style: doc.Style{Fg: r.Theme.Foreground("gray")}}},
			Indent:   indent, BlockID: b.ID,
		})
	}
	return lines
}

// renderChildRef renders a child_page / child_database / link_to_page as a
// navigable-looking line (the nav itself is G3). The target id rides on the IR.
func (r *Renderer) renderChildRef(b *doc.Block, width, indent int) []doc.Line {
	title := plainText(b.RichText)
	if title == "" {
		title = "(linked page)"
	}
	glyph := "📄 "
	switch b.Type {
	case doc.BlockChildDatabase:
		glyph = "🗂 "
	case doc.BlockLinkToPage:
		glyph = "🔗 "
	}
	segs := []doc.Segment{
		{Text: glyph},
		{Text: title, Style: doc.Style{Underline: true, Fg: r.Theme.Foreground("blue")}},
	}
	return []doc.Line{{Segments: segs, Indent: indent, BlockID: b.ID}}
}

// renderBookmark renders a bookmark / link_preview as a captioned URL card line.
func (r *Renderer) renderBookmark(b *doc.Block, width, indent int) []doc.Line {
	label := plainText(b.Caption)
	if label == "" {
		label = b.URL
	}
	if label == "" {
		label = "(bookmark)"
	}
	segs := []doc.Segment{
		{Text: "🔖 "},
		{Text: label, Style: doc.Style{Underline: true, Fg: r.Theme.Foreground("blue"), Href: b.URL}},
	}
	return []doc.Line{{Segments: segs, Indent: indent, BlockID: b.ID}}
}

// renderUnsupported emits a single muted placeholder so an unhandled block is
// visible (its presence acknowledged) without crashing or dumping raw JSON into
// the reader (SPEC §5 graceful degradation).
func (r *Renderer) renderUnsupported(b *doc.Block, width, indent int) []doc.Line {
	label := "[" + string(b.Type) + "]"
	if b.Type == doc.BlockUnsupported {
		label = "[unsupported block]"
	}
	return []doc.Line{{
		Segments: []doc.Segment{{Text: label, Style: doc.Style{Italic: true, Fg: r.Theme.Foreground("gray")}}},
		Indent:   indent, BlockID: b.ID,
	}}
}

// --- small helpers --------------------------------------------------------

func headingLevelFromType(t doc.BlockType) int {
	switch t {
	case doc.BlockHeading1:
		return 1
	case doc.BlockHeading2:
		return 2
	case doc.BlockHeading3:
		return 3
	default:
		return 0
	}
}

// bulletMarker picks a bullet glyph by nesting depth so nested lists read
// distinctly (•, ◦, ▪ cycling).
func bulletMarker(depth int) string {
	switch depth % 3 {
	case 0:
		return "•"
	case 1:
		return "◦"
	default:
		return "▪"
	}
}

// plainText concatenates the plain text of a rich-text run.
func plainText(rts []doc.RichText) string {
	var b strings.Builder
	for _, rt := range rts {
		if rt.Text != "" {
			b.WriteString(rt.Text)
		} else if rt.Mention != nil {
			b.WriteString(rt.Mention.Label)
		}
	}
	return b.String()
}
