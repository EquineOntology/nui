package cache

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestColdMissFetchesAndCaches: a first Get fetches synchronously and writes the
// entry; a second Get serves it without re-fetching.
func TestColdMissFetchesAndCaches(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, time.Hour)

	var calls atomic.Int64
	fetch := func(_ context.Context, _ string) ([]byte, error) {
		calls.Add(1)
		return []byte("payload-v1"), nil
	}

	got, err := c.Get(context.Background(), "id1", fetch)
	if err != nil {
		t.Fatalf("Get cold: %v", err)
	}
	if string(got) != "payload-v1" {
		t.Errorf("cold Get = %q", got)
	}
	if calls.Load() != 1 {
		t.Errorf("cold miss made %d fetches, want 1", calls.Load())
	}

	// Fresh hit: no new fetch.
	got, err = c.Get(context.Background(), "id1", fetch)
	if err != nil {
		t.Fatalf("Get warm: %v", err)
	}
	if string(got) != "payload-v1" {
		t.Errorf("warm Get = %q", got)
	}
	if calls.Load() != 1 {
		t.Errorf("warm hit re-fetched (%d total), want 1", calls.Load())
	}
}

// TestServeStaleThenBackgroundRefresh: past the TTL, Get returns the stale copy
// immediately AND triggers a single background refresh whose result is visible
// on the next read. This is the core SWR port of _page_md/_refresh_bg.
func TestServeStaleThenBackgroundRefresh(t *testing.T) {
	dir := t.TempDir()
	// TTL of 0 means every existing entry is immediately stale.
	c := New(dir, time.Nanosecond)

	var calls atomic.Int64
	refreshed := make(chan struct{}, 1)
	fetch := func(_ context.Context, _ string) ([]byte, error) {
		n := calls.Add(1)
		if n == 1 {
			return []byte("v1"), nil
		}
		select {
		case refreshed <- struct{}{}:
		default:
		}
		return []byte("v2"), nil
	}

	// Cold miss writes v1.
	if got, _ := c.Get(context.Background(), "id1", fetch); string(got) != "v1" {
		t.Fatalf("cold = %q, want v1", got)
	}

	// Let the file age past the (nanosecond) TTL.
	time.Sleep(2 * time.Millisecond)

	// Stale read serves v1 immediately and kicks a background refresh.
	if got, _ := c.Get(context.Background(), "id1", fetch); string(got) != "v1" {
		t.Fatalf("stale read = %q, want v1 (serve-stale)", got)
	}

	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("background refresh did not run")
	}

	// Wait for the refresh write to land, then the next read sees v2. Note the
	// loop must not itself trigger a fresh stale refresh, so use a fresh cache
	// with a long TTL for the verification read once v2 is on disk.
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, _, ok := readFile(c.path("id1"))
		if ok && string(data) == "v2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("post-refresh file never became v2 (got %q ok=%v)", data, ok)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for the background refresh to release its lock before returning, so
	// t.TempDir cleanup does not race the goroutine's lock dir removal.
	waitLockCleared(t, c.path("id1"))
}

// waitLockCleared blocks until the refresh lock for path is gone (or fails the
// test), so a test does not return while a background goroutine still holds the
// on-disk lock under t.TempDir.
func waitLockCleared(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path + ".lock"); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh lock never released")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestDedupConcurrentRefresh: a burst of concurrent stale reads must collapse to
// a single in-flight background refresh (the inflight map + on-disk lock).
func TestDedupConcurrentRefresh(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, time.Nanosecond)

	// Seed a stale entry directly.
	path := c.path("id1")
	if err := c.writeAtomic(path, []byte("v1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	time.Sleep(2 * time.Millisecond)

	var refreshes atomic.Int64
	release := make(chan struct{})
	fetch := func(_ context.Context, _ string) ([]byte, error) {
		refreshes.Add(1)
		<-release // hold the refresh open so all readers race the same window
		return []byte("v2"), nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(context.Background(), "id1", fetch)
		}()
	}
	wg.Wait()      // all readers returned (stale served immediately)
	close(release) // let the single refresh complete

	// Give the (at most one) refresh goroutine a moment to record its call.
	time.Sleep(50 * time.Millisecond)
	if n := refreshes.Load(); n > 1 {
		t.Errorf("expected at most 1 deduped refresh, got %d", n)
	}
	waitLockCleared(t, path)
}

// TestAtomicWriteNoPartialReads: a reader concurrent with a write sees either the
// old bytes or the new bytes, never a torn write (the rename swap).
func TestAtomicWriteNoPartialReads(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, time.Hour)
	path := c.path("id1")

	if err := c.writeAtomic(path, []byte("AAAA")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				data, _, ok := readFile(path)
				if ok {
					s := string(data)
					if s != "AAAA" && s != "BBBBBBBB" {
						t.Errorf("torn read: %q", s)
						return
					}
				}
			}
		}
	}()
	for i := 0; i < 200; i++ {
		_ = c.writeAtomic(path, []byte("BBBBBBBB"))
		_ = c.writeAtomic(path, []byte("AAAA"))
	}
	close(stop)
	wg.Wait()
}

// TestColdMissErrorPropagates: a cold-miss fetch failure is returned (nothing to
// serve), unlike a background-refresh failure which is swallowed.
func TestColdMissErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, time.Hour)
	wantErr := context.DeadlineExceeded
	_, err := c.Get(context.Background(), "id1", func(_ context.Context, _ string) ([]byte, error) {
		return nil, wantErr
	})
	if err == nil {
		t.Fatal("expected cold-miss fetch error to propagate")
	}
	// And nothing was cached.
	if _, err := os.Stat(c.path("id1")); !os.IsNotExist(err) {
		t.Errorf("a failed cold miss should not write a cache file")
	}
}

// TestKeyIsFilesystemSafe: ids with slashes/dashes become a safe single-segment
// filename under dir (the bash ${id//[^a-zA-Z0-9]/_} port).
func TestKeyIsFilesystemSafe(t *testing.T) {
	c := New("/cache", time.Hour)
	got := c.path("a/b-c.d")
	want := filepath.Join("/cache", "a_b_c_d.json")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}
