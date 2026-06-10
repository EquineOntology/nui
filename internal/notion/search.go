package notion

// search.go is the faithful Go port of the bash `nui` `_search` helper
// (SPEC §7, G2 deliverable 6). Notion's /v1/search ranks by an opaque relevance
// score that buries exact-title matches under noise, returns invalid JSON
// (unescaped control chars from some pages), and reports databases as
// "data_source"/"database" objects. So this port:
//
//  1. asks the API to sort by last_edited_time descending (recent things you
//     actually touch rank higher than stale noise),
//  2. parses with the tolerant Decode path (the Go equivalent of Python's
//     json.loads(strict=False)),
//  3. dedupes by id,
//  4. re-ranks client-side by how well each title matches the query, floating
//     real matches to the top.
//
// This logic is correct and hard-won — it is ported, not redesigned. The rerank
// is a pure function (rerank) so it is golden-testable over a fixture response
// with no network. Wiring into the TUI/main.go is the tui track's job; this file
// only provides notion.Search + Result.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// searchPageSize matches the bash request body's "page_size":100.
const searchPageSize = 100

// Result is one search hit, already title-extracted, cleaned, and classified.
// It is a notion-internal display shape (not part of the pure Document IR), so
// it lives in the notion package. The fields mirror the bash output columns:
// id<TAB>title<TAB>url<TAB>kind.
type Result struct {
	ID    string // Notion object id (as returned by the API; ntn accepts either form)
	Title string // best-effort page/database title, control-chars cleaned, "(untitled)" fallback
	URL   string // public URL, control-chars stripped
	Kind  string // "database" for database/data_source objects, else "page"
}

// rawSearchResponse is the lenient shape we decode /v1/search into. Only the
// fields the rerank needs are modeled; everything else is ignored. Title lives
// in two different places depending on object kind (see extractTitle), so both
// are captured as raw maps and walked tolerantly.
type rawSearchResponse struct {
	Results []rawSearchResult `json:"results"`
}

type rawSearchResult struct {
	ID     string `json:"id"`
	Object string `json:"object"` // "page" | "database" | "data_source"
	URL    string `json:"url"`
	// Title array for database/data_source objects: [{plain_text:...}, ...].
	Title []richTextLite `json:"title"`
	// Page title lives under whichever property has type=="title".
	Properties map[string]rawProperty `json:"properties"`
}

type rawProperty struct {
	Type string `json:"type"`
	// Title is RawMessage, not []richTextLite, because its JSON shape depends on
	// the object kind: on a PAGE the title property is an array of rich text
	// ([{plain_text:...}]), but on a DATABASE the same property is a schema config
	// object ({}). Decoding the whole /v1/search response with a fixed []slice
	// type blows up the instant any database appears in the results (it does, for
	// most queries) — so we keep it raw and parse leniently in extractTitle.
	Title json.RawMessage `json:"title"`
}

// richTextLite captures just the plain_text we concatenate for a title.
type richTextLite struct {
	PlainText string `json:"plain_text"`
}

// Search runs Notion's /v1/search for query, sorted by last_edited_time desc,
// then reranks the results client-side by title relevance (SPEC §7). It honors
// ctx cancellation (the underlying ntn call does). An empty query is sent as-is
// to the API — the recents fallback for an empty query lives in the TUI/main
// layer (state.recents), not here, matching the package boundary (this file
// must not import state).
func (c *Client) Search(ctx context.Context, query string) ([]Result, error) {
	body, err := searchBody(query)
	if err != nil {
		return nil, err
	}
	var resp rawSearchResponse
	if err := c.API(ctx, "/v1/search", body, &resp); err != nil {
		return nil, err
	}
	return rerank(query, resp), nil
}

