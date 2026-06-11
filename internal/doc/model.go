package doc

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Document is the typed IR for one Notion page (SPEC §5). It is a tagged-struct
// tree, not an interface hierarchy: trivial JSON mapping, a clean Raw fallback
// for the unhandled long tail, and easy golden serialization.
type Document struct {
	ID        string
	Title     string
	Icon      *Icon      // emoji, or external/file image (T3 for image icons)
	Cover     *Cover     // T3
	Props     []Property // page properties, rendered as a block at the top
	Blocks    []Block    // top-level tree; children nested in Block.Children
	FetchedAt time.Time
}

// Block carries the superset of fields; only those relevant to its Type are
// populated (SPEC §5). Unknown types become BlockUnsupported with Raw set.
type Block struct {
	ID       string
	Type     BlockType
	RichText []RichText // inline content (paragraph, heading, list item, …)
	Children []Block    // nested: toggle body, list nesting, column children, …

	// type-specific (populate per Type; zero otherwise):
	Checked     bool       // to_do
	Language    string     // code
	Color       string     // block / callout color (Notion color name)
	Icon        *Icon      // callout icon
	Caption     []RichText // image / code / bookmark caption
	URL         string     // bookmark / link_preview / embed / external image
	HeadingLvl  int        // heading_1..6 -> 1..6
	Collapsible bool       // toggle, toggleable heading

	// Ordinal is the 1-based position within a contiguous numbered-list run. It is
	// a layout hint set by Layout's walk (which sees sibling order), NOT decoded
	// from the API — the per-block renderer cannot count siblings on its own. 0
	// when unset / not a numbered item.
	Ordinal int

	// table:
	TableWidth      int
	HasColumnHeader bool
	HasRowHeader    bool
	Cells           [][]RichText // populated when Type == BlockTableRow

	// media (T3):
	File *FileRef

	// navigation (T2):
	TargetID string // child_page / child_database / link_to_page target id

	Raw json.RawMessage // verbatim API block for unhandled types
}

// BlockType is the closed enum of block kinds nui models (SPEC §5). Anything
// outside it maps to BlockUnsupported.
type BlockType string

const (
	BlockParagraph BlockType = "paragraph"
	BlockHeading1  BlockType = "heading_1"
	BlockHeading2  BlockType = "heading_2"
	BlockHeading3  BlockType = "heading_3"
	// Notion's documented API tops out at heading_3, but real pages carry
	// heading_4 (and occasionally deeper) — they were degrading to "unsupported".
	BlockHeading4      BlockType = "heading_4"
	BlockHeading5      BlockType = "heading_5"
	BlockHeading6      BlockType = "heading_6"
	BlockBulleted      BlockType = "bulleted_list_item"
	BlockNumbered      BlockType = "numbered_list_item"
	BlockToDo          BlockType = "to_do"
	BlockToggle        BlockType = "toggle"
	BlockQuote         BlockType = "quote"
	BlockCallout       BlockType = "callout"
	BlockCode          BlockType = "code"
	BlockDivider       BlockType = "divider"
	BlockTable         BlockType = "table"
	BlockTableRow      BlockType = "table_row"
	BlockColumnList    BlockType = "column_list"
	BlockColumn        BlockType = "column"
	BlockToC           BlockType = "table_of_contents"
	BlockChildPage     BlockType = "child_page"
	BlockChildDatabase BlockType = "child_database"
	BlockLinkToPage    BlockType = "link_to_page"
	BlockBookmark      BlockType = "bookmark"
	BlockLinkPreview   BlockType = "link_preview"
	BlockImage         BlockType = "image"        // T3
	BlockVideo         BlockType = "video"        // T3
	BlockFile          BlockType = "file"         // T3
	BlockPDF           BlockType = "pdf"          // T3
	BlockEquation      BlockType = "equation"     // T3 (raw LaTeX text)
	BlockSyncedBlock   BlockType = "synced_block" // T3
	BlockUnsupported   BlockType = "unsupported"  // anything not handled -> Raw
)

