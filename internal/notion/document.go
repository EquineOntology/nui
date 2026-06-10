package notion

import (
	"context"
	"encoding/json"

	"github.com/EQuineOntology/nui/internal/doc"
)

// cachedPage is the on-disk SWR cache payload for one page id: the page
// metadata plus the fully-assembled raw block tree, as raw JSON. Caching the raw
// shapes (not the built Document, not laid-out Lines) means a terminal resize
// re-layouts for free and the IR can change without invalidating the cache
// (SPEC §7).
type cachedPage struct {
	Page   doc.RawPage    `json:"page"`
	Blocks []doc.RawBlock `json:"blocks"`
}

// Document fetches a page through the SWR cache and builds the typed Document
// IR. A cache hit returns immediately (and refreshes in the background if stale);
// a cold miss fetches the page metadata and the recursive block tree, both
// gated by the shared rate limiter, then caches the raw JSON.
//
// This is the single entry point the reader (G2) and the G1 dump/inventory
// commands use to turn an id into a Document.
func (c *Client) Document(ctx context.Context, id string) (*doc.Document, error) {
	raw, err := c.cachedDocumentJSON(ctx, id)
	if err != nil {
		return nil, err
	}
	var cp cachedPage
	if err := Decode(raw, &cp); err != nil {
		return nil, err
	}
	return doc.Build(cp.Page, cp.Blocks), nil
}

// RawDocumentJSON returns the cached raw JSON for a page (the cachedPage
// envelope). `nui dump --json` prints this so the bytes can be captured as a
// test fixture and re-decoded through the tolerant path.
func (c *Client) RawDocumentJSON(ctx context.Context, id string) ([]byte, error) {
	return c.cachedDocumentJSON(ctx, id)
}

// cachedDocumentJSON returns the raw cachedPage JSON for id via the SWR cache,
// fetching from ntn on a cold miss. When the client has no cache configured
// (e.g. a test client), it fetches directly.
func (c *Client) cachedDocumentJSON(ctx context.Context, id string) ([]byte, error) {
	if c.cache == nil {
		return c.fetchDocumentJSON(ctx, id)
	}
	return c.cache.Get(ctx, id, c.fetchDocumentJSON)
}

// fetchDocumentJSON performs the cold-miss fetch: page metadata + recursive
// block tree, marshaled into the cachedPage envelope.
func (c *Client) fetchDocumentJSON(ctx context.Context, id string) ([]byte, error) {
	page, err := c.Page(ctx, id)
	if err != nil {
		return nil, err
	}
	blocks, err := c.Blocks(ctx, id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(cachedPage{Page: page, Blocks: blocks})
}
