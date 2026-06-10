package render

import (
	"strings"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/charmbracelet/lipgloss"
)

// paint.go is THE ONLY place ANSI is produced (SPEC §6 PAINT RULE). Everywhere
// else in render emits structured doc.Line / doc.Segment; here a Line's Segments
// are turned into a terminal string by building a lipgloss.Style per Segment and
// concatenating, with OSC-8 hyperlinks for Href and an optional search-highlight
// overlay applied on top. The overlay is a parameter, never a mutation of the
// Line — the same Line paints differently depending on the current search hit.

// HighlightRange marks a [Start,End) column span on a line to receive the search
// highlight overlay. Columns are display columns into the painted line's visible
// text (after Indent). G3's in-doc search produces these; G2 stubs their use (the
// param is accepted and honored, but no caller passes ranges yet).
type HighlightRange struct {
	Start, End int
}

// PaintOpts carries paint-time knobs. Highlights is the search overlay (may be
// empty/nil). NoColor disables color output (e.g. when piping or NO_COLOR is set)
// while still emitting text + structure. Hyperlinks enables OSC-8 link wrapping
// (terminals that don't support it ignore the sequence; some render it as noise,
// so it is opt-in).
type PaintOpts struct {
	Highlights []HighlightRange
	NoColor    bool
	Hyperlinks bool
}

// Paint renders one doc.Line to an ANSI string: leading indent, then each
// Segment styled and (if Href set + Hyperlinks on) OSC-8 wrapped, with the search
// overlay applied over the matched columns. It is the sole ANSI producer; it must
// never be called from the pure layers.
func Paint(line doc.Line, opts PaintOpts) string {
	var b strings.Builder
	if line.Indent > 0 {
		b.WriteString(strings.Repeat(" ", line.Indent))
	}

	col := 0 // display column within the visible (post-indent) text
	for _, seg := range line.Segments {
		painted := paintSegment(seg, col, opts)
		b.WriteString(painted)
		col += displayWidth(seg.Text)
	}
	return b.String()
}

// paintSegment styles one segment, splitting it where a highlight range overlaps
// so only the matched columns get the overlay. col is the segment's starting
// display column on the line.
func paintSegment(seg doc.Segment, col int, opts PaintOpts) string {
	base := lipStyle(seg.Style, opts.NoColor)

	// Fast path: no overlay touches this segment.
	if !overlaps(seg, col, opts.Highlights) {
		return wrapHref(base.Render(seg.Text), seg.Style.Href, opts)
	}

	// Slow path: walk the segment rune-by-rune, toggling the overlay per column.
	var b strings.Builder
	c := col
	var run strings.Builder
	runHi := highlighted(c, opts.Highlights)
	flush := func() {
		if run.Len() == 0 {
			return
		}
		st := base
		if runHi {
			st = st.Reverse(true)
		}
		b.WriteString(st.Render(run.String()))
		run.Reset()
	}
	for _, r := range seg.Text {
		hi := highlighted(c, opts.Highlights)
		if hi != runHi {
			flush()
			runHi = hi
		}
		run.WriteRune(r)
		c += runeDisplayWidth(r)
	}
	flush()
	return wrapHref(b.String(), seg.Style.Href, opts)
}

// lipStyle builds a lipgloss.Style from a doc.Style. This is the single
// Style -> lipgloss bridge; the booleans and color names on doc.Style are the
// source of truth (the paint rule keeps the rest of render from doing this).
func lipStyle(s doc.Style, noColor bool) lipgloss.Style {
	st := lipgloss.NewStyle()
	if s.Bold {
		st = st.Bold(true)
	}
	if s.Italic {
		st = st.Italic(true)
	}
	if s.Underline {
		st = st.Underline(true)
	}
	if s.Strike {
		st = st.Strikethrough(true)
	}
	if !noColor {
		if s.Fg != "" {
			st = st.Foreground(lipgloss.Color(s.Fg))
		}
		if s.Bg != "" {
			st = st.Background(lipgloss.Color(s.Bg))
		}
	}
	return st
}

// wrapHref wraps text in an OSC-8 hyperlink escape when a target is set and
// hyperlinks are enabled. Terminals lacking OSC-8 support treat the escape as a
// no-op; we gate it behind Hyperlinks so output stays clean where it would be
// rendered literally.
func wrapHref(text, href string, opts PaintOpts) string {
	if href == "" || !opts.Hyperlinks {
		return text
	}
	const (
		osc8Open  = "\x1b]8;;"
		osc8Close = "\x1b]8;;\x07"
		st        = "\x07"
	)
	return osc8Open + href + st + text + osc8Close
}

// overlaps reports whether any highlight range intersects the segment occupying
// [col, col+width).
func overlaps(seg doc.Segment, col int, ranges []HighlightRange) bool {
	if len(ranges) == 0 {
		return false
	}
	w := displayWidth(seg.Text)
	for _, r := range ranges {
		if r.Start < col+w && r.End > col {
			return true
		}
	}
	return false
}

// highlighted reports whether display column c falls inside any highlight range.
func highlighted(c int, ranges []HighlightRange) bool {
	for _, r := range ranges {
		if c >= r.Start && c < r.End {
			return true
		}
	}
	return false
}
