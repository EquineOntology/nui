package state

import (
	"path/filepath"
	"testing"
)

// TestRecordAndLoad: a recorded page round-trips through the TSV store, and the
// XDG_STATE_HOME redirect keeps the test off the real recents file.
func TestRecordAndLoad(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := Record(Recent{ID: "id1", Title: "First", URL: "u1", Kind: "page"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "id1" || got[0].Title != "First" {
		t.Fatalf("loaded = %+v", got)
	}
}

// TestRecordDedupesAndFloats: re-recording an id moves it to the top without
// duplicating it (the bash _record dedupe).
func TestRecordDedupesAndFloats(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	_ = Record(Recent{ID: "a", Title: "A"})
	_ = Record(Recent{ID: "b", Title: "B"})
	_ = Record(Recent{ID: "a", Title: "A2"}) // re-touch a

	got, _ := Load()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries after dedupe, got %d: %+v", len(got), got)
	}
	if got[0].ID != "a" || got[0].Title != "A2" {
		t.Errorf("re-touched entry did not float with new title: %+v", got[0])
	}
	if got[1].ID != "b" {
		t.Errorf("second entry wrong: %+v", got[1])
	}
}

// TestRecordCaps: the list never exceeds RecentsMax.
func TestRecordCaps(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for i := 0; i < RecentsMax+10; i++ {
		_ = Record(Recent{ID: string(rune('A'+i%26)) + itoa(i)})
	}
	got, _ := Load()
	if len(got) > RecentsMax {
		t.Errorf("recents grew past cap: %d > %d", len(got), RecentsMax)
	}
}

// TestLoadMissingFileIsEmpty: a first run (no file) returns no error and no rows.
func TestLoadMissingFileIsEmpty(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	got, err := Load()
	if err != nil {
		t.Fatalf("Load on missing file errored: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty recents, got %+v", got)
	}
}

// TestSanitizeFieldFlattensTabs: a title containing tabs/newlines must not
// corrupt the TSV columns.
func TestSanitizeFieldFlattensTabs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_ = Record(Recent{ID: "id1", Title: "a\tb\nc", URL: "u"})
	got, _ := Load()
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].Title != "a b c" {
		t.Errorf("tabs/newlines not flattened: %q", got[0].Title)
	}
	if got[0].URL != "u" {
		t.Errorf("url column corrupted: %q", got[0].URL)
	}
}

// TestIDs extracts ids in order.
func TestIDs(t *testing.T) {
	ids := IDs([]Recent{{ID: "x"}, {ID: "y"}})
	if len(ids) != 2 || ids[0] != "x" || ids[1] != "y" {
		t.Errorf("IDs = %v", ids)
	}
}

// pathDerivation guards the XDG-vs-HOME fallback used by recentsPath (smoke).
func TestRecentsPathHonorsXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	want := filepath.Join(dir, "nui", "recents.tsv")
	if got := recentsPath(); got != want {
		t.Errorf("recentsPath = %q, want %q", got, want)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
