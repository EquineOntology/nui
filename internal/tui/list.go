package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/notion"
	"github.com/EQuineOntology/nui/internal/platform"
	"github.com/EQuineOntology/nui/internal/render"
)

// list.go is the search/list view: the native fzf replacement (SPEC §8, G2
// deliverable 8). A textinput search box (prompt `notion ❯`) drives a debounced
// Search; an empty query shows recents. A results list on the left supports
// arrow selection; a live preview pane on the right renders the highlighted
// result through the SAME doc.Layout + render.Paint pipeline as the reader (no
// second renderer — SPEC §8). ctrl-o opens in the browser, ctrl-y copies the URL.

// searchDebounce is the window a keystroke waits before its Search fires, so a
// fast typist issues one request per pause, not per key (SPEC §8: ~200ms).
const searchDebounce = 200 * time.Millisecond

// listModel is the list-mode sub-model. It owns the textinput, the result rows,
// the selection cursor, and the laid-out preview for the current selection. The
// generation tokens (searchGen/debounceGen/previewGen) discard stale async
// replies (see cmds.go).
type listModel struct {
	input textinput.Model

	results []notion.Result
	cursor  int // index into results of the highlighted row

	width  int
	height int

	// preview holds the laid-out Document for the highlighted result, plus the
	// id it was built for (to ignore a stale previewResultMsg) and a loading flag.
	// previewDoc keeps the source Document so a resize can re-lay-out at the new
	// pane width without a refetch (the laid-out Lines are width-specific).
	preview     *doc.Rendered
	previewDoc  *doc.Document
	previewID   string
	previewLoad bool

	// status is a transient one-line message (errors, copy/open confirmations).
	status string

	// generation counters: incremented when a new request supersedes the prior
	// one so a late reply for an old query/selection is dropped on receipt.
	searchGen          int
	debounceGen        int
	previewGen         int
	previewDebounceGen int

	theme *render.Theme

	// lopts/headings are the layout options + per-level heading styles pushed in
	// from the Model's settings (Model.applySettings). layoutPreview reads them so
	// the preview matches the reader.
	lopts    doc.LayoutOpts
	headings []render.HeadingStyle
}

// newListModel builds the list sub-model with the search box focused and the
// prompt set. Size is applied later via setSize once the WindowSizeMsg arrives.
func newListModel() listModel {
	ti := textinput.New()
	ti.Prompt = "notion ❯ "
	ti.Placeholder = "search (empty = recents)"
	ti.Focus()
	return listModel{
		input: ti,
		theme: render.DefaultTheme(),
	}
}

// setSize records the terminal size and sizes the search box. The list/preview
// split is computed at render time from these.
func (l *listModel) setSize(w, h int) {
	l.width = w
	l.height = h
	if w > 0 {
		l.input.Width = w - lipgloss.Width(l.input.Prompt) - 2
	}
}

// updateList handles list-mode messages: keystrokes (selection, open, actions,
// quit), debounce/search/preview replies, and the textinput's own updates. It
// returns the root Model so transitions to reader mode happen here.
func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.listKey(msg)

	case debounceMsg:
		// Only the latest keystroke's debounce fires a Search.
		if msg.gen != m.list.debounceGen {
			return m, nil
		}
		m.list.searchGen++
		return m, m.searchCmd(msg.query)

	case searchResultMsg:
		if msg.gen != m.list.searchGen {
			return m, nil // superseded by a newer query
		}
		return m.applySearchResults(msg)

	case previewDebounceMsg:
		// Only the latest selection's debounce fires a preview fetch.
		if msg.gen != m.list.previewDebounceGen || msg.id != m.list.previewID {
			return m, nil
		}
		m.list.previewGen++
		return m, m.previewCmd(msg.id)

	case previewResultMsg:
		if msg.gen != m.list.previewGen || msg.id != m.list.previewID {
			return m, nil // selection moved on; drop stale preview
		}
		m.list.previewLoad = false
		if msg.err != nil {
			m.list.preview = nil
			m.list.previewDoc = nil
			m.list.status = "preview: " + msg.err.Error()
			return m, nil
		}
		m.list.status = "" // a successful preview clears a prior error note
		m.list.previewDoc = msg.doc
		m.list.preview = m.layoutPreview(msg.doc)
		return m, nil

	case actionMsg:
		if msg.err != nil {
			m.list.status = msg.err.Error()
		} else {
			m.list.status = msg.note
		}
		return m, nil
	}

	// Fall through to the textinput (cursor blink, paste, etc.).
	var cmd tea.Cmd
	m.list.input, cmd = m.list.input.Update(msg)
	return m, cmd
}