// knownTypes is the set of BlockTypes Build maps to first-class IR. A raw type
// not present here becomes BlockUnsupported. Keeping it as data (not a switch
// with a default) lets nui inventory report coverage against the same source.
var knownTypes = map[string]BlockType{
	"paragraph":          BlockParagraph,
	"heading_1":          BlockHeading1,
	"heading_2":          BlockHeading2,
	"heading_3":          BlockHeading3,
	"heading_4":          BlockHeading4,
	"heading_5":          BlockHeading5,
	"heading_6":          BlockHeading6,
	"bulleted_list_item": BlockBulleted,
	"numbered_list_item": BlockNumbered,
	"to_do":              BlockToDo,
	"toggle":             BlockToggle,
	"quote":              BlockQuote,
	"callout":            BlockCallout,
	"code":               BlockCode,
	"divider":            BlockDivider,
	"table":              BlockTable,
	"table_row":          BlockTableRow,
	"column_list":        BlockColumnList,
	"column":             BlockColumn,
	"table_of_contents":  BlockToC,
	"child_page":         BlockChildPage,
	"child_database":     BlockChildDatabase,
	"link_to_page":       BlockLinkToPage,
	"bookmark":           BlockBookmark,
	"link_preview":       BlockLinkPreview,
	"image":              BlockImage,
	"video":              BlockVideo,
	"file":               BlockFile,
	"pdf":                BlockPDF,
	"equation":           BlockEquation,
	"synced_block":       BlockSyncedBlock,
}

// KnownBlockType reports whether a raw type name maps to first-class IR (i.e.
// not BlockUnsupported). Exported so nui inventory classifies coverage with the
// exact same table Build uses.
func KnownBlockType(rawType string) bool {
	_, ok := knownTypes[rawType]
	return ok
}

// RichText is one inline span (SPEC §6). Mention is nil unless the span is a
// page/user/date/database mention.
type RichText struct {
	Text    string
	Href    string // link target; "" = none
	Ann     Annotations
	Mention *Mention // T2: nil unless this span is a mention
}

// Annotations are the inline style flags plus the Notion color name.
type Annotations struct {
	Bold, Italic, Strikethrough, Underline, Code bool
	Color                                        string // Notion color name
}

// Mention is the payload of a mention span (SPEC §6).
type Mention struct {
	Kind     string // "page" | "user" | "date" | "database"
	TargetID string // page/db id for nav (T2)
	Label    string // display text resolved by the API where possible
}

// Icon is an emoji or an image URL — exactly one is set.
type Icon struct {
	Emoji string
	URL   string
}

// Cover is a page cover image URL (T3).
type Cover struct {
	URL string
}

// FileRef is a media reference. Notion-hosted URLs expire, so Expiry is captured
// at fetch time and must not be treated as permanent (G1 risk note, SPEC §5).
type FileRef struct {
	URL     string
	Caption string
	Expiry  time.Time
}

// Property is one page property rendered as rich text at the top of the document.
type Property struct {
	Name  string
	Kind  string
	Value []RichText
}

// Build maps a RawPage and its (already recursively assembled) block tree into
// the typed Document. It is the only place the API's {type, <type>:{...}} envelope
// becomes the flat tagged IR (SPEC §5). It never panics on unexpected JSON: a
// malformed payload degrades to empty fields, and an unknown type degrades to
// BlockUnsupported with Raw populated.
func Build(p RawPage, blocks []RawBlock) *Document {
	d := &Document{
		ID:        normalizeID(p.ID),
		FetchedAt: time.Now(),
	}
	d.Title = buildTitle(p)
	d.Icon = buildIcon(p.Icon)
	d.Cover = buildCover(p.Cover)
	d.Props = buildProps(p.Properties)
	d.Blocks = buildBlocks(blocks)
	return d
}

// buildBlocks maps a slice of raw blocks, recursing children. A nil/empty input
// yields a nil slice (not an empty non-nil one) so golden output is stable.
func buildBlocks(raw []RawBlock) []Block {
	if len(raw) == 0 {
		return nil
	}
	out := make([]Block, 0, len(raw))
	for i := range raw {
		out = append(out, buildBlock(raw[i]))
	}
	return out
}

