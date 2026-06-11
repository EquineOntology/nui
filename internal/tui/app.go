// Package tui is nui's Bubble Tea front-end: a self-contained terminal app with
// a search/list mode and an owned-viewport reader mode (SPEC §8, revised
// 2026-06-10). It is the native equivalent of the bash fzf+less `nui`.
//
// Architecture (Elm): app.go holds the root Model with mode ∈ {list, reader} and
// an overlay field reserved for G3 ({none} now). ALL Notion I/O is async via
// tea.Cmd (a goroutine produces a tea.Msg); the UI never blocks on a fetch.
// Navigating away cancels the in-flight fetch's context.
//
// The TUI depends on notion/doc/render/state through a small DataSource seam
// (defined here) so the Update logic is unit-testable without a real ntn binary
// or a TTY — the whole point of the Elm architecture (SPEC §9).
package tui

import (
	"context"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/notion"
)

// layoutOpts builds the layout options shared by the reader and preview: page
// properties shown. Progressive heading-depth indentation (Nest) is OFF by
// default — headings are set off by their per-level underline rule instead, so
// the body stays flush-left; set NUI_NEST=1 to bring the indentation cascade
// back for comparison.
func layoutOpts() doc.LayoutOpts {
	return doc.LayoutOpts{ShowProps: true, Nest: os.Getenv("NUI_NEST") == "1"}
}

// mode is the top-level screen the app shows. overlay (G3) is intentionally a
// separate axis so the reader/list keep working while an overlay is layered on.
type mode int

const (
	modeList mode = iota
	modeReader
)

// overlay is the G3 overlay axis (command palette, in-doc search, outline,
// minimap). G2 only ever holds overlayNone; the field exists now so the model
// shape does not change when G3 lands (SPEC §8).
type overlay int

const (
	overlayNone overlay = iota
)

// DataSource is the seam between the TUI and the network/disk world. The
// production implementation (clientSource) wraps a *notion.Client + state; tests
// inject a fake so Update is exercised without a TTY or ntn (SPEC §9). Every
// method honors ctx so navigating away cancels in-flight work.
type DataSource interface {
	// Search runs a title-reranked Notion search. An empty query yields recents
	// (the empty-query → state.recents fallback lives behind this seam).
	Search(ctx context.Context, query string) ([]notion.Result, error)
	// Document fetches+builds a page through the SWR cache.
	Document(ctx context.Context, id string) (*doc.Document, error)
	// Recents returns the recents list newest-first (the empty-query list).
	Recents() []notion.Result
	// Record notes that a page was opened, updating the recents store.
	Record(r notion.Result)
}

// Model is the root Bubble Tea model. It owns the shared DataSource, the two
// sub-models (list, reader), the terminal size, and the current mode/overlay.
// It is the single place I/O is dispatched as tea.Cmd; the sub-models return
// commands but never run goroutines themselves.
type Model struct {
	src    DataSource
	mode   mode
	ovl    overlay
	width  int
	height int

	// fetch cancels superseded in-flight fetches per axis. A pointer so it
	// survives the value-copies of Update (see fetch.go).
	fetch *fetchControl

	list   listModel
	reader readerModel

	// quitting records that the user asked to exit so View can blank the screen
	// on the final frame (avoids a flash of stale UI on quit).
	quitting bool

	// startReadID, when set, opens the reader directly on that id at launch
	// (`nui read <id>`), skipping the list (SPEC §8).
	startReadID string
}

// New builds the root model. seedQuery pre-fills the search box (empty for a
// bare `nui`); startReadID, when non-empty, launches straight into the reader
// for that page (`nui read <id>`).
func New(src DataSource, seedQuery, startReadID string) Model {
	m := Model{
		src:         src,
		mode:        modeList,
		ovl:         overlayNone,
		fetch:       newFetchControl(),
		startReadID: startReadID,
		list:        newListModel(),
		reader:      newReaderModel(),
	}
	if seedQuery != "" {
		m.list.input.SetValue(seedQuery)
	}
	if startReadID != "" {
		m.mode = modeReader
		m.reader.begin(startReadID, "", constructURL(startReadID))
	}
	return m
}

// Init kicks off the first command: either a direct read (`nui read <id>`) or
// the initial list load (recents for an empty seed, a search for a seeded
// query). textinput's blink cursor is also started.
func (m Model) Init() tea.Cmd {
	if m.startReadID != "" {
		return m.openReaderCmd(m.startReadID, "", constructURL(m.startReadID))
	}
	// Issue the initial load. An empty query resolves to recents inside
	// searchCmd (the empty-query rule), so there is one path. The seed query was
	// already set on the input in New.
	return tea.Batch(
		m.list.input.Cursor.BlinkCmd(),
		m.searchCmd(m.list.input.Value()),
	)
}

// Update is the root reducer. It handles the global keys and window-size, then
// routes to the active mode. All Notion I/O leaves here as a tea.Cmd; Update
// itself never blocks (SPEC §8).
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.onResize(msg)

	case tea.KeyMsg:
		// ctrl+c always quits, from any mode.
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
	}

	switch m.mode {
	case modeReader:
		return m.updateReader(msg)
	default:
		return m.updateList(msg)
	}
}

// onResize threads the new terminal size to both sub-models and re-runs the
// reader's Layout at the new width (resize reflows without a network hit, since
// the raw JSON is cached and Layout is pure — SPEC §6/§8).
func (m Model) onResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.list.setSize(msg.Width, msg.Height)
	m.reader.setSize(msg.Width, msg.Height)
	// Re-lay-out the preview at the new pane width too (the reader does this in
	// setSize; the preview holds Lines pre-wrapped to the old width). Pure + cached
	// — no network. A database placeholder has no source doc, so leave it as-is.
	if m.list.previewDoc != nil {
		m.list.preview = m.layoutPreview(m.list.previewDoc)
	}
	return m, nil
}

// View renders the active mode. A non-positive size means we have not received
// the initial WindowSizeMsg yet (or the terminal is degenerate); render nothing
// rather than dividing by zero or drawing garbage.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	switch m.mode {
	case modeReader:
		return m.reader.view()
	default:
		return m.listView()
	}
}

// Run starts the Bubble Tea program in the alt-screen with mouse cell-motion
// off (the reader owns scroll via keys). It is the single entry main.go calls.
func Run(src DataSource, seedQuery, startReadID string) error {
	p := tea.NewProgram(
		New(src, seedQuery, startReadID),
		tea.WithAltScreen(),
	)
	_, err := p.Run()
	return err
}
