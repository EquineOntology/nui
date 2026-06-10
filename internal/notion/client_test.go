package notion

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// fakeCmd is a canned commander: it returns the stdout/err it was constructed
// with, so tests drive Client without spawning a process.
type fakeCmd struct {
	out []byte
	err error
}

func (f fakeCmd) Output() ([]byte, error) { return f.out, f.err }

// newTestClient builds a Client whose exec seam returns the given fakeCmd for
// every call, with an unlimited rate limiter and no timeout so tests are fast
// and deterministic.
func newTestClient(cmd commander) *Client {
	return &Client{
		exec: func(_ context.Context, _ string, _ ...string) commander {
			return cmd
		},
		limiter: rate.NewLimiter(rate.Inf, 1),
		timeout: 0,
	}
}

// exitError fabricates an *exec.ExitError carrying stderr. mapExecErr branches
// on the stderr text (the 401 path) and on the *exec.ExitError type via
// errors.As, neither of which needs a real exit code or ProcessState.
func exitError(stderr string) error {
	return &exec.ExitError{Stderr: []byte(stderr)}
}

func TestSanitizeStripsControlChars(t *testing.T) {
	// Build a payload with a raw NUL, a vertical tab, and a bell inside a JSON
	// string — all < 0x20 and all rejected by encoding/json. Tab/newline/CR
	// must survive (they are legitimate whitespace Notion emits raw).
	raw := []byte("{\"text\":\"a\x00b\x0bc\x07\tkeep\nme\rtoo\"}")

	// Sanity: plain Unmarshal must reject the unsanitized bytes, proving the
	// fixture actually exercises the strictness we are defending against.
	var plain map[string]string
	if err := json.Unmarshal(raw, &plain); err == nil {
		t.Fatalf("fixture is not strict-invalid; json.Unmarshal accepted it")
	}

	var got map[string]string
	if err := Decode(raw, &got); err != nil {
		t.Fatalf("tolerant Decode rejected sanitizable input: %v", err)
	}
	want := "abc\tkeep\nme\rtoo"
	if got["text"] != want {
		t.Fatalf("Decode text = %q, want %q", got["text"], want)
	}
}

func TestSanitizeRepairsBackslashBeforeRawControl(t *testing.T) {
	// A backslash immediately followed by a RAW control byte is illegal JSON that
	// Go rejects; sanitize holds the backslash and repairs it: raw tab/newline/CR
	// become their escape, any other raw control drops both bytes. (Implausible in
	// real Notion output, but the function's contract claims to handle every
	// in-string control byte — this guards that claim.)
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{"backslash-then-raw-newline", []byte("{\"t\":\"a\\\nb\"}"), "a\nb"},
		{"backslash-then-raw-tab", []byte("{\"t\":\"a\\\tb\"}"), "a\tb"},
		{"backslash-then-other-control", []byte("{\"t\":\"a\\\x01b\"}"), "ab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var plain map[string]string
			if err := json.Unmarshal(tc.raw, &plain); err == nil {
				t.Fatalf("fixture is not strict-invalid; json.Unmarshal accepted it")
			}
			var got map[string]string
			if err := Decode(tc.raw, &got); err != nil {
				t.Fatalf("tolerant Decode rejected repairable input: %v", err)
			}
			if got["t"] != tc.want {
				t.Fatalf("Decode t = %q, want %q", got["t"], tc.want)
			}
		})
	}
}

func TestDecodeStillFailsOnTrulyBrokenJSON(t *testing.T) {
	// Sanitization removes control chars but does not fix structural breakage;
	// such input must return an error (never panic).
	var got map[string]any
	if err := Decode([]byte("{not json"), &got); err == nil {
		t.Fatalf("expected error decoding structurally-broken JSON, got nil")
	}
}

func TestWhoAmISuccess(t *testing.T) {
	body := `{
      "id": "bot-id",
      "name": "Notion CLI",
      "object": "user",
      "type": "bot",
      "bot": {
        "workspace_id": "ws-id",
        "workspace_name": "Equilibrium Energy",
        "owner": {
          "type": "user",
          "user": {
            "id": "user-id",
            "name": "Christian Fratta",
            "person": {"email": "christian@example.com"}
          }
        }
      }
    }`
	c := newTestClient(fakeCmd{out: []byte(body)})

	id, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI returned error: %v", err)
	}
	if id.WorkspaceName != "Equilibrium Energy" {
		t.Errorf("WorkspaceName = %q", id.WorkspaceName)
	}
	if id.BotName != "Notion CLI" {
		t.Errorf("BotName = %q", id.BotName)
	}
	if id.UserName != "Christian Fratta" {
		t.Errorf("UserName = %q", id.UserName)
	}
	if id.UserEmail != "christian@example.com" {
		t.Errorf("UserEmail = %q", id.UserEmail)
	}
}