// buildBlock maps one raw block. Unknown types short-circuit to the Raw fallback
// before any payload decode is attempted.
func buildBlock(rb RawBlock) Block {
	bt, known := knownTypes[rb.Type]
	if !known {
		return Block{
			ID:       normalizeID(rb.ID),
			Type:     BlockUnsupported,
			Raw:      rb.Raw,
			Children: buildBlocks(rb.Children),
		}
	}

	b := Block{ID: normalizeID(rb.ID), Type: bt}
	payload := rb.TypePayload()

	switch bt {
	case BlockParagraph, BlockQuote,
		BlockBulleted, BlockNumbered,
		BlockToggle:
		b.RichText = decodeRichText(payload, "rich_text")
		b.Color = decodeColor(payload)
		if bt == BlockToggle {
			b.Collapsible = true
		}

	case BlockHeading1, BlockHeading2, BlockHeading3,
		BlockHeading4, BlockHeading5, BlockHeading6:
		b.RichText = decodeRichText(payload, "rich_text")
		b.Color = decodeColor(payload)
		b.HeadingLvl = headingLevel(bt)
		b.Collapsible = decodeBool(payload, "is_toggleable")

	case BlockToDo:
		b.RichText = decodeRichText(payload, "rich_text")
		b.Color = decodeColor(payload)
		b.Checked = decodeBool(payload, "checked")

	case BlockCallout:
		b.RichText = decodeRichText(payload, "rich_text")
		b.Color = decodeColor(payload)
		b.Icon = decodeIcon(payload, "icon")

	case BlockCode:
		b.RichText = decodeRichText(payload, "rich_text")
		b.Caption = decodeRichText(payload, "caption")
		b.Language = decodeString(payload, "language")

	case BlockTable:
		b.TableWidth = decodeInt(payload, "table_width")
		b.HasColumnHeader = decodeBool(payload, "has_column_header")
		b.HasRowHeader = decodeBool(payload, "has_row_header")

	case BlockTableRow:
		b.Cells = decodeCells(payload)

	case BlockChildPage, BlockChildDatabase:
		// child_page/child_database carry a title; the navigable target is the
		// block's own id (SPEC §5 TargetID).
		b.RichText = plainTitle(decodeString(payload, "title"))
		b.TargetID = normalizeID(rb.ID)

	case BlockLinkToPage:
		b.TargetID = decodeLinkTarget(payload)

	case BlockBookmark, BlockLinkPreview:
		b.URL = decodeString(payload, "url")
		b.Caption = decodeRichText(payload, "caption")

	case BlockImage, BlockVideo, BlockFile, BlockPDF:
		b.File = decodeFileRef(payload)
		if b.File != nil {
			b.URL = b.File.URL
			b.Caption = decodeRichText(payload, "caption")
		}

	case BlockEquation:
		// equation carries its LaTeX in "expression"; surface it as rich text so
		// the (T3) renderer can show the raw source without a special field.
		b.RichText = plainTitle(decodeString(payload, "expression"))

	case BlockColumnList, BlockColumn, BlockDivider, BlockToC, BlockSyncedBlock:
		// structural / no inline payload of their own; meaning is in Children
		// (column_list/column/synced_block) or purely positional (divider/toc).
		if bt == BlockColumnList || bt == BlockColumn || bt == BlockToggle {
			b.Collapsible = bt == BlockToggle
		}
	}

	b.Children = buildBlocks(rb.Children)
	return b
}

func headingLevel(bt BlockType) int {
	switch bt {
	case BlockHeading1:
		return 1
	case BlockHeading2:
		return 2
	case BlockHeading3:
		return 3
	case BlockHeading4:
		return 4
	case BlockHeading5:
		return 5
	case BlockHeading6:
		return 6
	default:
		return 0
	}
}

// --- payload decoders ----------------------------------------------------
//
// Each tolerates a nil/empty payload and a wrong-shaped value by returning the
// zero value. They never error out: Build must not panic on unexpected JSON.

