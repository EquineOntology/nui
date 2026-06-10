package notion

import (
	"context"
	"strings"
	"testing"
)

// fixtureSearchJSON is a hand-built /v1/search response exercising every shape
// the rerank must handle:
//   - a database object (title from the top-level title array),
//   - pages (title from the property whose type=="title"),
//   - a duplicate id (must be deduped),
//   - an empty/missing id (must be skipped),
//   - a control char inside a title string (must still parse via the tolerant
//     decoder, AND be cleaned to a space in the output title),
//   - a control char inside a url (must be stripped),
//   - API order that does NOT match relevance order (so the rerank is observable).
//
// The API order here is deliberately "wrong": the exact-title match is listed
// last, so a passing ordering proves the client-side rerank ran (not just the
// API's last_edited_time order passed through).
const fixtureSearchJSON = `{
  "results": [
    {
      "object": "page",
      "id": "page-unrelated",
      "url": "https://notion.so/unrelated",
      "properties": {
        "Name": {"type": "title", "title": [{"plain_text": "Quarterly logistics memo"}]}
      }
    },
    {
      "object": "data_source",
      "id": "db-grid",
      "url": "https://notion.so/grid",
      "title": [{"plain_text": "Grid Operations Tracker"}]
    },
    {
      "object": "page",
      "id": "page-prefix",
      "url": "https://notion.so/prefix",
      "properties": {
        "Title": {"type": "title", "title": [{"plain_text": "Grid resiliency notes"}]}
      }
    },
    {
      "object": "page",
      "id": "page-substr",
      "url": "https://notion.so/sub` + "\x07" + `str",
      "properties": {
        "Name": {"type": "title", "title": [{"plain_text": "EQ` + "\t" + `grid` + "\x01" + ` review"}]}
      }
    },
    {
      "object": "page",
      "id": "page-tokens",
      "url": "https://notion.so/tokens",
      "properties": {
        "Name": {"type": "title", "title": [{"plain_text": "Resiliency and grid planning"}]}
      }
    },
    {
      "object": "page",
      "id": "page-exact",
      "url": "https://notion.so/exact",
      "properties": {
        "Name": {"type": "title", "title": [{"plain_text": "Grid"}]}
      }
    },
    {
      "object": "page",
      "id": "page-exact",
      "url": "https://notion.so/dup",
      "properties": {
        "Name": {"type": "title", "title": [{"plain_text": "Duplicate id, must drop"}]}
      }
    },
    {
      "object": "page",
      "id": "",
      "url": "https://notion.so/noid",
      "properties": {
        "Name": {"type": "title", "title": [{"plain_text": "No id, must drop"}]}
      }
    },
    {
      "object": "page",
      "id": "page-untitled",
      "url": "https://notion.so/untitled",
      "properties": {
        "Status": {"type": "select"}
      }
    }
  ]
}`

// decodeFixture runs the fixture through the same tolerant Decode the real
// Search path uses, so the test also proves invalid-JSON-with-control-chars
// parses (a raw 0x07 in a url and a raw 0x01 in a title would both make Go's
// strict json.Unmarshal fail).
func decodeFixture(t *testing.T) rawSearchResponse {
	t.Helper()
	var resp rawSearchResponse
	if err := Decode([]byte(fixtureSearchJSON), &resp); err != nil {
		t.Fatalf("tolerant Decode rejected the control-char fixture: %v", err)
	}
	return resp
}

// TestRerankOrdering asserts the relevance tiers order results exactly:
// exact(100) > prefix(80) > substring(60) > all-tokens(40) > some-tokens(5),
// with dedupe and skip-empty-id applied. The query "grid" makes each tier
// observable.
func TestRerankOrdering(t *testing.T) {
	resp := decodeFixture(t)
	got := rerank("grid", resp)

	wantIDs := []string{
		"page-exact",     // "Grid" == "grid"             -> 100
		"db-grid",        // "Grid Operations Tracker"    -> 80 (prefix)
		"page-prefix",    // "Grid resiliency notes"      -> 80 (prefix), API order after db-grid
		"page-substr",    // "EQ grid review"             -> 60 (substring "grid")
		"page-tokens",    // "Resiliency and grid planning"-> 60 (substring "grid" present)
		"page-unrelated", // "Quarterly logistics memo"   -> 0, API index 0 (before untitled)
		"page-untitled",  // "(untitled)"                 -> 0, API index 8 (last)
	}

	if len(got) != len(wantIDs) {
		t.Fatalf("got %d results, want %d: %+v", len(got), len(wantIDs), got)
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("position %d: got id %q (title %q, score-tier mismatch), want %q",
				i, got[i].ID, got[i].Title, id)
		}
	}
}

