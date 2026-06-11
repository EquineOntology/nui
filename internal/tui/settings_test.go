package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestReaderOpensSettings asserts 's' in the reader opens the settings popup.
func TestReaderOpensSettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // never touch the real config
	src := &fakeSource{}
	m := openedReader(t, src)
	mm, _ := m.Update(keyRunes("s"))
	if mm.(Model).ovl != overlaySettings {
		t.Fatal("'s' in the reader should open the settings overlay")
	}
}

// TestSettingsOverlayTogglesAndCycles drives the popup: space toggles indent,
// right cycles a heading color, ] cycles its underline, and esc closes.
func TestSettingsOverlayTogglesAndCycles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	src := &fakeSource{}
	m := sized(New(src, "", ""), 100, 30)
	m.ovl = overlaySettings
	m.settingsCursor = 0 // the indent row

	indentBefore := m.settings.Indent
	mm, _ := m.Update(keyRunes(" ")) // space toggles indent
	m = mm.(Model)
	if m.settings.Indent == indentBefore {
		t.Fatal("space should toggle indent")
	}

	// Row 1 is "H1 Color": right cycles it.
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(Model)
	colorBefore := m.settings.Headings[0].Color
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = mm.(Model)
	if m.settings.Headings[0].Color == colorBefore {
		t.Fatalf("right should cycle H1 color, stayed %q", colorBefore)
	}
	// Row 2 is "H1 Underline": space cycles it (same control as every other row).
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(Model)
	underlineBefore := m.settings.Headings[0].Underline
	mm, _ = m.Update(keyRunes(" "))
	m = mm.(Model)
	if m.settings.Headings[0].Underline == underlineBefore {
		t.Fatalf("space should cycle H1 underline, stayed %q", underlineBefore)
	}

	// esc closes the popup (and persists to the temp config dir).
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.(Model).ovl != overlayNone {
		t.Fatal("esc should close the settings overlay")
	}
}

// TestSettingsIndentDrivesLayout asserts toggling indent in the popup flips the
// reader's layout option (the cascade on/off).
func TestSettingsIndentDrivesLayout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	src := &fakeSource{}
	m := sized(New(src, "", ""), 100, 30)
	m.ovl = overlaySettings
	m.settingsCursor = 0

	// Force indent off, then on, and check it propagates to the reader's lopts.
	m.settings.Indent = true
	m.applySettings()
	if !m.reader.lopts.Nest {
		t.Fatal("indent on should set reader Nest")
	}
	mm, _ := m.Update(keyRunes(" ")) // toggle off
	m = mm.(Model)
	if m.reader.lopts.Nest {
		t.Fatal("toggling indent off should clear reader Nest")
	}
}
