// Package notion is the single boundary between nui and Notion. Everything that
// knows the Notion API shape or shells out to the `ntn` binary lives here
// (SPEC §7). The rest of nui talks to a Client; it never execs ntn directly.
//
// Two seams make this package testable without a network or a real ntn binary:
//   - execFunc: an injectable command constructor (defaults to exec.CommandContext
//     "ntn"). Tests swap in a fake that returns canned stdout / exit codes.
//   - Decode: the tolerant JSON decoder is exported so every caller (and tests)
//     uses the exact same control-char sanitization before json.Unmarshal.
package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/EQuineOntology/nui/internal/cache"
)

const (
	// defaultRate is the requests-per-second ceiling to ntn when NUI_RATE is
	// unset. Notion's API tolerates ~3 req/s; staying under it avoids 429s
	// during the recursive block fetch (SPEC §7, §12).
	defaultRate = 3.0

	// defaultTimeout bounds a single ntn api call. The TUI also cancels via
	// ctx when the user navigates away; this is the backstop for a hung call.
	defaultTimeout = 30 * time.Second

	// defaultChildWorkers caps the concurrent child-fetch goroutines during the
	// recursive block walk (SPEC §7: bounded worker pool, semaphore default 4).
	// All workers still share the single rate.Limiter, so this only bounds
	// goroutine fan-out, not the call rate.
	defaultChildWorkers = 4
)

// Sentinel errors callers can match with errors.Is to map to UX. They are kept
// typed (not just string-formatted) so the TUI status line and main.go can
// branch without string matching.
var (
	// ErrNtnNotFound means the ntn binary is not on PATH. The fix is to install it.
	ErrNtnNotFound = errors.New("ntn not found on PATH")

	// ErrUnauthorized means ntn ran but Notion rejected the credentials (401),
	// or ntn reports it is not logged in. The fix is `nui login`.
	ErrUnauthorized = errors.New("not authorized; run `nui login`")
)

// commander is the minimal slice of *exec.Cmd that Client needs. It exists so
// tests can return a fake implementation from execFunc without spawning a process.
type commander interface {
	// Output runs the command and returns its stdout. On a non-zero exit it
	// returns an error; for *exec.Cmd that is *exec.ExitError carrying Stderr
	// and the exit code (which mapExecErr inspects).
	Output() ([]byte, error)
}

// execFunc constructs a runnable command. The default builds an *exec.Cmd via
// exec.CommandContext; tests inject a fake. Keeping the signature identical to
// exec.CommandContext makes the default a one-liner and the seam obvious.
type execFunc func(ctx context.Context, name string, args ...string) commander

// limiter is the minimal slice of *rate.Limiter that Client.run depends on. It
// exists so the rate-limit test can inject a counting fake and assert every call
// is gated, without asserting on wall-clock sleeps (SPEC §9). *rate.Limiter
// satisfies it directly.
type limiter interface {
	// Wait blocks until the limiter admits one event or ctx is done.
	Wait(ctx context.Context) error
	// Limit reports the configured rate (used by NewClient tests to verify the
	// NUI_RATE wiring). *rate.Limiter provides it.
	Limit() rate.Limit
}

// defaultExecFunc shells out to a real binary, capturing stderr so mapExecErr
// can surface Notion's error message.
func defaultExecFunc(ctx context.Context, name string, args ...string) commander {
	return exec.CommandContext(ctx, name, args...)
}

// Client is the boundary to ntn. It is safe to construct with NewClient and
// share; the rate.Limiter serializes the call rate across goroutines.
type Client struct {
	exec    execFunc
	limiter limiter
	timeout time.Duration
	cache   *cache.Cache
	// children is the bounded worker-pool ceiling for the recursive block fetch
	// (semaphore size, SPEC §7). Zero means defaultChildWorkers.
	children int
}

// NewClient builds a Client with production defaults: the real ntn exec seam,
// a rate limiter at NUI_RATE (default 3/s), and the default per-call timeout.
func NewClient() *Client {
	r := defaultRate
	if v := os.Getenv("NUI_RATE"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed > 0 {
			r = parsed
		}
	}
	return &Client{
		exec:     defaultExecFunc,
		limiter:  rate.NewLimiter(rate.Limit(r), 1),
		timeout:  defaultTimeout,
		cache:    cache.New(cacheDir(), cache.DefaultTTL),
		children: defaultChildWorkers,
	}
}

// cacheDir resolves the raw-JSON cache directory, honoring XDG_CACHE_HOME then
// TMPDIR then /tmp, all suffixed with /nui (SPEC §7, §10). It mirrors the bash
// CACHE_DIR derivation so the two front-ends can share a cache during G2–G3.
func cacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "nui")
	}
	if t := os.Getenv("TMPDIR"); t != "" {
		return filepath.Join(t, "nui")
	}
	return filepath.Join("/tmp", "nui")
}

