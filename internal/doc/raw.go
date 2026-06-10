// Package doc is the pure core of nui: the Document IR and (later) layout. It
// imports neither notion nor tui nor anything that touches a TTY or the network
// (SPEC §4 dependency rule). That purity is what makes Build golden-testable and
// keeps the reader fast.
//
// raw.go holds the on-the-wire DTOs (RawPage / RawBlock) exactly as Notion's API
// returns them. They live here, not in internal/notion, so that doc stays
// import-pure and notion can depend on doc (its tolerant decoder populates these)
// without doc ever depending back on notion.
package doc

import "encoding/json"

// RawBlock is one element of a /v1/blocks/{id}/children "results" array. Notion
// uses a tagged envelope: the Type field names a sibling key that carries the
// type-specific payload (e.g. Type=="paragraph" => the "paragraph" key holds the
// rich text). Build reads Type, then reaches into the matching payload.
//
// The payload is kept as json.RawMessage in a map keyed by type name rather than
// being eagerly decoded into N typed fields: it keeps this DTO small, lets Build
// decode lazily only the shape a given type needs, and gives BlockUnsupported a
// verbatim Raw to fall back to. Children is populated by notion.Blocks (the
// recursive fetch), not by the API in a single response.
type RawBlock struct {
	Object         string `json:"object"`
	ID             string `json:"id"`
	Type           string `json:"type"`
	HasChildren    bool   `json:"has_children"`
	CreatedTime    string `json:"created_time"`
	LastEditedTime string `json:"last_edited_time"`

	// Payload holds every top-level key of the block keyed by name. Build pulls
	// the entry named by Type. Decoding into a map (rather than embedding the
	// typed payload) lets one DTO carry every block type and keeps the verbatim
	// bytes available for the Raw fallback.
	Payload map[string]json.RawMessage `json:"-"`

	// Raw is the verbatim JSON of this block, captured during decode. Build
	// copies it onto Block.Raw for unsupported types so nothing is ever dropped.
	Raw json.RawMessage `json:"-"`

	// Children is the assembled subtree for a has_children block. notion.Blocks
	// fills this by recursively fetching /children; the API never returns it
	// inline. Pure-core tests populate it directly.
	Children []RawBlock `json:"-"`
}

// childrenKey is the synthetic JSON key under which RawBlock serializes its
// assembled subtree. The Notion API never returns children inline (they are
// fetched separately and assembled by notion.Blocks), so nui owns this key: it
// lets a fully-resolved tree round-trip through json.Marshal/Unmarshal for the
// SWR cache and for `nui dump --json` fixture capture. On decode, both this key
// and Notion's own absence-of-children are handled.
const childrenKey = "children"

// UnmarshalJSON captures the verbatim bytes (for the Raw fallback) and the full
// key/value map (so Build can pull the type-specific payload by name) in a single
// pass. It never errors on unexpected keys — unknown shapes flow through to the
// Payload map and the Raw bytes untouched, which is the whole point of the
// tolerant IR. A nui-synthesized "children" key (see childrenKey) is decoded back
// into Children so an assembled tree round-trips.
func (b *RawBlock) UnmarshalJSON(data []byte) error {
	// Keep a verbatim copy for the unsupported fallback.
	b.Raw = append(b.Raw[:0], data...)

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	// Pull the handful of scalar header fields we model directly.
	unmarshalString(m, "object", &b.Object)
	unmarshalString(m, "id", &b.ID)
	unmarshalString(m, "type", &b.Type)
	unmarshalString(m, "created_time", &b.CreatedTime)
	unmarshalString(m, "last_edited_time", &b.LastEditedTime)
	if v, ok := m["has_children"]; ok {
		_ = json.Unmarshal(v, &b.HasChildren)
	}

	// Decode an assembled subtree if present (cache / fixture round-trip), then
	// drop the synthetic key from the payload so it is not mistaken for a
	// Notion block field and does not re-serialize twice.
	if v, ok := m[childrenKey]; ok {
		_ = json.Unmarshal(v, &b.Children)
		delete(m, childrenKey)
	}
	b.Payload = m
	return nil
}

// MarshalJSON re-emits the block's original payload (every Notion key it was
// decoded from) plus the nui-synthesized "children" key carrying the assembled
// subtree. This makes a fully-resolved tree round-trip: Marshal -> bytes -> the
// tolerant Decode -> an equivalent tree. When there are no children the key is
// omitted, so a leaf block serializes byte-for-byte like the API returned it.
func (b RawBlock) MarshalJSON() ([]byte, error) {
	m := make(map[string]json.RawMessage, len(b.Payload)+1)
	for k, v := range b.Payload {
		m[k] = v
	}
	if len(b.Children) > 0 {
		kids, err := json.Marshal(b.Children)
		if err != nil {
			return nil, err
		}
		m[childrenKey] = kids
	}
	return json.Marshal(m)
}

// TypePayload returns the raw JSON of the type-specific payload (the value under
// the key named by Type), or nil if absent. This is the single accessor Build
// uses to reach a block's body regardless of type.
func (b RawBlock) TypePayload() json.RawMessage {
	if b.Payload == nil {
		return nil
	}
	return b.Payload[b.Type]
}

// RawPage mirrors a /v1/pages/{id} response: identity plus the metadata nui
// renders at the top of a document (title/icon/cover/properties). Block content
// is fetched separately via /v1/blocks (notion.Blocks); a page response carries
// only properties, not body blocks.
type RawPage struct {
	Object         string                     `json:"object"`
	ID             string                     `json:"id"`
	CreatedTime    string                     `json:"created_time"`
	LastEditedTime string                     `json:"last_edited_time"`
	Icon           json.RawMessage            `json:"icon"`
	Cover          json.RawMessage            `json:"cover"`
	Properties     map[string]json.RawMessage `json:"properties"`
	URL            string                     `json:"url"`

	// Title is set for database/data_source objects, which carry a top-level
	// title array instead of a "title"-typed property. For ordinary pages it is
	// empty and Build derives the title from the title property.
	Title json.RawMessage `json:"title"`
}

// unmarshalString decodes m[key] into dst if present, ignoring errors so a
// malformed scalar degrades to the zero value rather than failing the whole
// block (never panic on unexpected JSON, SPEC §5).
func unmarshalString(m map[string]json.RawMessage, key string, dst *string) {
	if v, ok := m[key]; ok {
		_ = json.Unmarshal(v, dst)
	}
}