func TestWhoAmISuccessWithControlChars(t *testing.T) {
	// Exercise the tolerant path end-to-end: a raw control char in the JSON
	// that plain Unmarshal would reject must still yield a valid Identity.
	body := "{\"id\":\"bot-id\",\"name\":\"Notion\x07 CLI\",\"bot\":{\"workspace_name\":\"EQ\"}}"
	c := newTestClient(fakeCmd{out: []byte(body)})

	id, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI returned error on control-char body: %v", err)
	}
	if id.WorkspaceName != "EQ" {
		t.Errorf("WorkspaceName = %q, want EQ", id.WorkspaceName)
	}
}

func TestWhoAmIUnauthorized(t *testing.T) {
	// ntn exits non-zero with a 401 in stderr.
	c := newTestClient(fakeCmd{err: exitError("Error: HTTP 401 Unauthorized")})

	_, err := c.WhoAmI(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("WhoAmI error = %v, want ErrUnauthorized", err)
	}
}

func TestWhoAmIUnauthorizedEmptyPayload(t *testing.T) {
	// A logged-out ntn may exit zero but return an identity-less payload.
	c := newTestClient(fakeCmd{out: []byte(`{}`)})

	_, err := c.WhoAmI(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("WhoAmI error = %v, want ErrUnauthorized", err)
	}
}

func TestWhoAmINtnAbsent(t *testing.T) {
	// exec reports the binary is not on PATH (as exec.CommandContext would when
	// the command cannot be found and .Output() is called).
	c := newTestClient(fakeCmd{err: &exec.Error{Name: "ntn", Err: exec.ErrNotFound}})

	_, err := c.WhoAmI(context.Background())
	if !errors.Is(err, ErrNtnNotFound) {
		t.Fatalf("WhoAmI error = %v, want ErrNtnNotFound", err)
	}
}

func TestMapExecErrPlainNotFound(t *testing.T) {
	c := &Client{}
	if got := c.mapExecErr(exec.ErrNotFound); !errors.Is(got, ErrNtnNotFound) {
		t.Fatalf("mapExecErr(ErrNotFound) = %v, want ErrNtnNotFound", got)
	}
}

func TestMapExecErrGenericExit(t *testing.T) {
	c := &Client{}
	err := c.mapExecErr(exitError("boom: something broke"))
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrNtnNotFound) {
		t.Fatalf("generic exit misclassified: %v", err)
	}
	if err == nil {
		t.Fatal("expected a wrapped error for a generic non-zero exit")
	}
}

func TestNewClientDefaults(t *testing.T) {
	t.Setenv("NUI_RATE", "")
	c := NewClient()
	if c.limiter.Limit() != rate.Limit(defaultRate) {
		t.Errorf("default rate = %v, want %v", c.limiter.Limit(), defaultRate)
	}
	if c.timeout != defaultTimeout {
		t.Errorf("default timeout = %v, want %v", c.timeout, defaultTimeout)
	}
}

func TestNewClientRespectsNuiRate(t *testing.T) {
	t.Setenv("NUI_RATE", "7.5")
	c := NewClient()
	if c.limiter.Limit() != rate.Limit(7.5) {
		t.Errorf("NUI_RATE rate = %v, want 7.5", c.limiter.Limit())
	}
}

func TestNewClientIgnoresBadNuiRate(t *testing.T) {
	t.Setenv("NUI_RATE", "not-a-number")
	c := NewClient()
	if c.limiter.Limit() != rate.Limit(defaultRate) {
		t.Errorf("bad NUI_RATE should fall back to default, got %v", c.limiter.Limit())
	}
}

func TestRateLimiterRespectsContextCancel(t *testing.T) {
	// A rate limiter that never admits, plus an already-cancelled context, must
	// surface promptly rather than block — proving run honors ctx.
	c := &Client{
		exec:    func(_ context.Context, _ string, _ ...string) commander { return fakeCmd{} },
		limiter: rate.NewLimiter(0, 0),
		timeout: 0,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { _, err := c.run(ctx, "whoami"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from cancelled context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not honor context cancellation")
	}
}
