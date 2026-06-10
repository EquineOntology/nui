package notion

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EQuineOntology/nui/internal/doc"
)

// TestTolerantDecodeControlChars is the load-bearing tolerant-decode golden
// (SPEC §7, §9): a real-shaped cachedPage fixture carrying raw control bytes
// (which Go's encoding/json rejects) must decode through notion.Decode and Build
// into a sane Document — proving the control-char sanitization is wired into the
// document path, not just the WhoAmI path. The fixture lives with the other doc
// fixtures.
func TestTolerantDecodeControlChars(t *testing.T) {
	path := filepath.Join("..", "doc", "testdata", "controlchars.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// Sanity: the fixture must actually be strict-invalid, or it is not testing
	// the tolerant path.
	var sink map[string]any
	if json.Unmarshal(data, &sink) == nil {
		t.Fatal("controlchars fixture is not strict-invalid; it does not exercise sanitization")
	}

	var cp cachedPage
	if err := Decode(data, &cp); err != nil {
		t.Fatalf("tolerant Decode rejected the control-char fixture: %v", err)
	}

	d := doc.Build(cp.Page, cp.Blocks)

	// Title carried a bell (0x07) which is dropped; surrounding text survives.
	if !strings.Contains(d.Title, "Control") || !strings.Contains(d.Title, "Char") {
		t.Errorf("title lost text after sanitization: %q", d.Title)
	}
	if strings.ContainsRune(d.Title, '\x07') {
		t.Errorf("control byte survived into title: %q", d.Title)
	}

	// The paragraph carried a NUL (0x00) between words; the words must survive.
	dump := doc.Dump(d)
	if !strings.Contains(dump, "line1") || !strings.Contains(dump, "line2") {
		t.Errorf("paragraph text lost after sanitization:\n%s", dump)
	}
	if strings.ContainsRune(dump, '\x00') {
		t.Error("NUL byte survived into the built Document")
	}
}

// TestDocumentNoCacheFetches verifies Document works without a cache configured
// (the test-client path), exercising Page + Blocks + Build end to end against the
// scripted exec.
func TestDocumentNoCacheFetches(t *testing.T) {
	pageJSON := `{"object":"page","id":"page1","properties":{"Name":{"type":"title","title":[{"type":"text","plain_text":"Hello"}]}}}`
	s := &scriptedExec{
		pages:   map[string][]string{"page1": {childrenPage("", leaf("a", "paragraph"))}},
		cursors: map[string]int{},
	}
	// Wrap the scripted exec so /v1/pages/{id} returns the page metadata while
	// /v1/blocks/{id}/children is served by the script.
	base := s.fn
	c := newScriptedClient(s, nil)
	c.exec = func(ctx context.Context, name string, args ...string) commander {
		if len(args) >= 2 && strings.HasPrefix(args[1], "/v1/pages/") {
			return fakeCmd{out: []byte(pageJSON)}
		}
		return base(ctx, name, args...)
	}
	c.cache = nil

	d, err := c.Document(context.Background(), "page1")
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if d.Title != "Hello" {
		t.Errorf("title = %q, want Hello", d.Title)
	}
	if len(d.Blocks) != 1 || d.Blocks[0].Type != doc.BlockParagraph {
		t.Fatalf("blocks wrong: %+v", d.Blocks)
	}
}
