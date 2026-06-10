package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/notion"
)

// fakeSource is an in-memory DataSource for the model tests: no network, no TTY.
// It records what was opened (Record) so transition tests can assert recents are
// updated, and returns canned search/document results.
type fakeSource struct {
	searchResults []notion.Result
	searchErr     error
	docByID       map[string]*doc.Document
	docErr        error
	recents       []notion.Result
	recorded      []notion.Result
	docCalls      int // number of Document fetches (to assert debounce/dedup)
}

func (f *fakeSource) Search(_ context.Context, _ string) ([]notion.Result, error) {
	return f.searchResults, f.searchErr
}

func (f *fakeSource) Document(_ context.Context, id string) (*doc.Document, error) {
	f.docCalls++
	if f.docErr != nil {
		return nil, f.docErr
	}
	if d, ok := f.docByID[id]; ok {
		return d, nil
	}
	return &doc.Document{ID: id, Title: "doc " + id}, nil
}

func (f *fakeSource) Recents() []notion.Result { return f.recents }

func (f *fakeSource) Record(r notion.Result) { f.recorded = append(f.recorded, r) }

// sized returns a model that has received its initial WindowSizeMsg, so views
// and body-height math behave as in a real terminal.
func sized(m Model, w, h int) Model {
	mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return mm.(Model)
}

// withResults returns a list-mode model populated with the given results and the
// cursor at the top (as if a search had just resolved).
func withResults(src DataSource, results []notion.Result) Model {
	m := sized(New(src, "", ""), 100, 30)
	mm, _ := m.Update(searchResultMsg{gen: m.list.searchGen, results: results})
	return mm.(Model)
}

func TestPreviewIsDebounced(t *testing.T) {
	src := &fakeSource{}
	// Three page results; withResults installs them and schedules the first
	// preview debounce (it does not fetch).
	m := withResults(src, []notion.Result{
		{ID: "p1", Kind: "page"},
		{ID: "p2", Kind: "page"},
		{ID: "p3", Kind: "page"},
	})
	if src.docCalls != 0 {
		t.Fatalf("installing results should NOT fetch a preview (debounced), got %d calls", src.docCalls)
	}

	// Simulate holding ↓ through the list: every move re-arms the debounce, none
	// fires a fetch.
	for i := 0; i < 2; i++ {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = mm.(Model)
	}
	if src.docCalls != 0 {
		t.Fatalf("moving the selection must not fetch per tick, got %d calls", src.docCalls)
	}

	// The debounce settles: feed the latest previewDebounceMsg; it should issue
	// exactly one preview fetch for the current selection (p3).
	wantID := m.list.results[m.list.cursor].ID
	mm, cmd := m.Update(previewDebounceMsg{gen: m.list.previewDebounceGen, id: wantID})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("a settled debounce should return a preview fetch command")
	}
	if msg := cmd(); msg != nil {
		mm, _ = m.Update(msg) // deliver the previewResultMsg
		m = mm.(Model)
	}
	if src.docCalls != 1 {
		t.Fatalf("settled debounce should fetch exactly once, got %d", src.docCalls)
	}

	// A STALE debounce (older generation) must be ignored — no extra fetch.
	_, cmd = m.Update(previewDebounceMsg{gen: m.list.previewDebounceGen - 1, id: wantID})
	if cmd != nil {
		t.Fatal("a superseded debounce must not fetch")
	}
	if src.docCalls != 1 {
		t.Fatalf("stale debounce must not fetch, got %d total", src.docCalls)
	}
}

func TestEnterOpensReader(t *testing.T) {
	src := &fakeSource{}
	m := withResults(src, []notion.Result{
		{ID: "page-1", Title: "First", URL: "https://n/1", Kind: "page"},
		{ID: "page-2", Title: "Second", Kind: "page"},
	})

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := mm.(Model)

	if got.mode != modeReader {
		t.Fatalf("mode = %v, want reader", got.mode)
	}
	if got.reader.id != "page-1" {
		t.Fatalf("reader.id = %q, want page-1", got.reader.id)
	}
	if !got.reader.loading {
		t.Fatalf("reader should be loading after open")
	}
	// Recording happens on a successful doc load, not on the keypress, so the bad
	// path (fetch fails) is not falsely remembered. Feed the doc result and assert.
	if len(src.recorded) != 0 {
		t.Fatalf("nothing should be recorded before the doc loads, got %+v", src.recorded)
	}
	mm, _ = got.Update(docResultMsg{gen: got.reader.docGen, id: "page-1", title: "First", doc: &doc.Document{ID: "page-1", Title: "First"}})
	got = mm.(Model)
	if len(src.recorded) != 1 || src.recorded[0].ID != "page-1" {
		t.Fatalf("expected page-1 recorded to recents on successful load, got %+v", src.recorded)
	}
}