// TestRerankCleansAndClassifies checks the per-field cleanup: control chars in
// titles become spaces (and the title is trimmed), control chars in urls are
// stripped, database/data_source -> "database" kind, and the untitled fallback.
func TestRerankCleansAndClassifies(t *testing.T) {
	resp := decodeFixture(t)
	got := rerank("grid", resp)

	byID := make(map[string]Result, len(got))
	for _, r := range got {
		byID[r.ID] = r
	}

	if r := byID["db-grid"]; r.Kind != "database" {
		t.Errorf("data_source kind = %q, want database", r.Kind)
	}
	if r := byID["page-exact"]; r.Kind != "page" {
		t.Errorf("page kind = %q, want page", r.Kind)
	}
	// The decoded title is "EQ\tgrid review": the tab survives the tolerant decode
	// (it is legal whitespace), and the 0x01 was dropped by the decoder (it strips
	// controls other than tab/nl/cr inside strings). cleanTitle then replaces the
	// surviving tab with a space, giving "EQ grid review".
	if r := byID["page-substr"]; r.Title != "EQ grid review" {
		t.Errorf("title not cleaned as expected: got %q, want %q", r.Title, "EQ grid review")
	}
	// 0x07 inside the url was stripped (not replaced).
	if r := byID["page-substr"]; r.URL != "https://notion.so/substr" {
		t.Errorf("control char in url not stripped: got %q", r.URL)
	}
	if r := byID["page-untitled"]; r.Title != "(untitled)" {
		t.Errorf("missing title fallback = %q, want (untitled)", r.Title)
	}
}

// TestRelevanceTiers locks the exact scores of the bash python relevance()
// port: exact > prefix > substring > all-tokens > some-tokens, and 0 on empty
// query.
func TestRelevanceTiers(t *testing.T) {
	cases := []struct {
		query, title string
		want         int
	}{
		{"grid", "grid", 100},
		{"grid", "Grid Operations", 80},
		{"grid", "the grid is here", 60},
		{"load grid", "grid under load conditions", 40}, // both tokens present, not substring of "load grid"
		{"load grid", "grid only here", 5},              // one of two tokens present
		{"load grid", "neither token", 0},
		{"", "anything at all", 0},
	}
	for _, tc := range cases {
		q := strings.ToLower(strings.TrimSpace(tc.query))
		got := relevance(q, strings.Fields(q), tc.title)
		if got != tc.want {
			t.Errorf("relevance(%q, %q) = %d, want %d", tc.query, tc.title, got, tc.want)
		}
	}
}

// TestSearchThroughClient drives the full Search path through a faked ntn exec,
// proving the request transport + tolerant decode + rerank compose end-to-end
// (no network). The fake ignores args and returns the fixture bytes.
func TestSearchThroughClient(t *testing.T) {
	c := newTestClient(fakeCmd{out: []byte(fixtureSearchJSON)})
	got, err := c.Search(context.Background(), "grid")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) == 0 || got[0].ID != "page-exact" {
		t.Fatalf("Search end-to-end ordering wrong: %+v", got)
	}
}

// TestSearchDecodesDatabaseResults guards the regression where any /v1/search
// result set containing a DATABASE crashed the whole decode: a database's title
// property is a schema config object ({}), not a rich-text array, so a fixed
// []richTextLite type failed to unmarshal and returned zero results + an error.
// The DB title must come from the top-level title array; the page title from its
// title property.
func TestSearchDecodesDatabaseResults(t *testing.T) {
	const mixed = `{"results":[
		{"object":"database","id":"db1","url":"http://n/db1",
		 "title":[{"plain_text":"Meeting Notes DB"}],
		 "properties":{"Name":{"type":"title","title":{}}}},
		{"object":"page","id":"pg1","url":"http://n/pg1",
		 "properties":{"Title":{"type":"title","title":[{"plain_text":"Meeting page"}]}}}
	]}`
	c := newTestClient(fakeCmd{out: []byte(mixed)})
	got, err := c.Search(context.Background(), "meeting")
	if err != nil {
		t.Fatalf("Search errored on a result set containing a database: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (db + page): %+v", len(got), got)
	}
	byID := map[string]Result{}
	for _, r := range got {
		byID[r.ID] = r
	}
	if byID["db1"].Title != "Meeting Notes DB" || byID["db1"].Kind != "database" {
		t.Errorf("database result wrong: %+v", byID["db1"])
	}
	if byID["pg1"].Title != "Meeting page" || byID["pg1"].Kind != "page" {
		t.Errorf("page result wrong: %+v", byID["pg1"])
	}
}

// TestSearchBody confirms the request body matches the bash shape: query,
// page_size 100, and the last_edited_time descending sort. It also confirms
// json.Marshal escapes a quote/backslash in the query (the bash hand-escaping).
func TestSearchBody(t *testing.T) {
	body, err := searchBody(`a"b\c`)
	if err != nil {
		t.Fatalf("searchBody: %v", err)
	}
	for _, want := range []string{
		`"query":"a\"b\\c"`,
		`"page_size":100`,
		`"direction":"descending"`,
		`"timestamp":"last_edited_time"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q missing %q", body, want)
		}
	}
}