// listKey handles a keypress in list mode. Navigation/open/action keys are
// intercepted; everything else feeds the textinput and (on a value change)
// schedules a debounced reload.
func (m Model) listKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// From the list, esc with an empty query quits; otherwise it clears.
		if m.list.input.Value() == "" {
			m.quitting = true
			return m, tea.Quit
		}
		m.list.input.SetValue("")
		return m.scheduleReload()

	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "up", "ctrl+p":
		return m.moveSelection(-1)
	case "down", "ctrl+n":
		return m.moveSelection(1)

	case "enter":
		return m.openSelected()

	case "ctrl+o":
		return m.openInBrowser()
	case "ctrl+y":
		return m.copyURL()
	}

	// A printable/edit key: let the textinput consume it, then if the value
	// changed schedule a debounced reload.
	prev := m.list.input.Value()
	var cmd tea.Cmd
	m.list.input, cmd = m.list.input.Update(msg)
	if m.list.input.Value() != prev {
		mm, reloadCmd := m.scheduleReload()
		return mm, tea.Batch(cmd, reloadCmd)
	}
	return m, cmd
}

// scheduleReload bumps the debounce generation and arms a debounce timer for the
// current query. The actual Search only fires when the debounce elapses with no
// newer keystroke (see debounceMsg handling).
func (m Model) scheduleReload() (tea.Model, tea.Cmd) {
	m.list.debounceGen++
	return m, m.debounceCmd(m.list.input.Value())
}

// applySearchResults installs a fresh result set, clamps the selection, and
// kicks off a preview fetch for the new top selection.
func (m Model) applySearchResults(msg searchResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.list.status = "search: " + msg.err.Error()
		m.list.results = nil
		m.list.cursor = 0
		m.list.preview = nil
		return m, nil
	}
	m.list.status = ""
	m.list.results = msg.results
	if m.list.cursor >= len(m.list.results) {
		m.list.cursor = 0
	}
	return m, m.selectionPreviewCmd()
}

// moveSelection moves the highlight by delta, clamps to bounds, and refreshes
// the preview for the newly selected row.
func (m Model) moveSelection(delta int) (tea.Model, tea.Cmd) {
	if len(m.list.results) == 0 {
		return m, nil
	}
	m.list.cursor += delta
	if m.list.cursor < 0 {
		m.list.cursor = 0
	}
	if m.list.cursor >= len(m.list.results) {
		m.list.cursor = len(m.list.results) - 1
	}
	return m, m.selectionPreviewCmd()
}

// selectionPreviewCmd starts a preview fetch for the current selection, bumping
// the preview generation so an in-flight preview for the prior row is dropped.
// Selecting a database shows a stopgap note (view rendering is G4) rather than
// fetching, since /v1/views is out of scope here.
//
// It takes a POINTER receiver on purpose: it mutates the preview generation /
// id / loading flag on the model, and callers invoke it on their addressable
// local copy (return m, m.selectionPreviewCmd()) so the mutation survives into
// the value they return. A value receiver here silently dropped every preview
// (the gen/id bump was lost), leaving the preview pane blank.
func (m *Model) selectionPreviewCmd() tea.Cmd {
	sel, ok := m.list.selected()
	if !ok {
		m.list.preview = nil
		m.list.previewDoc = nil
		m.list.previewID = ""
		return nil
	}
	m.list.previewID = sel.ID
	if sel.Kind == "database" {
		// DB views are G4; show a placeholder instead of fetching a page that 404s.
		m.list.previewGen++ // discard any in-flight page preview from a prior row
		m.list.preview = m.databasePlaceholder(sel)
		m.list.previewDoc = nil
		m.list.previewLoad = false
		return nil
	}
	// Debounce the fetch so holding ↑/↓ doesn't fire one per tick: arm a timer at
	// the current selection generation; the actual previewCmd fires only when the
	// selection settles (previewDebounceMsg with a matching gen).
	m.list.previewDebounceGen++
	m.list.preview = nil
	m.list.previewDoc = nil
	m.list.previewLoad = true
	return m.previewDebounceCmd(sel.ID)
}

