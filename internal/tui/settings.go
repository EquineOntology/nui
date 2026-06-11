package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/EQuineOntology/nui/internal/config"
	"github.com/EQuineOntology/nui/internal/render"
)

// settings.go is the settings popup overlay: a centered panel listing one
// editable value per row — the indent toggle, then a Color and an Underline row
// under each heading level. Every row changes the same way (←/→ or space), and
// enter/esc both save and close. Changes apply to the reader/preview immediately
// (Model.applySettings) and persist on close.

// settingItemKind is the kind of editable value a row holds.
type settingItemKind int

const (
	itemIndent settingItemKind = iota
	itemColor
	itemUnderline
)

// settingItem is one selectable/editable row. level applies to color/underline.
type settingItem struct {
	kind  settingItemKind
	level int // 1-based heading level (color/underline rows)
}

// settingItems is the fixed order of editable rows: the indent toggle, then a
// Color and Underline row per heading level. (Group headers shown in the view
// are not items — the cursor only lands on editable rows.)
func settingItems() []settingItem {
	items := []settingItem{{kind: itemIndent}}
	for lvl := 1; lvl <= config.MaxHeadingLevel; lvl++ {
		items = append(items, settingItem{kind: itemColor, level: lvl})
		items = append(items, settingItem{kind: itemUnderline, level: lvl})
	}
	return items
}

// updateSettings handles popup input: ↑/↓ move between rows; ←/→ and space change
// the selected row's value (every row the same way); enter/esc save and close.
func (m Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := settingItems()
	it := items[m.settingsCursor]
	apply := false
	switch msg.String() {
	case "esc", "enter", "q", "s":
		m.ovl = overlayNone
		_ = config.Save(m.settings) // a failed save must not crash the reader
		return m, nil
	case "up", "k", "shift+tab":
		if m.settingsCursor > 0 {
			m.settingsCursor--
		}
	case "down", "j", "tab":
		if m.settingsCursor < len(items)-1 {
			m.settingsCursor++
		}
	case "left", "h":
		m.changeSetting(it, -1)
		apply = true
	case "right", "l", " ":
		m.changeSetting(it, +1)
		apply = true
	}
	if apply {
		m.applySettings()
	}
	return m, nil
}

// changeSetting cycles the value of one row by dir (-1 prev, +1 next). The indent
// toggle flips for either direction.
func (m *Model) changeSetting(it settingItem, dir int) {
	switch it.kind {
	case itemIndent:
		m.settings.Indent = !m.settings.Indent
	case itemColor:
		i := it.level - 1
		if dir < 0 {
			m.settings.Headings[i].Color = config.PrevColor(m.settings.Headings[i].Color)
		} else {
			m.settings.Headings[i].Color = config.NextColor(m.settings.Headings[i].Color)
		}
	case itemUnderline:
		i := it.level - 1
		if dir < 0 {
			m.settings.Headings[i].Underline = config.PrevUnderline(m.settings.Headings[i].Underline)
		} else {
			m.settings.Headings[i].Underline = config.NextUnderline(m.settings.Headings[i].Underline)
		}
	}
}

// settingsView renders the centered settings popup: a group header per heading
// level, then its Color and Underline rows, with the selected row pointed at and
// a live sample painted in that level's style.
func (m Model) settingsView() string {
	theme := render.DefaultTheme()
	items := settingItems()

	var b strings.Builder
	b.WriteString(settingsTitleStyle.Render("Settings"))
	b.WriteString("\n\n")

	level := 0
	for i, it := range items {
		// A group header introduces each heading level's rows.
		if it.kind == itemColor && it.level != level {
			level = it.level
			b.WriteString("  " + settingsGroupStyle.Render(fmt.Sprintf("Heading %d", it.level)))
			b.WriteString("\n")
		}
		pointer := "   "
		if i == m.settingsCursor {
			pointer = selStyle.Render(" ▸ ")
		}
		b.WriteString(pointer + m.settingItemText(it, theme))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑/↓ move · ←/→ or space change · enter/esc save"))

	box := settingsBoxStyle.Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// settingItemText renders one row (without the pointer): a label, the current
// value, and — for heading rows — a live sample painted in that level's style.
func (m Model) settingItemText(it settingItem, theme *render.Theme) string {
	switch it.kind {
	case itemIndent:
		// Top-level item: its label aligns with the "Heading N" group labels;
		// the Color/Underline rows below are sub-indented under their heading.
		state := "off"
		if m.settings.Indent {
			state = "on"
		}
		return fmt.Sprintf("%-24s %s", "Progressive indent", settingsValueStyle.Render(state))
	default:
		h := render.HeadingStyle{}
		if i := it.level - 1; i >= 0 && i < len(m.settings.Headings) {
			h = m.settings.Headings[i]
		}
		st := lipgloss.NewStyle().Bold(true)
		if fg := theme.Foreground(h.Color); fg != "" {
			st = st.Foreground(lipgloss.Color(fg))
		}
		if it.kind == itemColor {
			name := h.Color
			if name == "" {
				name = "default"
			}
			// Two-space sub-indent so Color/Underline nest under "Heading N"; the
			// label width keeps the value column aligned with the indent row.
			return fmt.Sprintf("  %-22s %-9s %s", "Color", name, st.Render("Aa"))
		}
		// underline
		val := h.Underline
		sample := ""
		if val == "" {
			val = "none"
		} else {
			sample = st.Render(strings.Repeat(h.Underline, 6))
		}
		return fmt.Sprintf("  %-22s %-9s %s", "Underline", val, sample)
	}
}

var (
	settingsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#337ea9"))
	settingsGroupStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9b9a97"))
	settingsValueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#337ea9"))
	settingsBoxStyle   = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#9b9a97")).
				Padding(0, 2)
)
