package render_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/render"
)

// update regenerates the golden files. Run `go test ./internal/render -update`
// after an intentional layout change, then review the golden diff.
var update = flag.Bool("update", false, "update golden files")

// goldenWidth is the fixed width all layout goldens are produced at. A fixed
// width keeps the wrapped output deterministic across machines.
const goldenWidth = 72

// layoutFixtures reuse the G1 captured pages (doc/testdata). The layout golden
// asserts the full Document -> Layout -> Rendered mapping over the same spread the
// doc-layer golden covers (prose, callouts/columns/table/toggle, deep nesting, an
// unsupported block).
var layoutFixtures = []string{"prose", "rich", "toggles", "unsupported"}

// cachedPage mirrors the {page, blocks} envelope `nui dump --json` writes.
type cachedPage struct {
	Page   doc.RawPage    `json:"page"`
	Blocks []doc.RawBlock `json:"blocks"`
}

func loadFixture(t *testing.T, name string) cachedPage {
	t.Helper()
	// Fixtures live in the doc package's testdata; reuse them rather than
	// duplicating ~700KB of JSON.
	path := filepath.Join("..", "doc", "testdata", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var cp cachedPage
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return cp
}

// TestLayoutGolden is the core Track-A parity check (SPEC §9): each real fixture
// is Built, laid out at a fixed width, and serialized as a readable line dump
// (text + style flags + indent + blockID + heading info — NEVER raw ANSI), then
// compared against a committed golden. Drift means the layout/render mapping
// changed.
func TestLayoutGolden(t *testing.T) {
	theme := render.DefaultTheme()
	opts := doc.LayoutOpts{ShowProps: true}
	r := render.NewRenderer(theme, opts)

	for _, name := range layoutFixtures {
		t.Run(name, func(t *testing.T) {
			cp := loadFixture(t, name)
			d := doc.Build(cp.Page, cp.Blocks)
			rendered := doc.Layout(d, goldenWidth, r, opts)
			got := serializeRendered(rendered)

			goldenPath := filepath.Join("testdata", name+".layout.golden")
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
				t.Errorf("Layout(%s) golden mismatch.\n--- got (first 50 lines) ---\n%s",
					name, firstLines(got, 50))
			}
		})
	}
}

// serializeRendered dumps a *doc.Rendered as a deterministic, ANSI-free text
// representation: a header with the outline + anchor count, then one block per
// Line showing indent, blockID, heading info, and each Segment's text + style
// flags. This is the golden representation (SPEC §9: structure, never ANSI).
func serializeRendered(r *doc.Rendered) string {
	var b strings.Builder
	fmt.Fprintf(&b, "lines=%d outline=%d anchors=%d\n", len(r.Lines), len(r.Outline), len(r.Anchors))
	b.WriteString("=== OUTLINE ===\n")
	for _, h := range r.Outline {
		fmt.Fprintf(&b, "  L%d h%d %q\n", h.LineIdx, h.Level, h.Text)
	}
	b.WriteString("=== LINES ===\n")
	for i, ln := range r.Lines {
		fmt.Fprintf(&b, "[%03d] indent=%d block=%s", i, ln.Indent, shortID(ln.BlockID))
		if ln.IsHeading {
			fmt.Fprintf(&b, " H%d", ln.HeadingLevel)
		}
		b.WriteByte('\n')
		for _, seg := range ln.Segments {
			fmt.Fprintf(&b, "      %q%s\n", seg.Text, styleFlags(seg.Style))
		}
	}
	return b.String()
}

// styleFlags renders a doc.Style's non-zero fields compactly, e.g. "{b i fg=#... href=...}".
func styleFlags(s doc.Style) string {
	var parts []string
	if s.Bold {
		parts = append(parts, "b")
	}
	if s.Italic {
		parts = append(parts, "i")
	}
	if s.Underline {
		parts = append(parts, "u")
	}
	if s.Strike {
		parts = append(parts, "s")
	}
	if s.Code {
		parts = append(parts, "code")
	}
	if s.Fg != "" {
		parts = append(parts, "fg="+s.Fg)
	}
	if s.Bg != "" {
		parts = append(parts, "bg="+s.Bg)
	}
	if s.Href != "" {
		parts = append(parts, "href="+s.Href)
	}
	if len(parts) == 0 {
		return ""
	}
	return " {" + strings.Join(parts, " ") + "}"
}

// shortID trims a normalized id to its first 8 chars for a readable dump, keeping
// the empty case explicit.
func shortID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstLines(s string, n int) string {
	count := 0
	for i, r := range s {
		if r == '\n' {
			count++
			if count >= n {
				return s[:i]
			}
		}
	}
	return s
}