type rawRichText struct {
	Type        string `json:"type"`
	PlainText   string `json:"plain_text"`
	Href        string `json:"href"`
	Annotations struct {
		Bold          bool   `json:"bold"`
		Italic        bool   `json:"italic"`
		Strikethrough bool   `json:"strikethrough"`
		Underline     bool   `json:"underline"`
		Code          bool   `json:"code"`
		Color         string `json:"color"`
	} `json:"annotations"`
	Mention json.RawMessage `json:"mention"`
}

func decodeRichText(payload json.RawMessage, key string) []RichText {
	field := nestedRaw(payload, key)
	if field == nil {
		return nil
	}
	var raw []rawRichText
	if err := json.Unmarshal(field, &raw); err != nil {
		return nil
	}
	out := make([]RichText, 0, len(raw))
	for _, r := range raw {
		rt := RichText{
			Text: r.PlainText,
			Href: r.Href,
			Ann: Annotations{
				Bold:          r.Annotations.Bold,
				Italic:        r.Annotations.Italic,
				Strikethrough: r.Annotations.Strikethrough,
				Underline:     r.Annotations.Underline,
				Code:          r.Annotations.Code,
				Color:         r.Annotations.Color,
			},
		}
		if r.Type == "mention" {
			rt.Mention = decodeMention(r.Mention, r.PlainText)
		}
		out = append(out, rt)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// decodeMention maps a mention payload to the IR Mention, resolving the target
// id by mention kind. Label falls back to the span's plain_text (Notion usually
// pre-resolves the display text).
func decodeMention(raw json.RawMessage, label string) *Mention {
	if len(raw) == 0 {
		return nil
	}
	var m struct {
		Type     string          `json:"type"`
		Page     json.RawMessage `json:"page"`
		Database json.RawMessage `json:"database"`
		User     json.RawMessage `json:"user"`
		Date     json.RawMessage `json:"date"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	men := &Mention{Kind: m.Type, Label: label}
	switch m.Type {
	case "page":
		men.TargetID = normalizeID(decodeString(m.Page, "id"))
	case "database":
		men.TargetID = normalizeID(decodeString(m.Database, "id"))
	case "user":
		men.TargetID = normalizeID(decodeString(m.User, "id"))
	}
	return men
}

// decodeCells maps a table_row's "cells": [][]richtext.
func decodeCells(payload json.RawMessage) [][]RichText {
	field := nestedRaw(payload, "cells")
	if field == nil {
		return nil
	}
	var rawCells [][]rawRichText
	if err := json.Unmarshal(field, &rawCells); err != nil {
		return nil
	}
	out := make([][]RichText, 0, len(rawCells))
	for _, cell := range rawCells {
		row := make([]RichText, 0, len(cell))
		for _, r := range cell {
			row = append(row, RichText{
				Text: r.PlainText,
				Href: r.Href,
				Ann: Annotations{
					Bold:          r.Annotations.Bold,
					Italic:        r.Annotations.Italic,
					Strikethrough: r.Annotations.Strikethrough,
					Underline:     r.Annotations.Underline,
					Code:          r.Annotations.Code,
					Color:         r.Annotations.Color,
				},
			})
		}
		out = append(out, row)
	}
	return out
}

// decodeFileRef maps a file/external media payload, capturing the expiry of a
// Notion-hosted URL (these expire — G1 risk note). Returns nil when no URL is
// present (this workspace strips file URLs on some images, leaving an empty
// payload — degrade to nil rather than a bogus FileRef).
func decodeFileRef(payload json.RawMessage) *FileRef {
	if len(payload) == 0 {
		return nil
	}
	var f struct {
		Type string `json:"type"`
		File struct {
			URL        string `json:"url"`
			ExpiryTime string `json:"expiry_time"`
		} `json:"file"`
		External struct {
			URL string `json:"url"`
		} `json:"external"`
	}
	if err := json.Unmarshal(payload, &f); err != nil {
		return nil
	}
	ref := &FileRef{}
	switch f.Type {
	case "file":
		ref.URL = f.File.URL
		ref.Expiry = parseTime(f.File.ExpiryTime)
	case "external":
		ref.URL = f.External.URL
	default:
		// type unknown/absent: take whichever url is present.
		if f.File.URL != "" {
			ref.URL = f.File.URL
			ref.Expiry = parseTime(f.File.ExpiryTime)
		} else if f.External.URL != "" {
			ref.URL = f.External.URL
		}
	}
	if ref.URL == "" {
		return nil
	}
	return ref
}

// decodeLinkTarget pulls the target id from a link_to_page payload, which is one
// of {page_id|database_id|...}: <id>.
func decodeLinkTarget(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	for _, key := range []string{"page_id", "database_id", "data_source_id"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return normalizeID(s)
			}
		}
	}
	return ""
}

// --- icon / cover / title / props ----------------------------------------

func buildIcon(raw json.RawMessage) *Icon {
	return decodeIconValue(raw)
}

// decodeIcon pulls an icon nested under key in payload (callout icon).
func decodeIcon(payload json.RawMessage, key string) *Icon {
	return decodeIconValue(nestedRaw(payload, key))
}

// decodeIconValue maps an icon value, which is one of:
//
//	{type:"emoji", emoji:"📝"}
//	{type:"external", external:{url}}
//	{type:"file", file:{url}}
//	{type:"icon", icon:{name,color}}   (Notion's built-in named icons)
func decodeIconValue(raw json.RawMessage) *Icon {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v struct {
		Type     string `json:"type"`
		Emoji    string `json:"emoji"`
		External struct {
			URL string `json:"url"`
		} `json:"external"`
		File struct {
			URL string `json:"url"`
		} `json:"file"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	switch v.Type {
	case "emoji":
		if v.Emoji == "" {
			return nil
		}
		return &Icon{Emoji: v.Emoji}
	case "external":
		if v.External.URL == "" {
			return nil
		}
		return &Icon{URL: v.External.URL}
	case "file":
		if v.File.URL == "" {
			return nil
		}
		return &Icon{URL: v.File.URL}
	default:
		// Built-in named icons ("icon" type) and anything else have no
		// emoji/url we can render; degrade to nil rather than a blank icon.
		return nil
	}
}

func buildCover(raw json.RawMessage) *Cover {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v struct {
		Type     string `json:"type"`
		External struct {
			URL string `json:"url"`
		} `json:"external"`
		File struct {
			URL string `json:"url"`
		} `json:"file"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	url := v.External.URL
	if url == "" {
		url = v.File.URL
	}
	if url == "" {
		return nil
	}
	return &Cover{URL: url}
}

// buildTitle resolves a page/database title. A database/data_source carries a
// top-level "title" array; an ordinary page carries a "title"-typed property.
func buildTitle(p RawPage) string {
	if len(p.Title) > 0 && string(p.Title) != "null" {
		var rts []rawRichText
		if json.Unmarshal(p.Title, &rts) == nil {
			if s := joinPlain(rts); s != "" {
				return s
			}
		}
	}
	for _, raw := range p.Properties {
		var pv struct {
			Type  string        `json:"type"`
			Title []rawRichText `json:"title"`
		}
		if json.Unmarshal(raw, &pv) != nil {
			continue
		}
		if pv.Type == "title" {
			return joinPlain(pv.Title)
		}
	}
	return ""
}

// buildProps maps page properties into renderable rich text. The title property
// is omitted (it is surfaced as Document.Title). Property iteration order from a
// Go map is unstable, so properties are sorted by name for deterministic golden
// output.
func buildProps(props map[string]json.RawMessage) []Property {
	if len(props) == 0 {
		return nil
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Property, 0, len(props))
	for _, name := range names {
		raw := props[name]
		kind, value := decodeProperty(raw)
		if kind == "title" {
			continue // surfaced as Document.Title
		}
		if len(value) == 0 {
			continue // empty property: skip rather than render a blank row
		}
		out = append(out, Property{Name: name, Kind: kind, Value: value})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// decodeProperty renders one property value as rich text by its type. It mirrors
// the bash _datasource_md cell() logic for the common property kinds; unknown
// kinds degrade to empty (no row).
func decodeProperty(raw json.RawMessage) (kind string, value []RichText) {
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return "", nil
	}
	kind = head.Type

	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return kind, nil
	}
	body := m[kind]

	var text string
	switch kind {
	case "title", "rich_text":
		var rts []rawRichText
		if json.Unmarshal(body, &rts) == nil {
			text = joinPlain(rts)
		}
	case "select", "status":
		text = decodeString(body, "name")
	case "multi_select":
		text = joinNamed(body)
	case "people":
		text = joinNamed(body)
	case "date":
		text = decodeDate(body)
	case "number":
		if body != nil && string(body) != "null" {
			text = strings.TrimSpace(string(body))
		}
	case "checkbox":
		var b bool
		if json.Unmarshal(body, &b) == nil && b {
			text = "x"
		}
	case "url", "email", "phone_number":
		_ = json.Unmarshal(body, &text)
	case "unique_id":
		text = decodeUniqueID(body)
	default:
		// relation, formula, rollup, created_by, etc. — not rendered in G1.
		text = ""
	}
	if text == "" {
		return kind, nil
	}
	return kind, plainTitle(text)
}

func decodeDate(body json.RawMessage) string {
	if len(body) == 0 || string(body) == "null" {
		return ""
	}
	var d struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if json.Unmarshal(body, &d) != nil {
		return ""
	}
	if d.Start == "" {
		return ""
	}
	if d.End != "" {
		return d.Start + " → " + d.End
	}
	return d.Start
}

func decodeUniqueID(body json.RawMessage) string {
	var u struct {
		Prefix string `json:"prefix"`
		Number *int   `json:"number"`
	}
	if json.Unmarshal(body, &u) != nil || u.Number == nil {
		return ""
	}
	pre := ""
	if u.Prefix != "" {
		pre = u.Prefix + "-"
	}
	return pre + strconv.Itoa(*u.Number)
}

// joinNamed joins the "name" field of an array of {name:...} objects (people,
// multi_select).
func joinNamed(body json.RawMessage) string {
	var arr []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(body, &arr) != nil {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, o := range arr {
		if o.Name != "" {
			parts = append(parts, o.Name)
		}
	}
	return strings.Join(parts, ", ")
}

// --- small shared helpers -------------------------------------------------

// nestedRaw returns payload[key] without fully decoding payload.
func nestedRaw(payload json.RawMessage, key string) json.RawMessage {
	if len(payload) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(payload, &m) != nil {
		return nil
	}
	return m[key]
}

func decodeString(payload json.RawMessage, key string) string {
	v := nestedRaw(payload, key)
	if v == nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(v, &s)
	return s
}

func decodeBool(payload json.RawMessage, key string) bool {
	v := nestedRaw(payload, key)
	if v == nil {
		return false
	}
	var b bool
	_ = json.Unmarshal(v, &b)
	return b
}

func decodeInt(payload json.RawMessage, key string) int {
	v := nestedRaw(payload, key)
	if v == nil {
		return 0
	}
	var n int
	_ = json.Unmarshal(v, &n)
	return n
}

// decodeColor reads a block's "color" payload field, defaulting to "default".
func decodeColor(payload json.RawMessage) string {
	c := decodeString(payload, "color")
	if c == "" {
		return "default"
	}
	return c
}

func joinPlain(rts []rawRichText) string {
	var b strings.Builder
	for _, r := range rts {
		b.WriteString(r.PlainText)
	}
	return strings.TrimSpace(b.String())
}

// plainTitle wraps a plain string as a single default-styled RichText span, or
// nil when empty.
func plainTitle(s string) []RichText {
	if s == "" {
		return nil
	}
	return []RichText{{Text: s, Ann: Annotations{Color: "default"}}}
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// normalizeID strips dashes from a Notion id for stable comparison/display. The
// API accepts both dashed and undashed forms; nui stores the compact form.
func normalizeID(id string) string {
	return strings.ReplaceAll(id, "-", "")
}
