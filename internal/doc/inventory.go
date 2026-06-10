package doc

import (
	"fmt"
	"sort"
	"strings"
)

// Tier classifies a block type by feature tier (SPEC §11), so the inventory can
// report coverage as "what nui handles today" vs "the unhandled long tail" and
// drive G2/G3 triage.
type Tier int

const (
	TierUnknown Tier = iota // not in any tier list -> BlockUnsupported / long tail
	Tier1                   // rock-solid read, ~90% of daily use (G2)
	Tier2                   // structured (G3)
	Tier3                   // media / rich (G5)
)

func (t Tier) String() string {
	switch t {
	case Tier1:
		return "T1"
	case Tier2:
		return "T2"
	case Tier3:
		return "T3"
	default:
		return "long-tail"
	}
}

// tierOf maps a raw block type name to its feature tier (SPEC §11). Types not
// listed are TierUnknown — the long tail that BlockUnsupported absorbs.
var tierOf = map[string]Tier{
	// T1
	"paragraph":          Tier1,
	"heading_1":          Tier1,
	"heading_2":          Tier1,
	"heading_3":          Tier1,
	"bulleted_list_item": Tier1,
	"numbered_list_item": Tier1,
	"to_do":              Tier1,
	"quote":              Tier1,
	"divider":            Tier1,
	"code":               Tier1,
	"callout":            Tier1,
	"table":              Tier1,
	"table_row":          Tier1,
	// T2
	"toggle":            Tier2,
	"column_list":       Tier2,
	"column":            Tier2,
	"table_of_contents": Tier2,
	"child_page":        Tier2,
	"child_database":    Tier2,
	"link_to_page":      Tier2,
	"bookmark":          Tier2,
	"link_preview":      Tier2,
	// T3
	"image":        Tier3,
	"video":        Tier3,
	"file":         Tier3,
	"pdf":          Tier3,
	"audio":        Tier3,
	"equation":     Tier3,
	"synced_block": Tier3,
	"breadcrumb":   Tier3,
}

// Inventory tallies block-type frequencies across one or more block trees and
// classifies each by tier and by whether Build maps it to first-class IR. It is
// pure (operates on the IR), so it is unit-testable and reused by `nui inventory`.
type Inventory struct {
	Counts  map[string]int // raw block type -> occurrences
	Total   int            // total blocks counted (across the whole tree)
	Pages   int            // number of documents walked
	tierSum map[Tier]int   // cached per-tier sums, filled by finalize
}

// NewInventory returns an empty inventory ready for AddDocument.
func NewInventory() *Inventory {
	return &Inventory{
		Counts:  make(map[string]int),
		tierSum: make(map[Tier]int),
	}
}

// AddDocument walks a Document's block tree (depth-first, including children)
// and tallies each block's type. The IR's Type is the post-Build type, so an
// unhandled raw type shows up as "unsupported".
func (inv *Inventory) AddDocument(d *Document) {
	if d == nil {
		return
	}
	inv.Pages++
	inv.addBlocks(d.Blocks)
}

func (inv *Inventory) addBlocks(blocks []Block) {
	for i := range blocks {
		inv.Counts[string(blocks[i].Type)]++
		inv.Total++
		inv.addBlocks(blocks[i].Children)
	}
}

// finalize computes the per-tier sums lazily before a report is produced.
func (inv *Inventory) finalize() {
	inv.tierSum = make(map[Tier]int)
	for typ, n := range inv.Counts {
		inv.tierSum[tierFor(typ)] += n
	}
}

// tierFor maps a (possibly post-Build) type name to a tier. BlockUnsupported is
// always long-tail; otherwise consult the tier table.
func tierFor(typ string) Tier {
	if typ == string(BlockUnsupported) {
		return TierUnknown
	}
	if t, ok := tierOf[typ]; ok {
		return t
	}
	return TierUnknown
}

// Report renders the inventory as a human-readable frequency table plus a
// coverage verdict (e.g. "T1 covers 94% of blocks seen; long tail: ..."). This
// output drives G2/G3 triage (SPEC §12.4).
func (inv *Inventory) Report() string {
	inv.finalize()
	var b strings.Builder

	fmt.Fprintf(&b, "Block inventory — %d block(s) across %d page(s)\n\n", inv.Total, inv.Pages)

	// Frequency table, sorted by count desc then name for stable output.
	type row struct {
		typ  string
		n    int
		tier Tier
	}
	rows := make([]row, 0, len(inv.Counts))
	for typ, n := range inv.Counts {
		rows = append(rows, row{typ, n, tierFor(typ)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].typ < rows[j].typ
	})

	fmt.Fprintf(&b, "%-22s %6s  %-9s %s\n", "TYPE", "COUNT", "TIER", "PCT")
	for _, r := range rows {
		pct := 0.0
		if inv.Total > 0 {
			pct = 100 * float64(r.n) / float64(inv.Total)
		}
		fmt.Fprintf(&b, "%-22s %6d  %-9s %5.1f%%\n", r.typ, r.n, r.tier, pct)
	}

	// Coverage verdict.
	b.WriteString("\nCoverage:\n")
	for _, t := range []Tier{Tier1, Tier2, Tier3, TierUnknown} {
		n := inv.tierSum[t]
		pct := 0.0
		if inv.Total > 0 {
			pct = 100 * float64(n) / float64(inv.Total)
		}
		label := t.String()
		if t == TierUnknown {
			label = "long-tail"
		}
		fmt.Fprintf(&b, "  %-9s %6d  %5.1f%%\n", label, n, pct)
	}

	// The headline verdict: what the shipped reader (G2 = T1) covers, and the
	// remaining long tail spelled out.
	t1pct := 0.0
	if inv.Total > 0 {
		t1pct = 100 * float64(inv.tierSum[Tier1]) / float64(inv.Total)
	}
	fmt.Fprintf(&b, "\nVerdict: T1 covers %.1f%% of blocks seen.", t1pct)
	if tail := inv.longTail(); tail != "" {
		fmt.Fprintf(&b, " Long tail: %s.", tail)
	} else {
		b.WriteString(" No long tail.")
	}
	b.WriteString("\n")
	return b.String()
}

// longTail formats the unhandled (TierUnknown / unsupported) types as
// "synced_block×3, equation×1", sorted by count desc.
func (inv *Inventory) longTail() string {
	type row struct {
		typ string
		n   int
	}
	var rows []row
	for typ, n := range inv.Counts {
		if tierFor(typ) == TierUnknown {
			rows = append(rows, row{typ, n})
		}
	}
	if len(rows) == 0 {
		return ""
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].typ < rows[j].typ
	})
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s×%d", r.typ, r.n))
	}
	return strings.Join(parts, ", ")
}
