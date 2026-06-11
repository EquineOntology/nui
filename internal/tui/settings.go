package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/EQuineOntology/nui/internal/config"
	"github.com/EQuineOntology/nui/internal/render"
)

// settings.go is the settings popup overlay: a centered panel to toggle indent
// and pick a per-heading color + underline, with a live sample on each heading
// row so the effect is visible without leaving the popup. Changes apply to the
// reader/preview immediately (Model.applySettings) and persist on close.

// settingRowKind distinguishes the indent toggle from a per-heading-level row.
type settingRowKind int

const (
	rowIndent settingRowKind = iota
	rowHeading
)

type settingRow struct {
	kind  settingRowKind
	level int // 1-based heading level (rowHeading only)
}

// settingRows is the fixed row order: the indent toggle, then one row per heading
// level (color + underline edited on the same row).
func settingRows() []settingRow {
	rows := []settingRow{{kind: rowIndent}}
	for lvl := 1; lvl <= config.MaxHeadingLevel; lvl++ {
		rows = append(rows, settingRow{kind: rowHeading, level: lvl})
	}
	return rows
}

// updateSettings handles input while the settings popup is open: navigation, the
// per-row edits, and esc/s to save+close. left/right cycle the color (or toggle
// indent); [ / ] cycle the underline; space toggles indent. Every edit applies
// live via applySettings so the reader behind reflects it on close.
func (m Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := settingRows()
	cur := rows[m.settingsCursor]
	apply := false
	switch msg.String() {
	case "esc", "q", "s", "enter":
		m.ovl = overlayNone
		_ = config.Save(m.settings) // a failed save must not crash the reader
		return m, nil
	case "up", "k", "shift+tab":
		if m.settingsCursor > 0 {
			m.settingsCursor--
		}
	case "down", "j", "tab":
		if m.settingsCursor < len(rows)-1 {
			m.settingsCursor++
		}
	case "left", "h":
		if cur.kind == rowIndent {
			m.settings.Indent = false
		} else {
			i := cur.level - 1
			m.settings.Headings[i].Color = config.PrevColor(m.settings.Headings[i].Color)
		}
		apply = true
	case "right", "l":
		if cur.kind == rowIndent {
			m.settings.Indent = true
		} else {
			i := cur.level - 1
			m.settings.Headings[i].Color = config.NextColor(m.settings.Headings[i].Color)
		}
		apply = true
	case " ":
		if cur.kind == rowIndent {
			m.settings.Indent = !m.settings.Indent
			apply = true
		}
	case "[":
		if cur.kind == rowHeading {
			i := cur.level - 1
			m.settings.Headings[i].Underline = config.PrevUnderline(m.settings.Headings[i].Underline)
			apply = true
		}
	case "]":
		if cur.kind == rowHeading {
			i := cur.level - 1
			m.settings.Headings[i].Underline = config.NextUnderline(m.settings.Headings[i].Underline)
			apply = true
		}
	}
	if apply {
		m.applySettings()
	}
	return m, nil
}

// settingsView renders the centered settings popup. Each heading row shows the
// level, its color name, its underline (or "none"), and a live sample painted in
// that style, so the choice is visible in place.
func (m Model) settingsView() string {
	theme := render.DefaultTheme()
	rows := settingRows()

	var b strings.Builder
	b.WriteString(settingsTitleStyle.Render("Settings"))
	b.WriteString("\n\n")
	for i, row := range rows {
		pointer := "  "
		if i == m.settingsCursor {
			pointer = selStyle.Render("▸ ")
		}
		b.WriteString(pointer + m.settingRowText(row, theme))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑/↓ row · ←/→ color · [ ] underline · space indent · esc save"))

	box := settingsBoxStyle.Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// settingRowText is one rendered popup row (without the cursor pointer).
func (m Model) settingRowText(row settingRow, theme *render.Theme) string {
	switch row.kind {
	case rowIndent:
		state := "off"
		if m.settings.Indent {
			state = "on"
		}
		return fmt.Sprintf("%-34s %s", "Indent body under headings", selStyle.Render(state))
	default:
		i := row.level - 1
		h := render.HeadingStyle{}
		if i >= 0 && i < len(m.settings.Headings) {
			h = m.settings.Headings[i]
		}
		color := h.Color
		if color == "" {
			color = "default"
		}
		underline := h.Underline
		if underline == "" {
			underline = "none"
		}
		label := fmt.Sprintf("H%d", row.level)
		// Live sample: the level name + a short underline, painted in this style.
		st := lipgloss.NewStyle().Bold(true)
		if fg := theme.Foreground(h.Color); fg != "" {
			st = st.Foreground(lipgloss.Color(fg))
		}
		sample := st.Render("Aa")
		if h.Underline != "" {
			sample += " " + st.Render(strings.Repeat(h.Underline, 4))
		}
		return fmt.Sprintf("%-5s color %-8s underline %-5s  %s", label, color, underline, sample)
	}
}

var (
	settingsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#337ea9"))
	settingsBoxStyle   = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#9b9a97")).
				Padding(0, 2)
)
