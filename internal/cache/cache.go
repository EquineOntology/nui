// Package cache is the stale-while-revalidate (SWR) store for raw Notion JSON,
// keyed by page id (SPEC §7). It is a faithful port of the bash _page_md /
// _refresh_bg semantics:
//
//   - A cached copy is served immediately, even past the TTL.
//   - A read of an expired copy kicks off a single deduplicated background
//     refresh (an mkdir-style lock prevents a thundering herd); the next read is
//     fresh.
//   - Only a cold miss blocks on the upstream fetch; concurrent cold misses for
//     the same id collapse to one fetch via an in-process single-flight (so a
//     preview→reader burst on the same page does not double-fetch).
//   - Writes are atomic (temp file + rename) so a reader never sees a half-written
//     entry.
//
// It caches raw JSON bytes, not the Document or laid-out Lines, so a terminal
// resize re-layouts for free without a network hit (SPEC §7). The cache imports
// nothing from notion/doc/tui — it is a generic bytes store the notion.Client
// drives.
package cache

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// DefaultTTL is the staleness threshold past which a served entry triggers a
	// background refresh (SPEC §7: 600s). Matches the bash CACHE_TTL.
	DefaultTTL = 600 * time.Second

	// lockTTL bounds how long an in-flight-refresh lock is honored before another
	// reader steals it, matching the bash 60s stale-lock steal.
	lockTTL = 60 * time.Second
)

// idSanitizer mirrors the bash key derivation ${id//[^a-zA-Z0-9]/_}: every
// non-alphanumeric byte in a page id becomes '_' so the key is a safe filename.
var idSanitizer = regexp.MustCompile(`[^a-zA-Z0-9]`)

// Fetcher fetches the raw JSON for a page id from upstream (the notion.Client
// passes a closure that runs the recursive block fetch). It must honor ctx.
type Fetcher func(ctx context.Context, id string) ([]byte, error)

// Cache is a filesystem SWR store. The zero value is not usable; construct with
// New. It is safe for concurrent use: the inflight map dedupes background
// refreshes within a process, and the on-disk lock dedupes across processes
// (the bash front-end and the Go binary can run concurrently during G2–G3).
type Cache struct {
	dir string
	ttl time.Duration

	mu       sync.Mutex
	inflight map[string]bool // ids currently being refreshed in this process

	// cold collapses concurrent COLD misses for the same id into one upstream
	// fetch (the in-process single-flight, G2 carryover). It is distinct from the
	// inflight map / on-disk lock above, which dedupe only the BACKGROUND refresh
	// of an already-present (stale) entry. A cold miss has nothing on disk yet, so
	// the on-disk lock cannot help it; without this, a live preview pane plus the
	// reader would fire two cold reads of the most expensive op in the system
	// (highlight a result, then Enter the same id). Cross-process cold-miss races
	// still exist — singleflight is per-process and the on-disk lock only guards
	// background refresh — but in-process collapse covers the hot path.
	cold singleflight.Group
}

// New returns a Cache rooted at dir with the given TTL (use DefaultTTL for the
// spec value). The directory is created on first write, not here, so constructing
// a Cache never touches the filesystem.
func New(dir string, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Cache{
		dir:      dir,
		ttl:      ttl,
		inflight: make(map[string]bool),
	}
}

// Dir reports the directory the cache writes to. Used by callers that want to
// surface or clean the cache location.
func (c *Cache) Dir() string { return c.dir }

