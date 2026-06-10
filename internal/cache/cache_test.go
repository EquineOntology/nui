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

// TestDedupConcurrentColdMiss: N concurrent cold Get calls for the same id must
// collapse to a single upstream fetch (the in-process single-flight). This is
// the G2 hot path: highlighting a result (preview) then opening it (reader) is
// two cold reads of the same id in quick succession.
func TestDedupConcurrentColdMiss(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, time.Hour)

	var fetches atomic.Int64
	enter := make(chan struct{}) // first fetcher signals it has started
	release := make(chan struct{})
	fetch := func(_ context.Context, _ string) ([]byte, error) {
		if fetches.Add(1) == 1 {
			close(enter)
		}
		<-release // hold the flight open so every caller queues behind the leader
		return []byte("payload"), nil
	}

	const n = 20
	results := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = c.Get(context.Background(), "id1", fetch)
		}(i)
	}

	// Ensure the leader is in-flight before releasing, so the other 19 callers
	// have a window to join the single-flight group rather than each racing in
	// as their own cold miss.
	<-enter
	// Give the remaining goroutines a moment to reach DoChan and join.
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := fetches.Load(); got != 1 {
		t.Errorf("concurrent cold misses fetched %d times, want exactly 1", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("caller %d: unexpected error %v", i, errs[i])
		}
		if string(results[i]) != "payload" {
			t.Errorf("caller %d: got %q, want payload", i, results[i])
		}
	}
}

// TestColdMissWaiterHonorsOwnCtx: a waiter whose ctx is cancelled while the
// shared flight is still running returns its own ctx error promptly, without
// affecting the leader's fetch (which still completes and caches).
func TestColdMissWaiterHonorsOwnCtx(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, time.Hour)

	release := make(chan struct{})
	leaderStarted := make(chan struct{})
	var fetches atomic.Int64
	fetch := func(_ context.Context, _ string) ([]byte, error) {
		if fetches.Add(1) == 1 {
			close(leaderStarted)
		}
		<-release
		return []byte("payload"), nil
	}

	// Leader: a long-lived context that drives the actual fetch.
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		data, err := c.Get(context.Background(), "id1", fetch)
		if err != nil || string(data) != "payload" {
			t.Errorf("leader Get = (%q, %v), want (payload, nil)", data, err)
		}
	}()

	<-leaderStarted

	// Waiter: joins the same flight, then has its ctx cancelled. It must return
	// ctx.Err() without waiting for release.
	waiterCtx, cancel := context.WithCancel(context.Background())
	waiterErr := make(chan error, 1)
	go func() {
		_, err := c.Get(waiterCtx, "id1", fetch)
		waiterErr <- err
	}()
	// Let the waiter reach its select, then cancel it.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-waiterErr:
		if err != context.Canceled {
			t.Errorf("cancelled waiter err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not return promptly")
	}

	// The leader's fetch was unaffected by the waiter's cancellation.
	close(release)
	<-leaderDone
	if got := fetches.Load(); got != 1 {
		t.Errorf("fetch ran %d times, want 1 (waiter must not trigger its own)", got)
	}
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