// openSelected transitions to reader mode for the highlighted page. A database
// row stays in list mode with a note (the G4 view path is not built here).
func (m Model) openSelected() (tea.Model, tea.Cmd) {
	sel, ok := m.list.selected()
	if !ok {
		return m, nil
	}
	if sel.Kind == "database" {
		m.list.status = "database view rendering: G4"
		return m, nil
	}
	// Recording happens on a SUCCESSFUL doc load in the reader (see docResultMsg),
	// not here, so a page that fails to fetch is not falsely remembered.
	m.mode = modeReader
	m.reader.begin(sel.ID, sel.Title, resultURL(sel))
	m.reader.setSize(m.width, m.height)
	m.reader.docGen++
	return m, m.openReaderCmd(sel.ID, sel.Title, resultURL(sel))
}

// openInBrowser fires the platform open for the selection's URL.
func (m Model) openInBrowser() (tea.Model, tea.Cmd) {
	sel, ok := m.list.selected()
	if !ok {
		return m, nil
	}
	url := resultURL(sel)
	return m, func() tea.Msg {
		if err := platform.OpenURL(url); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{note: "opened in browser"}
	}
}

// copyURL fires the platform clipboard copy for the selection's URL.
func (m Model) copyURL() (tea.Model, tea.Cmd) {
	sel, ok := m.list.selected()
	if !ok {
		return m, nil
	}
	url := resultURL(sel)
	return m, func() tea.Msg {
		if err := platform.CopyURL(url); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{note: "copied URL"}
	}
}

// layoutPreview runs the shared Layout pipeline at the preview pane width. It is
// deliberately the SAME doc.Layout + render.Renderer the reader uses, just at a
// narrower width (SPEC §8: do not fork a second renderer).
func (m Model) layoutPreview(d *doc.Document) *doc.Rendered {
	w := m.previewWidth()
	if w <= 0 || d == nil {
		return nil
	}
	r := render.NewRenderer(m.list.theme, m.list.lopts, m.list.headings)
	return doc.Layout(d, w, r, m.list.lopts)
}

// databasePlaceholder builds a tiny one-line Rendered noting that database view
// rendering arrives in G4 (the stopgap allowed by the G2 spec).
func (m Model) databasePlaceholder(sel notion.Result) *doc.Rendered {
	line := doc.Line{
		Segments: []doc.Segment{
			{Text: "🗂  " + sel.Title},
			{Text: "  —  database view rendering: G4", Style: doc.Style{Italic: true, Fg: m.list.theme.Foreground("gray")}},
		},
	}
	return &doc.Rendered{Lines: []doc.Line{line}}
}

// selected returns the highlighted result, ok=false when the list is empty.
func (l listModel) selected() (notion.Result, bool) {
	if l.cursor < 0 || l.cursor >= len(l.results) {
		return notion.Result{}, false
	}
	return l.results[l.cursor], true
}

// previewWidth is the inner width of the preview pane (right column minus its
// border/padding). Derived from the list/preview split in view().
func (m Model) previewWidth() int {
	_, prevW := m.listSplit()
	w := prevW - 2 // padding inside the pane border
	if w < 1 {
		return 0
	}
	return w
}

// listSplit returns the column widths of the results list (left) and the preview
// pane (right). The preview gets the larger share; both have sane minimums so a
// narrow terminal still renders.
func (m Model) listSplit() (listW, prevW int) {
	if m.width < 40 {
		// Too narrow for a side-by-side split: list only, no preview.
		return m.width, 0
	}
	listW = m.width * 2 / 5
	if listW < 20 {
		listW = 20
	}
	if listW > 50 {
		listW = 50
	}
	prevW = m.width - listW - 1 // 1 col separator
	return listW, prevW
}

// resultURL returns the result's URL, falling back to a constructed notion.so
// URL from the id when the API did not give one (so open/copy always have a
// target).
func resultURL(r notion.Result) string {
	if r.URL != "" {
		return r.URL
	}
	return constructURL(r.ID)
}

// constructURL builds a canonical notion.so URL from a bare page id (dashes
// stripped), used when no API URL is available (e.g. `nui read <id>`).
func constructURL(id string) string {
	if id == "" {
		return ""
	}
	return "https://www.notion.so/" + strings.ReplaceAll(id, "-", "")
}
