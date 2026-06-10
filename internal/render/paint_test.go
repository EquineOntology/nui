package render_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/render"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// paintCases enumerate the Segment->ANSI mapping the paint-isolation golden
// asserts (SPEC §9, the test the G1 review asked for). Each case is one Line; the
// golden captures the exact escape sequences Paint emits so a regression in the
// single ANSI producer is caught.
var paintCases = []struct {
	name string
	line doc.Line
	opts render.PaintOpts
}{
	{name: "plain", line: lineOf(doc.Segment{Text: "hello"})},
	{name: "bold", line: lineOf(doc.Segment{Text: "bold", Style: doc.Style{Bold: true}})},
	{name: "italic", line: lineOf(doc.Segment{Text: "ital", Style: doc.Style{Italic: true}})},
	{name: "underline", line: lineOf(doc.Segment{Text: "und", Style: doc.Style{Underline: true}})},
	{name: "strike", line: lineOf(doc.Segment{Text: "del", Style: doc.Style{Strike: true}})},
	{name: "code", line: lineOf(doc.Segment{Text: "x()", Style: doc.Style{Code: true, Fg: "#d44c47"}})},
	{name: "fg-red", line: lineOf(doc.Segment{Text: "red", Style: doc.Style{Fg: "#d44c47"}})},
	{name: "bg-blue", line: lineOf(doc.Segment{Text: "bg", Style: doc.Style{Bg: "#ddebf1"}})},
	{
		name: "bold-italic-color",
		line: lineOf(doc.Segment{Text: "mix", Style: doc.Style{Bold: true, Italic: true, Fg: "#448361"}}),
	},
	{
		name: "indent",
		line: doc.Line{Indent: 4, Segments: []doc.Segment{{Text: "in"}}},
	},
	{
		name: "multi-segment",
		line: lineOf(
			doc.Segment{Text: "a "},
			doc.Segment{Text: "B", Style: doc.Style{Bold: true}},
			doc.Segment{Text: " c"},
		),
	},
	{
		name: "href-on",
		line: lineOf(doc.Segment{Text: "link", Style: doc.Style{Href: "https://example.com", Underline: true}}),
		opts: render.PaintOpts{Hyperlinks: true},
	},
	{
		name: "href-off",
		line: lineOf(doc.Segment{Text: "link", Style: doc.Style{Href: "https://example.com", Underline: true}}),
		opts: render.PaintOpts{Hyperlinks: false},
	},
	{
		name: "highlight-overlay",
		line: lineOf(doc.Segment{Text: "find me here"}),
		opts: render.PaintOpts{Highlights: []render.HighlightRange{{Start: 5, End: 7}}},
	},
	{
		name: "nocolor",
		line: lineOf(doc.Segment{Text: "red", Style: doc.Style{Fg: "#d44c47", Bold: true}}),
		opts: render.PaintOpts{NoColor: true},
	},
}

// TestPaintGolden is the paint-isolation golden (SPEC §9): it asserts the exact
// Segment->ANSI output for each annotation/color/overlay in one file, separate
// from the structural layout golden. ANSI escapes are escaped with %q so the
// golden is reviewable and diffs are legible.
func TestPaintGolden(t *testing.T) {
	// Force a truecolor profile so the golden is stable regardless of the
	// environment running the test (CI has no TTY; lipgloss would otherwise
	// downsample colors and the golden would drift).
	restore := forceColorProfile()
	defer restore()

	var b strings.Builder
	for _, tc := range paintCases {
		got := render.Paint(tc.line, tc.opts)
		fmt.Fprintf(&b, "%s: %q\n", tc.name, got)
	}
	got := b.String()

	goldenPath := filepath.Join("testdata", "paint.golden")
	if *update {
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("Paint golden mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, string(want))
	}
}

// TestPaintNeverPanics feeds Paint degenerate input (empty line, zero-width
// highlight, wide runes under a highlight) to assert it never panics — paint is
// on the hot path for every visible row.
func TestPaintNeverPanics(t *testing.T) {
	cases := []doc.Line{
		{},
		{Segments: []doc.Segment{{Text: ""}}},
		{Indent: 3},
		{Segments: []doc.Segment{{Text: "日本語テスト"}}},
	}
	hi := render.PaintOpts{Highlights: []render.HighlightRange{{Start: 0, End: 100}}}
	for i, ln := range cases {
		_ = render.Paint(ln, render.PaintOpts{})
		_ = render.Paint(ln, hi)
		_ = i
	}
}

func lineOf(segs ...doc.Segment) doc.Line {
	return doc.Line{Segments: segs}
}

// forceColorProfile pins lipgloss's default renderer to TrueColor so the paint
// golden is stable in any environment (no TTY in CI; otherwise lipgloss would
// downsample or strip color and the escapes would drift). Returns a restore func.
func forceColorProfile() func() {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	return func() { lipgloss.SetColorProfile(prev) }
}
