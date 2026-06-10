package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"golang.org/x/sync/errgroup"

	"github.com/EQuineOntology/nui/internal/doc"
)

// blockPageSize is the per-request child page size. 100 is Notion's maximum, so
// shallow pages resolve in a single round trip — minimizing calls against the
// ~3 req/s ceiling (SPEC §7, §12.1).
const blockPageSize = 100

// blockList is one /v1/blocks/{id}/children response page. Results are decoded
// into doc.RawBlock (whose custom UnmarshalJSON captures the verbatim bytes and
// the type payload map).
type blockList struct {
	Results    []doc.RawBlock `json:"results"`
	HasMore    bool           `json:"has_more"`
	NextCursor string         `json:"next_cursor"`
}

// Blocks fetches the full, recursively-assembled block tree for a page id
// (SPEC §7). It:
//
//   - pages through next_cursor for the top-level children;
//   - for every block with has_children, fetches that block's children;
//   - bounds BOTH the live goroutine count AND concurrent ntn calls by the
//     worker limit (c.children, default 4) — all calls also gated by the single
//     shared rate.Limiter at the Notion ceiling;
//   - honors ctx cancellation throughout (the first error cancels siblings).
//
// The returned tree's Children fields are populated; the raw API never returns
// them inline. Decoding is tolerant (control-char sanitization) via Client.API.
func (c *Client) Blocks(ctx context.Context, id string) ([]doc.RawBlock, error) {
	// Own the cancel so a synchronous-path error (resolve below) can stop the
	// goroutines already spawned on the group — that error never flows through
	// errgroup, so it can't cancel gctx on its own.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.childWorkers())

	root, err := c.listChildren(gctx, id)
	if err != nil {
		return nil, err
	}
	rerr := c.resolve(gctx, g, root)
	if rerr != nil {
		cancel() // stop in-flight goroutines via the derived ctx
	}
	if werr := g.Wait(); werr != nil && rerr == nil {
		return nil, werr
	}
	if rerr != nil {
		return nil, rerr
	}
	return root, nil
}

// resolve fills in the Children of every has_children node in blocks. Each
// node's child-fetch is scheduled on the errgroup via TryGo when a worker slot
// is free, and run INLINE (synchronously, on the current goroutine) when the
// group is at its limit. That keeps the live goroutine count bounded by the
// group limit — protecting the low-memory budget that is nui's whole reason to
// exist (SPEC §1) — while staying deadlock-free: a goroutine never blocks
// waiting for a slot, it just does the work itself. Concurrent ntn calls
// likewise never meaningfully exceed the limit, so the rate ceiling holds.
func (c *Client) resolve(ctx context.Context, g *errgroup.Group, blocks []doc.RawBlock) error {
	for i := range blocks {
		if !blocks[i].HasChildren {
			continue
		}
		i := i
		fn := func() error {
			kids, err := c.listChildren(ctx, blocks[i].ID)
			if err != nil {
				return err
			}
			blocks[i].Children = kids
			return c.resolve(ctx, g, kids)
		}
		// TryGo runs fn on a new goroutine if under the limit; otherwise we run
		// it inline (no new goroutine, no blocking) so progress is guaranteed.
		if !g.TryGo(fn) {
			if err := fn(); err != nil {
				return err
			}
		}
	}
	return nil
}

// listChildren pages through a single block's direct children via next_cursor,
// concatenating every page. Each page is one rate-limited ntn call; concurrency
// is bounded by the errgroup worker limit in Blocks/resolve, not here.
func (c *Client) listChildren(ctx context.Context, id string) ([]doc.RawBlock, error) {
	var all []doc.RawBlock
	cursor := ""
	for {
		path := fmt.Sprintf("/v1/blocks/%s/children?page_size=%d", url.PathEscape(id), blockPageSize)
		if cursor != "" {
			path += "&start_cursor=" + url.QueryEscape(cursor)
		}
		var page blockList
		if err := c.API(ctx, path, "", &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

// childWorkers returns the configured semaphore size, defaulting when unset.
func (c *Client) childWorkers() int {
	if c.children > 0 {
		return c.children
	}
	return defaultChildWorkers
}

// BlocksRaw returns the recursively-assembled block tree as raw JSON, suitable
// for the SWR cache and for `nui dump --json` fixture capture. It wraps Blocks
// and marshals the tree (including the resolved Children) so a cached entry can
// be re-decoded back into the same tree.
func (c *Client) BlocksRaw(ctx context.Context, id string) ([]byte, error) {
	blocks, err := c.Blocks(ctx, id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(blocks)
}
