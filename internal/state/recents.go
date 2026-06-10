// Package state holds nui's durable, on-disk state. Today that is the recents
// list — pages the user has opened, most-recent first, deduped and capped — a
// faithful port of the bash _record helper (a sqlite frecency store is a later
// roadmap item, SPEC §4). It imports nothing from notion/doc/tui; it is a small
// TSV store callers drive.
package state

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// RecentsMax caps the recents list length, matching the bash RECENTS_MAX.
const RecentsMax = 50

// Recent is one row of the recents list: id<TAB>title<TAB>url<TAB>kind, newest
// first. Kind is "page" or "database".
type Recent struct {
	ID    string
	Title string
	URL   string
	Kind  string
}

// recentsPath resolves the recents file location, honoring XDG_STATE_HOME then
// $HOME/.local/state, suffixed with nui/recents.tsv (SPEC §10). It mirrors the
// bash STATE_DIR/RECENTS derivation so the two front-ends share one list.
func recentsPath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(base, "nui", "recents.tsv")
}

// Load reads the recents list newest-first. A missing file is not an error — it
// returns an empty slice (the first-run case). Malformed lines are skipped
// rather than failing the whole load (never panic on unexpected input).
func Load() ([]Recent, error) {
	return loadFrom(recentsPath())
}

func loadFrom(path string) ([]Recent, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Recent
	sc := bufio.NewScanner(f)
	// Recents titles can be long; raise the line cap above bufio's 64KiB default.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 1 || fields[0] == "" {
			continue
		}
		r := Recent{ID: fields[0]}
		if len(fields) > 1 {
			r.Title = fields[1]
		}
		if len(fields) > 2 {
			r.URL = fields[2]
		}
		if len(fields) > 3 {
			r.Kind = fields[3]
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// IDs returns just the ids of the recents, newest first — the form inventory
// and the (later) list view consume.
func IDs(recents []Recent) []string {
	ids := make([]string, 0, len(recents))
	for _, r := range recents {
		ids = append(ids, r.ID)
	}
	return ids
}

// Record prepends r to the recents list, deduping by id and capping at
// RecentsMax — the bash _record port (atomic temp-file swap). It is read-only's
// one exception: opening a page is an explicit user action that updates state.
// Not exercised by the G1 read commands, but the durable store lives here so G2+
// have it.
func Record(r Recent) error {
	if r.ID == "" {
		return nil
	}
	if r.Kind == "" {
		r.Kind = "page"
	}
	path := recentsPath()
	existing, err := loadFrom(path)
	if err != nil {
		return err
	}

	merged := make([]Recent, 0, len(existing)+1)
	merged = append(merged, r)
	for _, e := range existing {
		if e.ID == r.ID {
			continue // dedupe: the new entry floats to the top
		}
		merged = append(merged, e)
	}
	if len(merged) > RecentsMax {
		merged = merged[:RecentsMax]
	}

	return writeAtomic(path, merged)
}

func writeAtomic(path string, recents []Recent) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".recents-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	w := bufio.NewWriter(tmp)
	for _, r := range recents {
		// Tabs/newlines in titles would corrupt the TSV; flatten them.
		title := sanitizeField(r.Title)
		url := sanitizeField(r.URL)
		kind := sanitizeField(r.Kind)
		if _, err := w.WriteString(r.ID + "\t" + title + "\t" + url + "\t" + kind + "\n"); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func sanitizeField(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}