// run executes `ntn <args...>`, gated by the rate limiter and bounded by the
// per-call timeout (unless ctx already carries a shorter deadline). It returns
// stdout, mapping failures to the typed sentinel errors where possible.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	callCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	out, err := c.exec(callCtx, "ntn", args...).Output()
	if err != nil {
		return nil, c.mapExecErr(err)
	}
	return out, nil
}

// mapExecErr translates a raw exec failure into a typed nui error:
//   - ntn missing from PATH  -> ErrNtnNotFound
//   - 401 / unauthorized text -> ErrUnauthorized
//   - anything else           -> a wrapped error carrying ntn's stderr
func (c *Client) mapExecErr(err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return ErrNtnNotFound
	}
	// exec.LookPath failures wrapped by exec.Cmd.Start surface as *exec.Error.
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		if errors.Is(execErr.Err, exec.ErrNotFound) {
			return ErrNtnNotFound
		}
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr := strings.ToLower(string(exitErr.Stderr))
		if strings.Contains(stderr, "401") ||
			strings.Contains(stderr, "unauthorized") ||
			strings.Contains(stderr, "not logged in") {
			return ErrUnauthorized
		}
		msg := strings.TrimSpace(string(exitErr.Stderr))
		if msg == "" {
			msg = exitErr.String()
		}
		return fmt.Errorf("ntn failed: %s", msg)
	}

	return fmt.Errorf("ntn failed: %w", err)
}

// API runs `ntn api <path>` (optionally with a request body via -d) and returns
// the tolerantly-decoded JSON unmarshaled into v. body == "" omits -d, used for
// GET-style calls. This is the single API transport every later notion/ file
// (blocks, page, search, views) builds on.
func (c *Client) API(ctx context.Context, path, body string, v any) error {
	args := []string{"api", path}
	if body != "" {
		args = append(args, "-d", body)
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	return Decode(out, v)
}

// sanitize repairs the raw control characters Notion emits inside JSON string
// values that Go's (strict) encoding/json rejects. The bash version leaned on
// Python's json.loads(strict=False), which silently tolerates raw controls;
// Go has no such mode, so this port is load-bearing, not an edge case
// (SPEC §7, §12.3).
//
// The transform is string-aware. Outside string literals, raw tab/newline/CR
// are legal JSON whitespace (ntn pretty-prints its output that way), so they
// must be left untouched — escaping them there would itself break the parse.
// Inside a string literal, a raw control byte is illegal to Go's parser, so:
//   - tab/newline/CR  -> their JSON escape sequences (\t \n \r), preserving the
//     whitespace in the decoded value (the spec's "except \t \n \r");
//   - every other control byte (< 0x20) is dropped, matching the bash behavior
//     of stripping it to nothing.
//
// Escape state is tracked so a legitimate \" inside a string does not falsely
// close it. The backslash itself is emitted lazily — only once the byte it
// escapes is seen — so a backslash immediately followed by a RAW control byte
// (illegal JSON that Go also rejects) is repaired rather than copied through as
// a broken escape sequence: raw tab/newline/CR become \t/\n/\r, any other raw
// control after the backslash drops both bytes.
func sanitize(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for _, b := range data {
		if !inString {
			if b == '"' {
				inString = true
			}
			out = append(out, b)
			continue
		}
		// inside a string literal
		if escaped {
			// A backslash was held back; this byte is what it escapes. Emit the
			// held backslash together with a legal escape byte; repair a raw
			// control byte that would otherwise be an illegal escape.
			escaped = false
			switch b {
			case '\t':
				out = append(out, '\\', 't')
			case '\n':
				out = append(out, '\\', 'n')
			case '\r':
				out = append(out, '\\', 'r')
			default:
				if b < 0x20 {
					// backslash + other raw control: no valid escape, drop both.
					continue
				}
				out = append(out, '\\', b)
			}
			continue
		}
		switch {
		case b == '\\':
			escaped = true // hold; emit once we know what it escapes
		case b == '"':
			inString = false
			out = append(out, b)
		case b == '\t':
			out = append(out, '\\', 't')
		case b == '\n':
			out = append(out, '\\', 'n')
		case b == '\r':
			out = append(out, '\\', 'r')
		case b < 0x20:
			// drop other control chars inside the string
		default:
			out = append(out, b)
		}
	}
	return out
}

// Decode is the tolerant JSON decode path all callers share: it sanitizes raw
// control chars (see sanitize) and then unmarshals into v. Exported so tests
// and every notion/ file decode identically. Never panics: a malformed body
// after sanitization returns a wrapped json error for graceful degradation.
func Decode(data []byte, v any) error {
	if err := json.Unmarshal(sanitize(data), v); err != nil {
		return fmt.Errorf("notion: decode failed: %w", err)
	}
	return nil
}
