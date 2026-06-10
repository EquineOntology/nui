package doc

import (
	"strings"
	"testing"
)

// TestInventoryTallies verifies the inventory counts blocks across the whole
// tree (including children) and classifies tiers + the long tail correctly.
func TestInventoryTallies(t *testing.T) {
	d := &Document{
		Blocks: []Block{
			{Type: BlockParagraph},
			{Type: BlockHeading1, Children: []Block{
				{Type: BlockParagraph},
				{Type: BlockUnsupported},
			}},
			{Type: BlockToggle, Children: []Block{
				{Type: BlockImage}, // T3
			}},
		},
	}
	inv := NewInventory()
	inv.AddDocument(d)

	// paragraph + heading_1 + (paragraph + unsupported) + toggle + image = 6.
	if inv.Total != 6 {
		t.Errorf("Total = %d, want 6 (incl. children)", inv.Total)
	}
	if inv.Pages != 1 {
		t.Errorf("Pages = %d, want 1", inv.Pages)
	}
	if inv.Counts["paragraph"] != 2 {
		t.Errorf("paragraph count = %d, want 2", inv.Counts["paragraph"])
	}
	if inv.Counts["unsupported"] != 1 {
		t.Errorf("unsupported count = %d, want 1", inv.Counts["unsupported"])
	}

	inv.finalize()
	// T1: paragraph×2 + heading_1×1 = 3; T2: toggle×1 = 1; T3: image×1 = 1;
	// long tail: unsupported×1 = 1.
	if inv.tierSum[Tier1] != 3 {
		t.Errorf("Tier1 sum = %d, want 3", inv.tierSum[Tier1])
	}
	if inv.tierSum[Tier2] != 1 {
		t.Errorf("Tier2 sum = %d, want 1", inv.tierSum[Tier2])
	}
	if inv.tierSum[Tier3] != 1 {
		t.Errorf("Tier3 sum = %d, want 1", inv.tierSum[Tier3])
	}
	if inv.tierSum[TierUnknown] != 1 {
		t.Errorf("long-tail sum = %d, want 1", inv.tierSum[TierUnknown])
	}
}

// TestInventoryReportLongTail checks the verdict line names the long tail with
// counts (the form that drives G2/G3 triage).
func TestInventoryReportLongTail(t *testing.T) {
	d := &Document{Blocks: []Block{
		{Type: BlockUnsupported},
		{Type: BlockUnsupported},
		{Type: BlockParagraph},
	}}
	inv := NewInventory()
	inv.AddDocument(d)
	report := inv.Report()

	if !strings.Contains(report, "unsupported×2") {
		t.Errorf("report missing long-tail count; got:\n%s", report)
	}
	if !strings.Contains(report, "T1 covers") {
		t.Errorf("report missing T1 coverage verdict; got:\n%s", report)
	}
}

// TestInventoryFromRealFixtures runs the inventory over the captured fixtures —
// a smoke test that AddDocument handles real trees and produces a non-empty
// verdict (the same path `nui inventory` drives).
func TestInventoryFromRealFixtures(t *testing.T) {
	inv := NewInventory()
	for _, name := range fixtures {
		cp := loadFixture(t, name)
		inv.AddDocument(Build(cp.Page, cp.Blocks))
	}
	if inv.Pages != len(fixtures) {
		t.Errorf("Pages = %d, want %d", inv.Pages, len(fixtures))
	}
	if inv.Total == 0 {
		t.Fatal("inventory tallied zero blocks across real fixtures")
	}
	report := inv.Report()
	if !strings.Contains(report, "Verdict:") {
		t.Errorf("report missing verdict line; got:\n%s", report)
	}
	t.Logf("real-fixture inventory:\n%s", report)
}