// searchBody builds the /v1/search request body. The query is JSON-encoded
// (json.Marshal handles backslash/quote escaping the bash version did by hand),
// and the sort mirrors the bash body exactly.
func searchBody(query string) (string, error) {
	type sortSpec struct {
		Direction string `json:"direction"`
		Timestamp string `json:"timestamp"`
	}
	type reqBody struct {
		Query    string   `json:"query"`
		PageSize int      `json:"page_size"`
		Sort     sortSpec `json:"sort"`
	}
	b, err := json.Marshal(reqBody{
		Query:    query,
		PageSize: searchPageSize,
		Sort:     sortSpec{Direction: "descending", Timestamp: "last_edited_time"},
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// scoredResult pairs a Result with its relevance score and original API index,
// so the sort can be by score desc with ties broken by API order (recency).
type scoredResult struct {
	score int
	idx   int
	res   Result
}

// rerank is the pure heart of the port: given the query and a decoded search
// response, it dedupes by id, extracts+cleans each title, classifies kind, and
// orders by title relevance (descending), preserving API order (recency) within
// a score tier. It is a pure function of its inputs — no network, no TTY — so
// the rerank ordering is golden-testable.
func rerank(query string, resp rawSearchResponse) []Result {
	q := strings.ToLower(strings.TrimSpace(query))
	qtokens := strings.Fields(q)

	seen := make(map[string]bool, len(resp.Results))
	scored := make([]scoredResult, 0, len(resp.Results))
	for i, r := range resp.Results {
		if r.ID == "" || seen[r.ID] {
			continue
		}
		seen[r.ID] = true

		isDB := r.Object == "database" || r.Object == "data_source"
		title := cleanTitle(extractTitle(r, isDB))
		kind := "page"
		if isDB {
			kind = "database"
		}
		scored = append(scored, scoredResult{
			score: relevance(q, qtokens, title),
			idx:   i,
			res: Result{
				ID:    r.ID,
				Title: title,
				URL:   cleanControl(r.URL),
				Kind:  kind,
			},
		})
	}

	// Sort by score desc; ties keep the API order (recency). sort.SliceStable
	// would also work, but an explicit idx tiebreak documents the intent and is
	// robust regardless of the sort's stability.
	sort.Slice(scored, func(a, b int) bool {
		if scored[a].score != scored[b].score {
			return scored[a].score > scored[b].score
		}
		return scored[a].idx < scored[b].idx
	})

	out := make([]Result, len(scored))
	for i, s := range scored {
		out[i] = s.res
	}
	return out
}

// extractTitle pulls the title for a result. Database/data_source objects carry
// a top-level title array; pages carry it under the property whose type is
// "title" (the bash python filter's exact shape). Returns the raw concatenated
// plain_text (cleaning happens in cleanTitle).
func extractTitle(r rawSearchResult, isDB bool) string {
	if isDB {
		return concatPlain(r.Title)
	}
	for _, v := range r.Properties {
		if v.Type == "title" {
			var rt []richTextLite
			// Tolerate the database/schema shape ({} or null): only a page's title
			// property is an array, and only pages reach this branch in practice.
			_ = json.Unmarshal(v.Title, &rt)
			return concatPlain(rt)
		}
	}
	return ""
}

// concatPlain joins the plain_text of a rich-text array, the way the bash
// "".join(... plain_text ...) does.
func concatPlain(rt []richTextLite) string {
	if len(rt) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, t := range rt {
		sb.WriteString(t.PlainText)
	}
	return sb.String()
}

// relevance scores how well a page title matches the query, mirroring the bash
// python relevance() exactly: exact(100) > prefix(80) > substring(60) >
// all-tokens(40) > some-tokens(5 each). An empty query scores 0 for everything
// (the empty-query case is handled upstream as a recents view).
func relevance(q string, qtokens []string, title string) int {
	if q == "" {
		return 0
	}
	t := strings.ToLower(title)
	switch {
	case t == q:
		return 100
	case strings.HasPrefix(t, q):
		return 80
	case strings.Contains(t, q):
		return 60
	}
	if len(qtokens) > 0 {
		all := true
		for _, tok := range qtokens {
			if !strings.Contains(t, tok) {
				all = false
				break
			}
		}
		if all {
			return 40
		}
	}
	sum := 0
	for _, tok := range qtokens {
		if strings.Contains(t, tok) {
			sum += 5
		}
	}
	return sum
}

// cleanTitle reproduces the bash title cleanup: replace control chars (and tabs)
// with spaces, trim, and fall back to "(untitled)" when nothing is left. Note
// this differs from cleanControl (used for URLs) which DROPS controls rather
// than replacing them — the bash filter does the same (titles → space-replace +
// "(untitled)"; urls → strip).
func cleanTitle(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if r < ' ' { // includes tab; bash replaces tab with space too
			sb.WriteRune(' ')
		} else {
			sb.WriteRune(r)
		}
	}
	cleaned := strings.TrimSpace(sb.String())
	if cleaned == "" {
		return "(untitled)"
	}
	return cleaned
}

// cleanControl strips control characters (< 0x20) entirely, matching the bash
// url cleanup ("".join(c for c in url if c >= " ")).
func cleanControl(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if r >= ' ' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
