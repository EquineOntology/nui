package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/notion"
)

// cmds.go holds the async I/O seam: the tea.Msg types a fetch produces and the
// tea.Cmd constructors that run a fetch in a goroutine. Every command is the
// only place a goroutine is spawned (SPEC §8: all I/O async via tea.Cmd, the UI
// never blocks). Stale results are discarded by a monotonically increasing
// generation token (gen) carried on the request and echoed on the reply, so a
// fast typist's earlier in-flight search/preview cannot overwrite a newer one.

// searchResultMsg carries the outcome of a Search. gen lets Update drop a stale
// reply (an earlier query that resolved after a newer one was issued).
type searchResultMsg struct {
	gen     int
	query   string
	results []notion.Result
	err     error
}

// previewResultMsg carries a laid-out preview Document for the highlighted list
// item. gen ties it to the selection that requested it.
type previewResultMsg struct {
	gen int
	id  string
	doc *doc.Document
	err error
}

// docResultMsg carries the reader's full Document fetch result. gen ties it to
// the open request so a cancelled-then-reopened page cannot show stale content.
type docResultMsg struct {
	gen   int
	id    string
	title string
	url   string
	doc   *doc.Document
	err   error
}

// debounceMsg fires after the search debounce window; if its gen still matches
// the latest keystroke, Update issues the actual Search (SPEC §8: ~200ms
// debounce). Carrying gen makes the debounce self-cancelling without a timer
// handle to stop.
type debounceMsg struct {
	gen   int
	query string
}

// previewDebounceMsg fires after the preview debounce window; if its gen still
// matches the latest selection, Update issues the actual preview fetch. Mirrors
// the search debounce so holding ↑/↓ through results does not spawn a full
// fetch+Build+Layout(+chroma) per tick — only one after the selection settles.
type previewDebounceMsg struct {
	gen int
	id  string
}

// actionMsg carries the result of a fire-and-forget platform action (open URL /
// copy URL) so the status line can report success or failure.
type actionMsg struct {
	note string
	err  error
}

// searchCmd runs a Search for query at the current generation. An empty query
// resolves to recents via the DataSource (the empty-query → recents rule lives
// behind the seam). The fetch context comes from fetchControl.next(axisSearch),
// which cancels any prior in-flight search, so a fast typist's superseded query
// is not just dropped on receipt (gen) but actually CANCELLED (SPEC §7/§8).
func (m Model) searchCmd(query string) tea.Cmd {
	gen := m.list.searchGen
	src := m.src
	ctx := m.fetch.next(axisSearch)
	return func() tea.Msg {
		if query == "" {
			return searchResultMsg{gen: gen, query: query, results: src.Recents()}
		}
		res, err := src.Search(ctx, query)
		return searchResultMsg{gen: gen, query: query, results: res, err: err}
	}
}

// debounceCmd schedules a debounceMsg for the current keystroke generation after
// the debounce window. Bubble Tea's tea.Tick gives us the delay without owning a
// timer; the gen check on receipt makes superseded keystrokes no-ops.
func (m Model) debounceCmd(query string) tea.Cmd {
	gen := m.list.debounceGen
	return tea.Tick(searchDebounce, func(_ time.Time) tea.Msg {
		return debounceMsg{gen: gen, query: query}
	})
}

// previewDebounceCmd schedules a previewDebounceMsg for the current selection
// generation after the debounce window, mirroring debounceCmd for search. The
// gen check on receipt makes a superseded selection a no-op.
func (m Model) previewDebounceCmd(id string) tea.Cmd {
	gen := m.list.previewDebounceGen
	return tea.Tick(searchDebounce, func(_ time.Time) tea.Msg {
		return previewDebounceMsg{gen: gen, id: id}
	})
}

// previewCmd fetches+builds the Document for the highlighted list item so the
// preview pane can lay it out. It shares the SWR cache with the reader, so a
// highlight-then-open burst on the same id collapses to one fetch (cache
// single-flight, SPEC G2 carryover). gen ties it to the current selection.
func (m Model) previewCmd(id string) tea.Cmd {
	gen := m.list.previewGen
	src := m.src
	ctx := m.fetch.next(axisPreview)
	return func() tea.Msg {
		d, err := src.Document(ctx, id)
		return previewResultMsg{gen: gen, id: id, doc: d, err: err}
	}
}

// openReaderCmd fetches the Document for id and produces a docResultMsg to enter
// the reader. title/url are the known display fields (from the search Result or
// recents) so the reader has them before the body resolves; the fetch shares the
// cache with any preview already in flight for the same id.
func (m Model) openReaderCmd(id, title, url string) tea.Cmd {
	gen := m.reader.docGen
	src := m.src
	ctx := m.fetch.next(axisDoc)
	return func() tea.Msg {
		d, err := src.Document(ctx, id)
		return docResultMsg{gen: gen, id: id, title: title, url: url, doc: d, err: err}
	}
}
