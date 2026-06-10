package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/notion"
	"github.com/EQuineOntology/nui/internal/platform"
	"github.com/EQuineOntology/nui/internal/render"
)

// reader.go is the CUSTOM viewport (SPEC §8, G2 deliverable 9): NOT
// bubbles/viewport, because that cannot pin a sticky region. The reader reserves
// the top row(s) for the enclosing heading (computed from Rendered.Outline +
// scroll offset), slices Lines by offset for the body, and paints visible lines
// through render.Paint (the sole ANSI producer). Full sticky-heading polish is
// G3, but the mechanism is prototyped here so it is not retrofitted (SPEC risk #2).

// readerModel is the reader-mode sub-model. It owns the laid-out document, the
// scroll offset, the page metadata, and the loading/error state. The body is
// re-laid-out on resize (cheap, pure, no network — SPEC §6/§8).
type readerModel struct {
	id    string
	title string
	url   string

	doc      *doc.Document
	rendered *doc.Rendered
	offset   int // index of the first body line shown

	width  int
	height int

	loading bool
	errMsg  string
	status  string // transient action message (copy/open)

	theme *render.Theme

	// docGen discards a stale docResultMsg if the user navigated away and back.
	docGen int
}

// stickyRows is the number of top rows the reader reserves for the pinned
// enclosing heading. One row in G2; the model supports more (G3 may pin a
// heading chain).
const stickyRows = 1

// newReaderModel builds an empty reader sub-model with the default theme.
func newReaderModel() readerModel {
	return readerModel{theme: render.DefaultTheme()}
}

// begin resets the reader for a new page open: clears the prior document, sets
// the known metadata, and enters the loading state until the docResultMsg lands.
func (r *readerModel) begin(id, title, url string) {
	r.id = id
	r.title = title
	r.url = url
	r.doc = nil
	r.rendered = nil
	r.offset = 0
	r.loading = true
	r.errMsg = ""
	r.status = ""
}

// setSize records the size and re-lays-out the document at the new body width so
// a resize reflows without a network fetch (the raw JSON is cached; Layout is
// pure). The offset is clamped to the new line count.
func (r *readerModel) setSize(w, h int) {
	r.width = w
	r.height = h
	r.relayout()
}

// relayout runs the shared Layout pipeline at the current body width. Called on
// open and on resize. Pure: no I/O.
func (r *readerModel) relayout() {
	if r.doc == nil || r.width <= 0 {
		return
	}
	rd := render.NewRenderer(r.theme, doc.LayoutOpts{ShowProps: true})
	r.rendered = doc.Layout(r.doc, r.bodyWidth(), rd, doc.LayoutOpts{ShowProps: true})
	r.clampOffset()
}

// bodyWidth is the text width available to the document body (full width; the
// reader uses the whole terminal width for content).
func (r *readerModel) bodyWidth() int {
	if r.width < 1 {
		return 1
	}
	return r.width
}

// bodyHeight is the number of body rows: terminal height minus the sticky
// heading rows and the status line.
func (r *readerModel) bodyHeight() int {
	h := r.height - stickyRows - 1 // sticky region + status line
	if h < 1 {
		return 1
	}
	return h
}

// maxOffset is the largest valid scroll offset (so the last screen of content
// sits at the bottom without scrolling past the end).
func (r *readerModel) maxOffset() int {
	if r.rendered == nil {
		return 0
	}
	m := len(r.rendered.Lines) - r.bodyHeight()
	if m < 0 {
		return 0
	}
	return m
}

// clampOffset keeps offset within [0, maxOffset].
func (r *readerModel) clampOffset() {
	if r.offset < 0 {
		r.offset = 0
	}
	if mo := r.maxOffset(); r.offset > mo {
		r.offset = mo
	}
}

// updateReader handles reader-mode messages: navigation keys, the document
// fetch reply, and platform-action replies. Esc/q returns to the list.
func (m Model) updateReader(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.readerKey(msg)

	case docResultMsg:
		if msg.gen != m.reader.docGen || msg.id != m.reader.id {
			return m, nil // navigated away; drop stale fetch
		}
		m.reader.loading = false
		if msg.err != nil {
			m.reader.errMsg = msg.err.Error()
			return m, nil
		}
		m.reader.doc = msg.doc
		if msg.title != "" {
			m.reader.title = msg.title
		}
		if m.reader.doc != nil && m.reader.title == "" {
			m.reader.title = m.reader.doc.Title
		}
		m.reader.relayout()
		// Record the open to recents on a SUCCESSFUL load (SPEC §8: opening a page
		// records it). Doing it here rather than at the enter keypress covers the
		// direct `nui read <id>` path too, and avoids recording a page that failed
		// to fetch. Record is idempotent (dedupes by id), so a re-open is harmless.
		m.src.Record(notion.Result{
			ID:    m.reader.id,
			Title: m.reader.title,
			URL:   m.reader.url,
			Kind:  "page",
		})
		return m, nil

	case actionMsg:
		if msg.err != nil {
			m.reader.status = msg.err.Error()
		} else {
			m.reader.status = msg.note
		}
		return m, nil
	}
	return m, nil
}

