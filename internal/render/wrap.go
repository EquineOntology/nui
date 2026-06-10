package render

import (
	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/mattn/go-runewidth"
)

// wrap.go owns the segment-aware word wrapping. Layout pre-wraps to width (SPEC
// §6); the viewport never re-wraps. Wrapping operates on []doc.Segment so style
// runs survive the break — a bold phrase split across two lines stays bold on
// both. It is display-width aware (CJK / wide runes via go-runewidth) so the
// reader's column math is correct, and it never produces ANSI.

// segWidth returns the display width of a segment's text.
func segWidth(s doc.Segment) int {
	return runewidth.StringWidth(s.Text)
}

// displayWidth returns the display column width of a string (CJK/wide aware).
func displayWidth(s string) int {
	return runewidth.StringWidth(s)
}

// runeDisplayWidth returns the display column width of a single rune.
func runeDisplayWidth(r rune) int {
	return runewidth.RuneWidth(r)
}

// segsWidth returns the total display width of a segment run.
func segsWidth(segs []doc.Segment) int {
	w := 0
	for _, s := range segs {
		w += segWidth(s)
	}
	return w
}

// wrapSegments breaks a run of styled segments into lines no wider than width
// display columns, preferring to break on spaces. A single token longer than
// width is hard-split on a rune boundary (so a long URL or code token still fits).
// Returns at least one (possibly empty) line so an empty paragraph still occupies
// a row. width <= 0 yields the input as a single line (degrade, never divide by
// zero).
func wrapSegments(segs []doc.Segment, width int) [][]doc.Segment {
	if width <= 0 {
		return [][]doc.Segment{segs}
	}

	var lines [][]doc.Segment
	var cur []doc.Segment
	curWidth := 0

	flush := func() {
		lines = append(lines, cur)
		cur = nil
		curWidth = 0
	}

	for _, seg := range segs {
		for _, word := range splitWords(seg.Text) {
			ww := runewidth.StringWidth(word.text)

			// A word wider than the whole line: hard-split it.
			if ww > width {
				if curWidth > 0 {
					flush()
				}
				for _, piece := range hardSplit(word.text, width) {
					pw := runewidth.StringWidth(piece)
					if curWidth > 0 && curWidth+pw > width {
						flush()
					}
					cur = appendText(cur, piece, seg.Style)
					curWidth += pw
				}
				continue
			}

			// Whitespace-only token: keep it on the current line if it fits,
			// otherwise it is dropped at the wrap point (trailing space elision).
			if word.space {
				if curWidth == 0 {
					continue // no leading space on a fresh line
				}
				if curWidth+ww > width {
					flush()
					continue
				}
				cur = appendText(cur, word.text, seg.Style)
				curWidth += ww
				continue
			}

			if curWidth+ww > width {
				flush()
			}
			cur = appendText(cur, word.text, seg.Style)
			curWidth += ww
		}
	}
	flush()
	return lines
}

// appendText appends text under style to a segment run, merging into the trailing
// segment when the style matches so adjacent same-style runs do not fragment.
func appendText(segs []doc.Segment, text string, style doc.Style) []doc.Segment {
	if text == "" {
		return segs
	}
	if n := len(segs); n > 0 && segs[n-1].Style == style {
		segs[n-1].Text += text
		return segs
	}
	return append(segs, doc.Segment{Text: text, Style: style})
}

// token is one word or one whitespace gap from splitWords.
type token struct {
	text  string
	space bool
}

// splitWords splits a string into alternating word and whitespace tokens,
// preserving the whitespace runs so wrapping can collapse them at line breaks
// while keeping interior spacing.
func splitWords(s string) []token {
	if s == "" {
		return nil
	}
	var toks []token
	var b []rune
	inSpace := false
	flush := func(space bool) {
		if len(b) > 0 {
			toks = append(toks, token{text: string(b), space: space})
			b = b[:0]
		}
	}
	for _, r := range s {
		isSpace := r == ' ' || r == '\t'
		if isSpace != inSpace && len(b) > 0 {
			flush(inSpace)
		}
		inSpace = isSpace
		b = append(b, r)
	}
	flush(inSpace)
	return toks
}

// hardSplit breaks a single over-long token into width-bounded pieces on rune
// boundaries (display-width aware), used when one word exceeds the line width.
func hardSplit(s string, width int) []string {
	var pieces []string
	var b []rune
	w := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > width && len(b) > 0 {
			pieces = append(pieces, string(b))
			b = b[:0]
			w = 0
		}
		b = append(b, r)
		w += rw
	}
	if len(b) > 0 {
		pieces = append(pieces, string(b))
	}
	return pieces
}
