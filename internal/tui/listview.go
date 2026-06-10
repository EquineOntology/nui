package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/EQuineOntology/nui/internal/render"
)

// listview.go assembles the list mode's frame: the search box, the results
// column beside the live preview pane, and the footer. It is split from list.go
// (the Update logic) so the rendering — which needs the whole Model for size and
// the shared paint pipeline — reads top-to-bottom. ANSI for the preview comes
// only from render.Paint (the paint rule); the chrome uses lipgloss directly,
// which is allowed in tui (the paint rule binds render/, not tui/).

// chrome styles for the list frame. These live in tui (not render/theme.go):
// the paint rule forbids render/ from .Render()ing, but tui owns its own chrome.
var (
	selStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#337ea9"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9b9a97"))
	dbBadge     = lipgloss.NewStyle().Foreground(lipgloss.Color("#9065b0"))
	sepStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9b9a97"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#d9730d"))
)

// listView renders the entire list screen. Layout: row 0 is the search box; the
// last row is the footer; the band between is the results list (left) and the
// preview pane (right).
func (m Model) listView() string {
	listW, prevW := m.listSplit()
	bodyH := m.height - 2 // search box row + footer row
	if bodyH < 1 {
		bodyH = 1
	}

	left := m.renderResults(listW, bodyH)
	var body string
	if prevW > 0 {
		right := m.renderPreview(prevW, bodyH)
		sep := strings.TrimRight(strings.Repeat(sepStyle.Render("│")+"\n", bodyH), "\n")
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)
	} else {
		body = left
	}

	return strings.Join([]string{
		m.list.input.View(),
		body,
		m.listFooter(),
	}, "\n")
}

// renderResults draws the results column to a fixed width/height: one row per
// result, the highlighted row reverse/bold, a kind badge for databases, titles
// truncated to the column. Empty results show a hint.
func (m Model) renderResults(width, height int) string {
	if len(m.list.results) == 0 {
		hint := "no matches"
		if m.list.input.Value() == "" {
			hint = "no recents yet"
		}
		return padBlock(dimStyle.Render(hint), width, height)
	}

	// Scroll the window so the cursor stays visible.
	start := 0
	if m.list.cursor >= height {
		start = m.list.cursor - height + 1
	}
	end := start + height
	if end > len(m.list.results) {
		end = len(m.list.results)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		r := m.list.results[i]
		marker := "  "
		if i == m.list.cursor {
			marker = "▌ "
		}
		title := r.Title
		if r.Kind == "database" {
			title = dbBadge.Render("[db] ") + title
		}
		line := marker + title
		line = truncate(line, width)
		if i == m.list.cursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return padBlock(b.String(), width, height)
}

// renderPreview draws the live preview pane: the laid-out, painted Document of
// the highlighted result, sliced to the pane height. A loading or error state
// shows a placeholder. Painting goes through render.Paint — the only ANSI
// producer (SPEC §6).
func (m Model) renderPreview(width, height int) string {
	if m.list.previewLoad {
		return padBlock(dimStyle.Render("loading…"), width, height)
	}
	if m.list.preview == nil {
		return padBlock(dimStyle.Render(" "), width, height)
	}
	lines := m.list.preview.Lines
	if len(lines) > height {
		lines = lines[:height]
	}
	painted := make([]string, 0, len(lines))
	for _, ln := range lines {
		painted = append(painted, truncateANSI(render.Paint(ln, previewPaintOpts()), width))
	}
	return padBlock(strings.Join(painted, "\n"), width, height)
}

// listFooter renders the key hints (or the transient status message when set).
func (m Model) listFooter() string {
	if m.list.status != "" {
		return statusStyle.Render(m.list.status)
	}
	hints := "enter read · ctrl-o browser · ctrl-y copy · ↑/↓ move · esc clear/quit"
	return dimStyle.Render(truncate(hints, m.width))
}

// previewPaintOpts configures painting for the preview pane: hyperlinks OFF (it
// is a narrow glance pane, not an interaction surface — OSC-8 escapes there are
// just noise the user can't click meaningfully); color on.
func previewPaintOpts() render.PaintOpts {
	return render.PaintOpts{Hyperlinks: false}
}

// readerPaintOpts configures painting for the full reader: hyperlinks ON, so
// links are clickable in terminals that support OSC-8 (G2 acceptance). The
// terminal that can't decode them will show them inertly; that's the standard
// trade-off and matches the less-based reader's clickable links.
func readerPaintOpts() render.PaintOpts {
	return render.PaintOpts{Hyperlinks: true}
}

// padBlock pads/clips a (possibly multi-line) block to exactly width×height so
// the horizontal join keeps clean columns. Each line is padded to width on the
// display-width of its visible text; the block is padded/truncated to height.
func padBlock(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, height)
	for i := 0; i < height; i++ {
		if i < len(lines) {
			out = append(out, padLine(lines[i], width))
		} else {
			out = append(out, strings.Repeat(" ", width))
		}
	}
	return strings.Join(out, "\n")
}

// padLine right-pads a line to width display columns, accounting for ANSI escape
// sequences (which have zero display width).
func padLine(s string, width int) string {
	w := visibleWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// truncate clips a (possibly ANSI-styled) string to width display columns. It is
// ANSI-aware so chrome strings carrying lipgloss escapes are measured by their
// visible width, not byte length.
func truncate(s string, width int) string {
	if visibleWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "")
}

// truncateANSI clips a painted (ANSI) line to width display columns, preserving
// the escape sequences so styling survives the cut.
func truncateANSI(s string, width int) string {
	if visibleWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "")
}

// visibleWidth returns the display-column width of a string ignoring ANSI escape
// sequences (which occupy zero columns).
func visibleWidth(s string) int {
	if !strings.ContainsRune(s, '\x1b') {
		return runewidth.StringWidth(s)
	}
	return ansi.StringWidth(s)
}
