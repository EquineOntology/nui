package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EQuineOntology/nui/internal/render"
)

func TestDefaultHasAllLevels(t *testing.T) {
	d := Default()
	if !d.Indent {
		t.Error("default should indent")
	}
	if len(d.Headings) != MaxHeadingLevel {
		t.Fatalf("default headings = %d, want %d", len(d.Headings), MaxHeadingLevel)
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	s := Default()
	s.Indent = false
	s.Headings[0] = render.HeadingStyle{Color: "red", Underline: "─"}
	if err := Save(s); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nui", "settings.json")); err != nil {
		t.Fatalf("settings file not written: %v", err)
	}

	got := Load()
	if got.Indent {
		t.Error("indent=false not persisted")
	}
	if got.Headings[0].Color != "red" || got.Headings[0].Underline != "─" {
		t.Errorf("H1 style not persisted: %+v", got.Headings[0])
	}
	if len(got.Headings) != MaxHeadingLevel {
		t.Errorf("levels = %d, want %d", len(got.Headings), MaxHeadingLevel)
	}
}

func TestLoadMissingFileIsDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got := Load()
	if !got.Indent || len(got.Headings) != MaxHeadingLevel {
		t.Errorf("missing file should yield default, got %+v", got)
	}
}

func TestLoadBackfillsMissingLevels(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "nui"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file with only two heading levels must be backfilled to MaxHeadingLevel.
	body := `{"indent":true,"headings":[{"Color":"red","Underline":"="},{"Color":"blue","Underline":"~"}]}`
	if err := os.WriteFile(filepath.Join(dir, "nui", "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if len(got.Headings) != MaxHeadingLevel {
		t.Fatalf("backfill failed: %d levels", len(got.Headings))
	}
	if got.Headings[0].Color != "red" {
		t.Errorf("level 1 not loaded: %+v", got.Headings[0])
	}
	if def := render.DefaultHeadingStyles(); got.Headings[2] != def[2] {
		t.Errorf("level 3 not backfilled from default: %+v", got.Headings[2])
	}
}

func TestColorAndUnderlineCyclesWrap(t *testing.T) {
	if PrevColor(Colors[0]) != Colors[len(Colors)-1] {
		t.Error("PrevColor of first should wrap to last")
	}
	if NextColor(Colors[len(Colors)-1]) != Colors[0] {
		t.Error("NextColor of last should wrap to first")
	}
	if NextUnderline(Underlines[len(Underlines)-1]) != Underlines[0] {
		t.Error("NextUnderline of last should wrap to first")
	}
}
