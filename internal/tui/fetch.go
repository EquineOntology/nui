package tui

import (
	"context"
	"sync"
)

// fetch.go owns in-flight fetch cancellation. The Bubble Tea Model is copied by
// value on every Update, so a cancel func stored on the model directly would be
// awkward to thread through every returned model. Instead the Model holds a
// POINTER to one fetchControl that survives the copies; it is mutated only from
// Update (where the cmd constructors run synchronously), so it needs no more than
// a mutex for the goroutine that later reads the returned context.
//
// This makes "navigating away cancels the in-flight fetch" real (SPEC §7/§8): a
// new fetch on an axis cancels the prior one, so e.g. scrolling the selection
// never piles up more than one outstanding preview fetch, and leaving the reader
// cancels its document fetch.

// fetchAxis identifies an independent stream of fetches. Issuing a new fetch on
// an axis cancels the previous one on that same axis only.
type fetchAxis int

const (
	axisSearch fetchAxis = iota
	axisPreview
	axisDoc
)

// fetchControl tracks the cancel func of the latest in-flight fetch per axis.
type fetchControl struct {
	mu      sync.Mutex
	cancels map[fetchAxis]context.CancelFunc
}

func newFetchControl() *fetchControl {
	return &fetchControl{cancels: make(map[fetchAxis]context.CancelFunc)}
}

// next cancels any in-flight fetch on axis and returns a fresh context for the
// new one. Called synchronously from a cmd constructor during Update.
func (f *fetchControl) next(axis fetchAxis) context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.cancels[axis]; c != nil {
		c()
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancels[axis] = cancel
	return ctx
}

// cancel stops the in-flight fetch on axis (if any) without starting a new one —
// used when leaving a mode (e.g. esc out of the reader cancels its doc fetch).
func (f *fetchControl) cancel(axis fetchAxis) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.cancels[axis]; c != nil {
		c()
		delete(f.cancels, axis)
	}
}