func TestEnterOnDatabaseStaysInList(t *testing.T) {
	src := &fakeSource{}
	m := withResults(src, []notion.Result{
		{ID: "db-1", Title: "A Database", Kind: "database"},
	})

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := mm.(Model)

	if got.mode != modeList {
		t.Fatalf("database enter should stay in list, got mode %v", got.mode)
	}
	if got.list.status == "" {
		t.Fatalf("expected a G4 stopgap status note for database open")
	}
	if len(src.recorded) != 0 {
		t.Fatalf("database open should not record a recent, got %+v", src.recorded)
	}
}

func TestEscFromReaderReturnsToList(t *testing.T) {
	src := &fakeSource{}
	m := withResults(src, []notion.Result{{ID: "page-1", Title: "First", Kind: "page"}})
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := mm.(Model)

	if got.mode != modeList {
		t.Fatalf("esc from reader should return to list, got %v", got.mode)
	}
	// The list selection should survive the round trip.
	if got.list.cursor != 0 || len(got.list.results) != 1 {
		t.Fatalf("list state not preserved across reader round trip")
	}
}

func TestQFromReaderReturnsToList(t *testing.T) {
	src := &fakeSource{}
	m := withResults(src, []notion.Result{{ID: "page-1", Kind: "page"}})
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if mm.(Model).mode != modeList {
		t.Fatalf("q from reader should return to list")
	}
}

func TestSearchResultsMsgUpdatesList(t *testing.T) {
	src := &fakeSource{}
	m := sized(New(src, "", ""), 100, 30)

	results := []notion.Result{
		{ID: "a", Title: "Alpha", Kind: "page"},
		{ID: "b", Title: "Beta", Kind: "page"},
	}
	mm, _ := m.Update(searchResultMsg{gen: m.list.searchGen, results: results})
	got := mm.(Model)

	if len(got.list.results) != 2 {
		t.Fatalf("results not installed: %d", len(got.list.results))
	}
	if got.list.results[0].Title != "Alpha" {
		t.Fatalf("first result = %q", got.list.results[0].Title)
	}
}

func TestStaleSearchResultsDropped(t *testing.T) {
	src := &fakeSource{}
	m := withResults(src, []notion.Result{{ID: "current", Title: "Current", Kind: "page"}})

	// A reply tagged with an older generation must be ignored.
	mm, _ := m.Update(searchResultMsg{gen: m.list.searchGen - 1, results: []notion.Result{
		{ID: "stale", Title: "Stale", Kind: "page"},
	}})
	got := mm.(Model)

	if len(got.list.results) != 1 || got.list.results[0].ID != "current" {
		t.Fatalf("stale search reply should have been dropped, got %+v", got.list.results)
	}
}

func TestMoveSelectionClamps(t *testing.T) {
	src := &fakeSource{}
	m := withResults(src, []notion.Result{
		{ID: "a", Kind: "page"}, {ID: "b", Kind: "page"},
	})

	// Up at the top stays at 0.
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if mm.(Model).list.cursor != 0 {
		t.Fatalf("cursor should clamp at 0")
	}
	// Down moves to 1.
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyDown})
	if mm.(Model).list.cursor != 1 {
		t.Fatalf("cursor should be 1")
	}
	// Down again clamps at the last index.
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyDown})
	if mm.(Model).list.cursor != 1 {
		t.Fatalf("cursor should clamp at last index 1, got %d", mm.(Model).list.cursor)
	}
}

func TestEmptyQueryEscQuits(t *testing.T) {
	src := &fakeSource{}
	m := withResults(src, nil)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatalf("esc on empty query should issue a quit command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("quit command should produce a message")
	} else if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("esc on empty query should quit, got %T", msg)
	}
}

func TestDocResultEntersReaderContent(t *testing.T) {
	src := &fakeSource{}
	m := withResults(src, []notion.Result{{ID: "page-1", Title: "T", Kind: "page"}})
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	d := &doc.Document{
		ID:    "page-1",
		Title: "Loaded Title",
		Blocks: []doc.Block{
			{ID: "h", Type: doc.BlockHeading1, HeadingLvl: 1, RichText: []doc.RichText{{Text: "Intro"}}},
			{ID: "p", Type: doc.BlockParagraph, RichText: []doc.RichText{{Text: "body"}}},
		},
	}
	mm, _ = m.Update(docResultMsg{gen: m.reader.docGen, id: "page-1", title: "Loaded Title", doc: d})
	got := mm.(Model)

	if got.reader.loading {
		t.Fatalf("reader should not be loading after doc arrives")
	}
	if got.reader.rendered == nil || len(got.reader.rendered.Lines) == 0 {
		t.Fatalf("reader should have laid-out lines")
	}
	if got.reader.title != "Loaded Title" {
		t.Fatalf("reader title = %q", got.reader.title)
	}
}

func TestStaleDocResultDropped(t *testing.T) {
	src := &fakeSource{}
	m := withResults(src, []notion.Result{{ID: "page-1", Kind: "page"}})
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	// A doc reply for a different id (navigated away) must be ignored.
	mm, _ = m.Update(docResultMsg{gen: m.reader.docGen, id: "other", doc: &doc.Document{ID: "other"}})
	if mm.(Model).reader.doc != nil {
		t.Fatalf("doc reply for a non-current id should be dropped")
	}
}
