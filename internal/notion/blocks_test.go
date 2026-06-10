package notion

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/EQuineOntology/nui/internal/doc"
)

// scriptedExec is a fake execFunc that answers /v1/blocks/{id}/children requests
// from an in-memory map of id -> response pages, recording every call. It lets
// the recursion + pagination + rate-limit tests run with no process and no
// network, the seam the whole package is built around (SPEC §9).
type scriptedExec struct {
	mu      sync.Mutex
	calls   []string            // full arg path of each ntn call, in order
	pages   map[string][]string // block id -> ordered JSON response pages
	cursors map[string]int      // block id -> next page index to serve
}

func (s *scriptedExec) fn(_ context.Context, _ string, args ...string) commander {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := args[1] // args = ["api", "<path>"]
	s.calls = append(s.calls, path)

	id := extractBlockID(path)
	idx := s.cursors[id]
	resp := `{"results":[],"has_more":false}`
	if pages := s.pages[id]; idx < len(pages) {
		resp = pages[idx]
		s.cursors[id] = idx + 1
	}
	return fakeCmd{out: []byte(resp)}
}

// extractBlockID pulls the {id} out of /v1/blocks/{id}/children?...
func extractBlockID(path string) string {
	const pre = "/v1/blocks/"
	rest := strings.TrimPrefix(path, pre)
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// childrenPage builds a children-list response page for a set of blocks.
func childrenPage(nextCursor string, blocks ...string) string {
	hasMore := "false"
	cursor := "null"
	if nextCursor != "" {
		hasMore = "true"
		cursor = fmt.Sprintf("%q", nextCursor)
	}
	return fmt.Sprintf(`{"results":[%s],"has_more":%s,"next_cursor":%s}`,
		strings.Join(blocks, ","), hasMore, cursor)
}

func leaf(id, typ string) string {
	return fmt.Sprintf(`{"object":"block","id":%q,"type":%q,"has_children":false,%q:{"rich_text":[{"type":"text","plain_text":"%s text"}]}}`, id, typ, typ, id)
}

func parent(id, typ string) string {
	return fmt.Sprintf(`{"object":"block","id":%q,"type":%q,"has_children":true,%q:{"rich_text":[{"type":"text","plain_text":"%s text"}]}}`, id, typ, typ, id)
}

func newScriptedClient(s *scriptedExec, lim limiter) *Client {
	if lim == nil {
		lim = rate.NewLimiter(rate.Inf, 1)
	}
	return &Client{
		exec:     s.fn,
		limiter:  lim,
		timeout:  0,
		children: 4,
	}
}

// TestBlocksPaginates asserts top-level children are assembled across multiple
// next_cursor pages.
func TestBlocksPaginates(t *testing.T) {
	s := &scriptedExec{
		pages: map[string][]string{
			"root": {
				childrenPage("cur1", leaf("a", "paragraph")),
				childrenPage("", leaf("b", "paragraph")),
			},
		},
		cursors: map[string]int{},
	}
	c := newScriptedClient(s, nil)

	blocks, err := c.Blocks(context.Background(), "root")
	if err != nil {
		t.Fatalf("Blocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (paginated)", len(blocks))
	}
	if blocks[0].ID != "a" || blocks[1].ID != "b" {
		t.Errorf("page order wrong: %s, %s", blocks[0].ID, blocks[1].ID)
	}
	// Two calls for root (page 1 + page 2), and the second must carry the cursor.
	if len(s.calls) != 2 {
		t.Fatalf("expected 2 ntn calls, got %d: %v", len(s.calls), s.calls)
	}
	if !strings.Contains(s.calls[1], "start_cursor=cur1") {
		t.Errorf("second call did not pass the cursor: %s", s.calls[1])
	}
}

// TestBlocksRecurses asserts has_children blocks have their subtree fetched and
// assembled, at arbitrary depth.
func TestBlocksRecurses(t *testing.T) {
	s := &scriptedExec{
		pages: map[string][]string{
			"root": {childrenPage("", parent("t1", "toggle"))},
			"t1":   {childrenPage("", parent("t2", "toggle"))},
			"t2":   {childrenPage("", leaf("p", "paragraph"))},
		},
		cursors: map[string]int{},
	}
	c := newScriptedClient(s, nil)

	blocks, err := c.Blocks(context.Background(), "root")
	if err != nil {
		t.Fatalf("Blocks: %v", err)
	}
	// root -> t1 -> t2 -> p
	if len(blocks) != 1 || blocks[0].ID != "t1" {
		t.Fatalf("top level wrong: %+v", blocks)
	}
	t1 := blocks[0]
	if len(t1.Children) != 1 || t1.Children[0].ID != "t2" {
		t.Fatalf("depth-1 wrong: %+v", t1.Children)
	}
	t2 := t1.Children[0]
	if len(t2.Children) != 1 || t2.Children[0].ID != "p" {
		t.Fatalf("depth-2 wrong: %+v", t2.Children)
	}

	// Build must assemble the same nesting.
	d := doc.Build(doc.RawPage{}, blocks)
	dump := doc.Dump(d)
	if !strings.Contains(dump, "p text") {
		t.Errorf("deepest leaf missing from Build:\n%s", dump)
	}
}

// countingLimiter is a fake rate.Limiter substitute that records each Wait call
// and never sleeps. It lets the rate-limit test assert that EVERY ntn call is
// gated by the limiter, without depending on wall-clock timing (SPEC §9).
type countingLimiter struct {
	waits atomic.Int64
}

func (l *countingLimiter) Wait(_ context.Context) error {
	l.waits.Add(1)
	return nil
}

func (l *countingLimiter) Limit() rate.Limit { return rate.Inf }

// TestBlocksGatedByLimiter asserts the recursive/paginated fetch routes every
// single ntn call through the shared limiter — the mechanism that keeps the deep
// fetch under Notion's ~3 req/s ceiling (SPEC §7, §12.1). It counts limiter
// admissions and ntn calls and requires them equal, proving no call bypasses the
// gate. No wall-clock assertion is made.
func TestBlocksGatedByLimiter(t *testing.T) {
	s := &scriptedExec{
		pages: map[string][]string{
			"root": {
				childrenPage("cur1", parent("t1", "toggle")),
				childrenPage("", parent("t2", "toggle")),
			},
			"t1": {childrenPage("", leaf("a", "paragraph"))},
			"t2": {childrenPage("", leaf("b", "paragraph"))},
		},
		cursors: map[string]int{},
	}
	lim := &countingLimiter{}
	c := newScriptedClient(s, lim)

	if _, err := c.Blocks(context.Background(), "root"); err != nil {
		t.Fatalf("Blocks: %v", err)
	}

	// Calls: root page1, root page2, t1, t2 = 4 ntn calls; each gated once.
	gotCalls := int64(len(s.calls))
	if gotCalls != 4 {
		t.Fatalf("expected 4 ntn calls, got %d: %v", gotCalls, s.calls)
	}
	if got := lim.waits.Load(); got != gotCalls {
		t.Errorf("limiter admitted %d times but %d ntn calls were made; some call bypassed the rate gate", got, gotCalls)
	}
}

// concGauge is a fake execFunc that records the PEAK number of ntn calls in
// flight at once. Each call briefly overlaps (a short sleep) so concurrency is
// observable. It backs the bound-concurrency regression test.
type concGauge struct {
	pages    map[string]string
	inFlight atomic.Int64
	peak     atomic.Int64
}

func (g *concGauge) fn(_ context.Context, _ string, args ...string) commander {
	n := g.inFlight.Add(1)
	for { // record peak (lock-free max)
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond) // force overlap so the bound is exercised
	g.inFlight.Add(-1)

	resp := g.pages[extractBlockID(args[1])]
	if resp == "" {
		resp = `{"results":[],"has_more":false}`
	}
	return fakeCmd{out: []byte(resp)}
}

// TestBlocksBoundsConcurrency is the regression guard for the low-memory budget
// (SPEC §1): a WIDE tree must NOT spawn a fetch per node. Peak concurrent ntn
// calls — a proxy for live goroutines, since each fetch runs on one — must stay
// bounded by the worker limit and not scale with tree width. Before the
// errgroup SetLimit/TryGo fix, a node-per-goroutine fan-out drove this toward
// the node count (~24 here, tens of thousands on a real deep page).
func TestBlocksBoundsConcurrency(t *testing.T) {
	const fanout = 24
	parents := make([]string, fanout)
	pages := map[string]string{}
	for i := range parents {
		pid := fmt.Sprintf("p%d", i)
		parents[i] = parent(pid, "toggle")
		pages[pid] = childrenPage("", leaf(pid+"a", "paragraph"), leaf(pid+"b", "paragraph"))
	}
	pages["root"] = childrenPage("", parents...)

	g := &concGauge{pages: pages}
	const workers = 4
	c := &Client{exec: g.fn, limiter: rate.NewLimiter(rate.Inf, 1), children: workers}

	blocks, err := c.Blocks(context.Background(), "root")
	if err != nil {
		t.Fatalf("Blocks: %v", err)
	}
	if len(blocks) != fanout {
		t.Fatalf("got %d top-level blocks, want %d", len(blocks), fanout)
	}
	for _, b := range blocks {
		if len(b.Children) != 2 {
			t.Fatalf("block %s: got %d children, want 2", b.ID, len(b.Children))
		}
	}
	peak := g.peak.Load()
	if peak < 2 {
		t.Fatalf("peak concurrency %d — test did not actually exercise concurrency", peak)
	}
	// Allow workers + 1 for the caller goroutine running inline work at the limit.
	if peak > workers+1 {
		t.Errorf("peak concurrent ntn calls = %d, want <= %d (unbounded fan-out regressed)", peak, workers+1)
	}
}

// TestBlocksRespectsCancel asserts a cancelled context surfaces promptly instead
// of fetching the whole tree.
func TestBlocksRespectsCancel(t *testing.T) {
	s := &scriptedExec{
		pages:   map[string][]string{"root": {childrenPage("", leaf("a", "paragraph"))}},
		cursors: map[string]int{},
	}
	// A limiter that never admits, so Wait blocks until ctx is done.
	c := newScriptedClient(s, rate.NewLimiter(0, 0))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Blocks(ctx, "root"); err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
