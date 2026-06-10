package notion

import (
	"context"
	"strings"
	"testing"
)

// TestWhoAmIPrettyPrintedJSON guards the string-aware sanitizer against the
// real ntn shape: `ntn whoami --json` pretty-prints with raw newlines between
// tokens. Those structural newlines are legal JSON whitespace and must survive
// untouched; only control chars *inside* string values get repaired.
func TestWhoAmIPrettyPrintedJSON(t *testing.T) {
	pretty := `{
  "id": "bot-id",
  "name": "Notion CLI",
  "bot": {
    "workspace_name": "Equilibrium Energy",
    "owner": {
      "user": {
        "name": "Christian Fratta",
        "person": { "email": "c@example.com" }
      }
    }
  }
}`
	c := newTestClient(fakeCmd{out: []byte(pretty)})
	id, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI on pretty-printed JSON: %v", err)
	}
	if id.WorkspaceName != "Equilibrium Energy" || id.UserName != "Christian Fratta" {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestIdentityString(t *testing.T) {
	id := Identity{
		WorkspaceName: "Equilibrium Energy",
		BotName:       "Notion CLI",
		UserName:      "Christian Fratta",
		UserEmail:     "c@example.com",
	}
	s := id.String()
	for _, want := range []string{"Equilibrium Energy", "Notion CLI", "Christian Fratta", "c@example.com"} {
		if !strings.Contains(s, want) {
			t.Errorf("Identity.String() missing %q:\n%s", want, s)
		}
	}
}

func TestIdentityStringOmitsEmptyFields(t *testing.T) {
	id := Identity{WorkspaceName: "EQ"}
	s := id.String()
	if strings.Contains(s, "user:") || strings.Contains(s, "via:") {
		t.Errorf("expected only the workspace line, got:\n%s", s)
	}
}

// TestSanitizeEmbeddedQuoteEscape ensures an escaped quote inside a string does
// not prematurely close the string (which would let a later structural newline
// be misclassified as in-string).
func TestSanitizeEmbeddedQuoteEscape(t *testing.T) {
	body := `{"name":"a \"quoted\" name","bot":{"workspace_name":"EQ"}}`
	c := newTestClient(fakeCmd{out: []byte(body)})
	id, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI with escaped quotes: %v", err)
	}
	if id.BotName != `a "quoted" name` {
		t.Errorf("BotName = %q", id.BotName)
	}
	if id.WorkspaceName != "EQ" {
		t.Errorf("WorkspaceName = %q", id.WorkspaceName)
	}
}