// Get returns the raw JSON for id, applying SWR semantics:
//
//   - Hit (any age): return the cached bytes immediately. If the entry is older
//     than the TTL, a single deduplicated background refresh is launched (the
//     returned bytes are still the stale copy — the next Get sees the fresh one).
//   - Miss: fetch synchronously, cache the result, and return it.
//
// fetch is invoked at most once per cold miss and at most once per background
// refresh. A background-refresh failure is swallowed (the stale copy already
// served); a cold-miss failure is returned to the caller.
func (c *Cache) Get(ctx context.Context, id string, fetch Fetcher) ([]byte, error) {
	path := c.path(id)
	if data, mtime, ok := readFile(path); ok {
		if time.Since(mtime) >= c.ttl {
			c.refreshBackground(id, path, fetch)
		}
		return data, nil
	}

	// Cold miss: nothing to serve. Route concurrent cold misses for the same id
	// through a single in-flight fetch so a preview→reader burst (highlight then
	// open the same page) does not double-fetch the most expensive op in the
	// system. DoChan (not Do) lets a waiter honor its OWN ctx cancellation via the
	// select below; the shared fetch itself runs under the LEADER's ctx — a
	// waiter cancelling does not cancel the leader's in-flight call (and vice
	// versa), so a cancelled waiter just stops waiting while the fetch completes
	// for whoever remains. Cross-process cold-miss races are NOT covered (the
	// on-disk lock only guards background refresh, and singleflight is in-process).
	ch := c.cold.DoChan(id, func() (any, error) {
		// Re-check the cache inside the flight: between joining the group and
		// running, a background refresh or another process may have written the
		// entry, in which case we should serve it rather than re-fetch.
		if data, _, ok := readFile(path); ok {
			return data, nil
		}
		data, err := fetch(ctx, id)
		if err != nil {
			return nil, err
		}
		// A cache write failure must not fail the read — the bytes are valid, we
		// just couldn't persist them. Return them anyway.
		_ = c.writeAtomic(path, data)
		return data, nil
	})

	select {
	case <-ctx.Done():
		// This waiter gave up; the shared fetch continues for the others.
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		// res.Val is the []byte the flight returned; the type assertion is safe
		// because the flight func above only ever returns []byte on success.
		data, _ := res.Val.([]byte)
		return data, nil
	}
}

// refreshBackground launches one deduplicated background refresh for id. It
// dedupes both in-process (the inflight map) and cross-process (the on-disk
// lock), so a burst of stale reads — or the bash front-end racing the Go binary
// — produces a single upstream fetch.
func (c *Cache) refreshBackground(id, path string, fetch Fetcher) {
	c.mu.Lock()
	if c.inflight[id] {
		c.mu.Unlock()
		return
	}
	if !c.acquireLock(path) {
		c.mu.Unlock()
		return
	}
	c.inflight[id] = true
	c.mu.Unlock()

	go func() {
		defer func() {
			c.releaseLock(path)
			c.mu.Lock()
			delete(c.inflight, id)
			c.mu.Unlock()
		}()
		// A background refresh uses its own timeout-bounded context: the caller's
		// ctx may be cancelled the instant it navigates away, but the refresh is
		// the whole point of serving stale, so it must outlive that.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		data, err := fetch(ctx, id)
		if err != nil || len(data) == 0 {
			return // stale copy already served; swallow the failure
		}
		_ = c.writeAtomic(path, data)
	}()
}

// path is the cache file for an id: dir/<sanitized-id>.json.
func (c *Cache) path(id string) string {
	return filepath.Join(c.dir, idSanitizer.ReplaceAllString(id, "_")+".json")
}

// writeAtomic writes data to a temp file in the same directory and renames it
// over path, so a concurrent reader sees either the old or the new content,
// never a partial write (the bash `mv -f` atomic swap).
func (c *Cache) writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// acquireLock attempts to claim the refresh lock for path by atomically creating
// path.lock as a directory (mkdir is atomic; O_EXCL semantics). A lock older than
// lockTTL is considered stale and stolen, matching the bash 60s steal. Returns
// false if a fresh lock is already held.
func (c *Cache) acquireLock(path string) bool {
	lock := path + ".lock"
	if err := os.Mkdir(lock, 0o755); err == nil {
		return true
	}
	// Lock exists: steal it if stale.
	if info, err := os.Stat(lock); err == nil {
		if time.Since(info.ModTime()) >= lockTTL {
			_ = os.RemoveAll(lock)
			if err := os.Mkdir(lock, 0o755); err == nil {
				return true
			}
		}
	}
	return false
}

func (c *Cache) releaseLock(path string) {
	_ = os.RemoveAll(path + ".lock")
}

// readFile returns the file contents and mtime, ok=false if the file is missing
// or unreadable (treated as a cold miss).
func readFile(path string) (data []byte, mtime time.Time, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	return b, info.ModTime(), true
}
