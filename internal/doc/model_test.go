package doc

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update regenerates the golden files from the current Build+Dump output. Run
// `go test ./internal/doc -update` after an intentional IR change, then review
// the golden diff before committing.
var update = flag.Bool("update", false, "update golden files")

// fixtures are the captured real-page block trees (SPEC §9). Each is a cachedPage
// envelope ({page, blocks}) as produced by `nui dump --json`, covering a
// deliberate spread: prose-heavy, callouts/columns/table/toggle/image/child_database,
// a deeply nested toggle tree, and a page carrying an unsupported block.
var fixtures = []string{
	"prose",
	"rich",
	"toggles",
	"unsupported",
}

// cachedPage mirrors notion.cachedPage: the page metadata plus the assembled raw
// block tree. Defined here too because doc must not import notion (SPEC §4).
type cachedPage struct {
	Page   RawPage    `json:"page"`
	Blocks []RawBlock `json:"blocks"`
}

// loadFixture decodes a testdata/<name>.json cachedPage envelope. The captured
// fixtures are valid JSON (ntn escapes Notion's control chars as \uXXXX on
// output), so a plain Unmarshal suffices here; the tolerant-decode path is
// exercised separately in the notion package against a raw-control-char fixture.
func loadFixture(t *testing.T, name string) cachedPage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var cp cachedPage
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return cp
}

// TestBuildGolden is the core G1 parity check: each real fixture is Built into a
// Document and serialized via Dump (text + type + nesting, never ANSI — SPEC §9),
// then compared against a committed golden. A drift means the IR mapping changed.
func TestBuildGolden(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			cp := loadFixture(t, name)
			d := Build(cp.Page, cp.Blocks)
			got := Dump(d)

			goldenPath := filepath.Join("testdata", name+".golden")
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
				t.Errorf("Dump(%s) mismatch with golden.\n--- got ---\n%s\n--- want ---\n%s",
					name, firstLines(got, 40), firstLines(string(want), 40))
			}
		})
	}
}

// TestBuildDeterministic asserts Build+Dump is stable across runs (no map-order
// nondeterminism leaked into the output). Properties are sorted; block order is
// the API's. Two Builds of the same fixture must Dump identically.
func TestBuildDeterministic(t *testing.T) {
	cp := loadFixture(t, "rich")
	a := Dump(Build(cp.Page, cp.Blocks))
	b := Dump(Build(cp.Page, cp.Blocks))
	if a != b {
		t.Fatal("Build+Dump is nondeterministic across runs")
	}
}

// TestRichFixtureCoversTypes guards the rich fixture's value: it must continue to
// exercise the structurally interesting block types (callout, column, table,
// toggle, image, child_database) so the golden keeps testing them.
func TestRichFixtureCoversTypes(t *testing.T) {
	cp := loadFixture(t, "rich")
	d := Build(cp.Page, cp.Blocks)
	seen := map[BlockType]bool{}
	var walk func([]Block)
	walk = func(bs []Block) {
		for _, b := range bs {
			seen[b.Type] = true
			walk(b.Children)
		}
	}
	walk(d.Blocks)
	for _, want := range []BlockType{
		BlockCallout, BlockColumnList, BlockColumn, BlockTable, BlockTableRow,
		BlockToggle, BlockImage, BlockChildDatabase,
	} {
		if !seen[want] {
			t.Errorf("rich fixture no longer contains %s; golden coverage weakened", want)
		}
	}
}

// TestUnsupportedFixtureDegrades verifies the graceful-degradation contract: a
// real page carrying a type outside the enum yields a BlockUnsupported node with
// Raw populated, and no sibling is dropped (SPEC §5 acceptance).
func TestUnsupportedFixtureDegrades(t *testing.T) {
	cp := loadFixture(t, "unsupported")
	d := Build(cp.Page, cp.Blocks)

	var unsupported, total int
	var walk func([]Block)
	walk = func(bs []Block) {
		for _, b := range bs {
			total++
			if b.Type == BlockUnsupported {
				unsupported++
				if len(b.Raw) == 0 {
					t.Errorf("BlockUnsupported %s has empty Raw", b.ID)
				}
			}
			walk(b.Children)
		}
	}
	walk(d.Blocks)

	if unsupported == 0 {
		t.Fatal("unsupported fixture produced no BlockUnsupported node")
	}
	// Sanity: siblings survived (the page has more than just the unsupported block).
	if total <= unsupported {
		t.Fatalf("expected supported siblings alongside %d unsupported (total=%d)", unsupported, total)
	}
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
