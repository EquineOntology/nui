package tui

import (
	"context"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/notion"
	"github.com/EQuineOntology/nui/internal/state"
)

// source.go is the production DataSource: a thin adapter over notion.Client and
// the state recents store (SPEC §7/§8). It is the only place tui touches notion
// + state, so the Update logic stays testable against a fake DataSource (the Elm
// win, SPEC §9). It imports notion + state but NOT the pure layers' internals;
// doc types flow through unchanged.

// clientSource implements DataSource over a *notion.Client and the on-disk
// recents store.
type clientSource struct {
	client *notion.Client
}

// NewClientSource builds the production DataSource around a real notion.Client.
func NewClientSource(client *notion.Client) DataSource {
	return &clientSource{client: client}
}

// Search delegates to the client's title-reranked search (SPEC §7). An empty
// query returns recents instead (the empty-query rule), so the list view does
// not special-case it.
func (s *clientSource) Search(ctx context.Context, query string) ([]notion.Result, error) {
	if query == "" {
		return s.Recents(), nil
	}
	return s.client.Search(ctx, query)
}

// Document fetches+builds a page through the client's SWR cache (SPEC §7).
func (s *clientSource) Document(ctx context.Context, id string) (*doc.Document, error) {
	return s.client.Document(ctx, id)
}

// Recents reads the recents store and maps it to the Result shape the list view
// consumes. A read error degrades to an empty list (never panic; the list just
// shows "no recents").
func (s *clientSource) Recents() []notion.Result {
	recents, err := state.Load()
	if err != nil {
		return nil
	}
	out := make([]notion.Result, 0, len(recents))
	for _, r := range recents {
		out = append(out, notion.Result{
			ID:    r.ID,
			Title: r.Title,
			URL:   r.URL,
			Kind:  r.Kind,
		})
	}
	return out
}

// Record persists an opened page to the recents store (the bash _record port via
// state.Record). A write error is swallowed: failing to remember a page must not
// break reading it.
func (s *clientSource) Record(r notion.Result) {
	_ = state.Record(state.Recent{
		ID:    r.ID,
		Title: r.Title,
		URL:   r.URL,
		Kind:  r.Kind,
	})
}