// readerKey handles a keypress in reader mode: scrolling and the return-to-list
// transition. Unknown keys are ignored (the reader is read-only).
func (m Model) readerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		// Return to the list. The list sub-model is preserved (its results +
		// selection survive), so esc lands back where the user was.
		m.mode = modeList
		// Bump the reader doc generation so a still-in-flight fetch for the page
		// we just left is dropped on receipt, and actually CANCEL its context so
		// the fetch goroutine stops rather than running to completion.
		m.reader.docGen++
		m.fetch.cancel(axisDoc)
		return m, nil

	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "up", "k":
		m.reader.scroll(-1)
	case "down", "j":
		m.reader.scroll(1)
	case "pgup", "b":
		m.reader.scroll(-m.reader.bodyHeight())
	case "pgdown", "f", " ":
		m.reader.scroll(m.reader.bodyHeight())
	case "g", "home":
		m.reader.offset = 0
	case "G", "end":
		m.reader.offset = m.reader.maxOffset()

	case "ctrl+o":
		url := m.reader.url
		return m, func() tea.Msg {
			if err := platform.OpenURL(url); err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{note: "opened in browser"}
		}
	case "ctrl+y":
		url := m.reader.url
		return m, func() tea.Msg {
			if err := platform.CopyURL(url); err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{note: "copied URL"}
		}
	}
	return m, nil
}

// scroll moves the offset by delta lines and clamps.
func (r *readerModel) scroll(delta int) {
	r.offset += delta
	r.clampOffset()
}

// stickyHeading computes the heading to pin: the last heading that has scrolled
// OFF the top of the body, i.e. whose line index is strictly above the first
// visible line (LineIdx < offset). The strict comparison is deliberate — a
// heading sitting exactly at the top of the viewport (LineIdx == offset) is
// already drawn as the first body line, so pinning it too would draw it twice.
// Pure function of the Outline and offset, unit-testable in isolation (SPEC §9).
// Returns ok=false when no heading has scrolled off yet (top of doc).
func stickyHeading(outline []doc.Heading, offset int) (doc.Heading, bool) {
	var cur doc.Heading
	found := false
	for _, h := range outline {
		if h.LineIdx < offset {
			cur = h
			found = true
		} else {
			break // outline is in document (line) order; at/past the offset
		}
	}
	return cur, found
}

// view renders the reader: the pinned sticky heading row(s), the painted body
// window, and the status line (title + scroll %).
func (r readerModel) view() string {
	if r.width <= 0 || r.height <= 0 {
		return ""
	}
	if r.loading {
		return r.centered("loading " + r.titleOrID() + " …")
	}
	if r.errMsg != "" {
		return r.centered("error: " + r.errMsg + "\n\nesc to go back")
	}
	if r.rendered == nil || len(r.rendered.Lines) == 0 {
		return r.centered("(empty page)\n\nesc to go back")
	}

	var b strings.Builder
	b.WriteString(r.stickyView())
	b.WriteByte('\n')
	b.WriteString(r.bodyView())
	b.WriteByte('\n')
	b.WriteString(r.statusView())
	return b.String()
}

// stickyView renders the pinned heading row. When no heading encloses the
// current offset, a dim rule keeps the layout stable (the row is always
// reserved, so the body height never jumps).
func (r readerModel) stickyView() string {
	h, ok := stickyHeading(r.rendered.Outline, r.offset)
	style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#337ea9"))
	if !ok {
		return dimStyle.Render(strings.Repeat("─", r.width))
	}
	prefix := strings.Repeat("#", h.Level) + " "
	return truncate(style.Render(prefix+h.Text), r.width)
}

// bodyView paints the visible window of body lines, padded to the body height so
// the status line stays anchored at the bottom.
func (r readerModel) bodyView() string {
	h := r.bodyHeight()
	end := r.offset + h
	if end > len(r.rendered.Lines) {
		end = len(r.rendered.Lines)
	}
	out := make([]string, 0, h)
	for i := r.offset; i < end; i++ {
		out = append(out, truncateANSI(render.Paint(r.rendered.Lines[i], readerPaintOpts()), r.width))
	}
	for len(out) < h {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// statusView renders the bottom status line: the title and the scroll percent
// (or the transient action status when one is set).
func (r readerModel) statusView() string {
	if r.status != "" {
		return statusStyle.Render(truncate(r.status, r.width))
	}
	left := r.titleOrID()
	right := r.scrollPercent() + "  esc back · ctrl-o browser · ctrl-y copy"
	gap := r.width - visibleWidth(left) - visibleWidth(right)
	if gap < 1 {
		gap = 1
	}
	line := selStyle.Render(left) + strings.Repeat(" ", gap) + dimStyle.Render(right)
	return truncate(line, r.width)
}

// scrollPercent reports how far through the document the bottom of the viewport
// is, as a percentage. A document shorter than the viewport reads "All".
func (r readerModel) scrollPercent() string {
	if r.rendered == nil {
		return ""
	}
	total := len(r.rendered.Lines)
	if total <= r.bodyHeight() {
		return "All"
	}
	if r.offset <= 0 {
		return "Top"
	}
	if r.offset >= r.maxOffset() {
		return "Bot"
	}
	pct := r.offset * 100 / r.maxOffset()
	return strconv.Itoa(pct) + "%"
}

// titleOrID returns the page title, falling back to the id when the title is not
// yet known (during the loading window before the fetch resolves).
func (r readerModel) titleOrID() string {
	if r.title != "" {
		return r.title
	}
	return r.id
}

// centered renders a single message centered in the viewport (loading / error /
// empty states). It is intentionally simple — full polish is not the point here.
func (r readerModel) centered(msg string) string {
	return lipgloss.Place(r.width, r.height, lipgloss.Center, lipgloss.Center, dimStyle.Render(msg))
}
