package doc

import (
	"encoding/json"
	"testing"
)

// rawBlockFromJSON decodes a single block's JSON into a RawBlock via its custom
// UnmarshalJSON (the same path notion.Blocks uses).
func rawBlockFromJSON(t *testing.T, s string) RawBlock {
	t.Helper()
	var rb RawBlock
	if err := json.Unmarshal([]byte(s), &rb); err != nil {
		t.Fatalf("unmarshal raw block: %v", err)
	}
	return rb
}

// TestBuildNeverPanicsOnGarbage feeds Build a variety of malformed / surprising
// payloads and asserts it returns without panicking, degrading to empty/raw
// rather than crashing (SPEC §5: never panic on unexpected JSON).
func TestBuildNeverPanicsOnGarbage(t *testing.T) {
	cases := []string{
		`{"type":"paragraph"}`,                                  // missing payload
		`{"type":"paragraph","paragraph":null}`,                 // null payload
		`{"type":"paragraph","paragraph":{"rich_text":"oops"}}`, // wrong-typed rich_text
		`{"type":"heading_1","heading_1":{"rich_text":[]}}`,     // empty rich_text
		`{"type":"to_do","to_do":{"checked":"yes"}}`,            // wrong-typed checked
		`{"type":"table","table":{"table_width":"six"}}`,        // wrong-typed int
		`{"type":"callout","callout":{"icon":12345}}`,           // wrong-typed icon
		`{"type":"image","image":{}}`,                           // empty media -> nil File
		`{"type":"weird_future_block","weird_future_block":{}}`, // unknown -> unsupported
		`{}`,                                  // no type at all
		`{"type":"table_row","table_row":{}}`, // missing cells
	}
	for _, c := range cases {
		rb := rawBlockFromJSON(t, c)
		// Build must not panic.
		d := Build(RawPage{}, []RawBlock{rb})
		if len(d.Blocks) != 1 {
			t.Errorf("case %q: expected 1 block, got %d", c, len(d.Blocks))
		}
	}
}

// TestUnknownTypeIsRawUnsupported asserts an unknown type maps to
// BlockUnsupported with the verbatim bytes preserved in Raw.
func TestUnknownTypeIsRawUnsupported(t *testing.T) {
	rb := rawBlockFromJSON(t, `{"id":"abc","type":"mystery","mystery":{"x":1}}`)
	d := Build(RawPage{}, []RawBlock{rb})
	b := d.Blocks[0]
	if b.Type != BlockUnsupported {
		t.Fatalf("type = %s, want unsupported", b.Type)
	}
	if len(b.Raw) == 0 {
		t.Fatal("Raw not preserved for unsupported block")
	}
	// Raw must round-trip back to the original object.
	var m map[string]any
	if err := json.Unmarshal(b.Raw, &m); err != nil {
		t.Fatalf("Raw is not valid JSON: %v", err)
	}
	if m["type"] != "mystery" {
		t.Errorf("Raw lost the type field: %v", m)
	}
}

// TestRawBlockRoundTrip verifies a fully-assembled tree (with synthesized
// children) survives Marshal -> Unmarshal, which is what the SWR cache and
// `nui dump --json` fixture capture rely on.
func TestRawBlockRoundTrip(t *testing.T) {
	parent := rawBlockFromJSON(t, `{"id":"p","type":"toggle","has_children":true,"toggle":{"rich_text":[{"type":"text","plain_text":"Top"}]}}`)
	child := rawBlockFromJSON(t, `{"id":"c","type":"paragraph","paragraph":{"rich_text":[{"type":"text","plain_text":"Body"}]}}`)
	parent.Children = []RawBlock{child}

	encoded, err := json.Marshal([]RawBlock{parent})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded []RawBlock
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The rebuilt Document must match the original's Dump.
	want := Dump(Build(RawPage{}, []RawBlock{parent}))
	got := Dump(Build(RawPage{}, decoded))
	if want != got {
		t.Errorf("round-trip changed the Document.\n--- before ---\n%s\n--- after ---\n%s", want, got)
	}
}

// TestBuildTitleFromProperty checks an ordinary page's title comes from its
// "title"-typed property and is excluded from the rendered Props.
func TestBuildTitleFromProperty(t *testing.T) {
	page := RawPage{
		ID: "page1",
		Properties: map[string]json.RawMessage{
			"Name":   json.RawMessage(`{"type":"title","title":[{"type":"text","plain_text":"My Page"}]}`),
			"Status": json.RawMessage(`{"type":"select","select":{"name":"Done"}}`),
		},
	}
	d := Build(page, nil)
	if d.Title != "My Page" {
		t.Errorf("Title = %q, want %q", d.Title, "My Page")
	}
	for _, p := range d.Props {
		if p.Kind == "title" {
			t.Errorf("title property leaked into Props: %+v", p)
		}
	}
	// The select property should render.
	var sawStatus bool
	for _, p := range d.Props {
		if p.Name == "Status" {
			sawStatus = true
			if richTextString(p.Value) != "Done" {
				t.Errorf("Status value = %q, want Done", richTextString(p.Value))
			}
		}
	}
	if !sawStatus {
		t.Error("Status property not rendered")
	}
}
