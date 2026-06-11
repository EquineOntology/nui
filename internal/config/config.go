// Package config is nui's user-configurable presentation settings: whether the
// reader indents body under headings, and the per-level heading color/underline.
// It is edited live via the settings popup (internal/tui) and persisted to disk
// so choices stick across launches. The heading-style type lives in render (the
// renderer consumes it); config imports render for it and for the defaults, so
// there is a single source of truth and no import cycle (render never imports
// config).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/EQuineOntology/nui/internal/render"
)

// MaxHeadingLevel is how many heading levels the settings cover (H1..H6).
const MaxHeadingLevel = 6

// Settings is the persisted, user-editable presentation.
type Settings struct {
	Indent   bool                  `json:"indent"`   // indent body under enclosing-heading depth
	Headings []render.HeadingStyle `json:"headings"` // per level; index 0 = H1 .. MaxHeadingLevel-1
}

// Default is the built-in look: indent on, with the default per-level heading
// styles (distinct color + =/~/- underlines for the top three).
func Default() Settings {
	return Settings{Indent: true, Headings: render.DefaultHeadingStyles()}
}

// Colors is the cycle of Notion color names offered for a heading (settings popup
// ←/→). "default" is the terminal's own foreground.
var Colors = []string{"default", "gray", "brown", "orange", "yellow", "green", "blue", "purple", "pink", "red"}

// Underlines is the cycle of underline characters offered ("" = no underline).
var Underlines = []string{"", "=", "~", "-", "─", "·", "."}

// NextColor / PrevColor / NextUnderline / PrevUnderline cycle a value through its
// option list, wrapping around. An unknown current value starts the cycle at 0.
func NextColor(c string) string     { return cycle(Colors, c, 1) }
func PrevColor(c string) string     { return cycle(Colors, c, -1) }
func NextUnderline(u string) string { return cycle(Underlines, u, 1) }
func PrevUnderline(u string) string { return cycle(Underlines, u, -1) }

func cycle(list []string, cur string, dir int) string {
	idx := 0
	for i, v := range list {
		if v == cur {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(list)) % len(list)
	return list[idx]
}

// Path is the settings file location: $XDG_CONFIG_HOME/nui/settings.json (or
// ~/.config/nui/settings.json).
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "nui", "settings.json")
}

// Load reads the persisted settings, falling back to Default for a missing or
// unreadable/invalid file. Heading levels are normalized to exactly
// MaxHeadingLevel, backfilling any missing levels from the defaults so a file
// written by a different version stays usable.
func Load() Settings {
	def := Default()
	path := Path()
	if path == "" {
		return def
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	var loaded Settings
	if err := json.Unmarshal(data, &loaded); err != nil {
		return def
	}
	out := make([]render.HeadingStyle, MaxHeadingLevel)
	for i := range out {
		switch {
		case i < len(loaded.Headings):
			out[i] = loaded.Headings[i]
		case i < len(def.Headings):
			out[i] = def.Headings[i]
		}
	}
	return Settings{Indent: loaded.Indent, Headings: out}
}

// Save persists the settings atomically (temp file + rename). A missing config
// directory is created. Errors are returned for the caller to surface (or ignore
// — a failed save must never crash the reader).
func Save(s Settings) error {
	path := Path()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
