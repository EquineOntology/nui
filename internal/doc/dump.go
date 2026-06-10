package doc

import (
	"fmt"
	"strings"
)

// snippetLen bounds the text excerpt shown per block in the dump, so a dump of a
// prose-heavy page stays scannable.
const snippetLen = 60

// Dump renders a Document as a deterministic, indented text tree: one line per
// block as "type  "snippet"  [flags]", nested by depth. It is the human parity
// check behind `nui dump <id>` AND the golden-test serialization (text + type +
// nesting, never raw ANSI — SPEC §9). Being pure and stable is the whole point:
// two runs over the same fixture must byte-match.
func Dump(d *Document) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Document %s\n", d.ID)
	if d.Title != "" {
		fmt.Fprintf(&b, "  title: %s\n", d.Title)
	}
	if d.Icon != nil {
		fmt.Fprintf(&b, "  icon: %s\n", iconString(d.Icon))
	}
	if d.Cover != nil {
		fmt.Fprintf(&b, "  cover: %s\n", d.Cover.URL)
	}
	for _, p := range d.Props {
		fmt.Fprintf(&b, "  prop[%s/%s]: %s\n", p.Name, p.Kind, snippet(richTextString(p.Value)))
	}
	b.WriteString("---\n")
	dumpBlocks(&b, d.Blocks, 0)
	return b.String()
}

func dumpBlocks(b *strings.Builder, blocks []Block, depth int) {
	for i := range blocks {
		dumpBlock(b, blocks[i], depth)
	}
}

func dumpBlock(b *strings.Builder, blk Block, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s%s", indent, blk.Type)

	if text := richTextString(blk.RichText); text != "" {
		fmt.Fprintf(b, "  %q", snippet(text))
	}

	if flags := blockFlags(blk); flags != "" {
		fmt.Fprintf(b, "  [%s]", flags)
	}
	b.WriteByte('\n')

	// table_row cells are rendered inline (they have no child blocks).
	if blk.Type == BlockTableRow {
		for _, cell := range blk.Cells {
			fmt.Fprintf(b, "%s  | %s\n", indent, snippet(richTextString(cell)))
		}
	}

	dumpBlocks(b, blk.Children, depth+1)
}

// blockFlags formats the salient type-specific fields of a block for the dump,
// so the parity check shows checked/lang/color/url/target without dumping the
// whole struct.
func blockFlags(blk Block) string {
	var parts []string
	switch blk.Type {
	case BlockToDo:
		if blk.Checked {
			parts = append(parts, "checked")
		} else {
			parts = append(parts, "unchecked")
		}
	case BlockCode:
		if blk.Language != "" {
			parts = append(parts, "lang="+blk.Language)
		}
	case BlockHeading1, BlockHeading2, BlockHeading3:
		parts = append(parts, fmt.Sprintf("h%d", blk.HeadingLvl))
		if blk.Collapsible {
			parts = append(parts, "toggleable")
		}
	case BlockTable:
		parts = append(parts, fmt.Sprintf("w=%d", blk.TableWidth))
		if blk.HasColumnHeader {
			parts = append(parts, "col-header")
		}
		if blk.HasRowHeader {
			parts = append(parts, "row-header")
		}
	case BlockCallout:
		if blk.Icon != nil {
			parts = append(parts, "icon="+iconString(blk.Icon))
		}
	case BlockBookmark, BlockLinkPreview:
		if blk.URL != "" {
			parts = append(parts, "url="+blk.URL)
		}
	case BlockChildPage, BlockChildDatabase, BlockLinkToPage:
		if blk.TargetID != "" {
			parts = append(parts, "target="+blk.TargetID)
		}
	case BlockImage, BlockVideo, BlockFile, BlockPDF:
		if blk.File != nil && blk.File.URL != "" {
			parts = append(parts, "file="+blk.File.URL)
		}
	case BlockUnsupported:
		parts = append(parts, "raw")
	}
	// Color shown when non-default and meaningful (callout/heading/text blocks).
	if blk.Color != "" && blk.Color != "default" {
		parts = append(parts, "color="+blk.Color)
	}
	// Mentions inside the rich text.
	if m := mentionFlags(blk.RichText); m != "" {
		parts = append(parts, m)
	}
	return strings.Join(parts, " ")
}

func mentionFlags(rts []RichText) string {
	var parts []string
	for _, rt := range rts {
		if rt.Mention != nil {
			id := rt.Mention.TargetID
			if id == "" {
				parts = append(parts, "mention:"+rt.Mention.Kind)
			} else {
				parts = append(parts, "mention:"+rt.Mention.Kind+"="+id)
			}
		}
	}
	return strings.Join(parts, " ")
}

// richTextString concatenates the plain text of a rich-text run.
func richTextString(rts []RichText) string {
	var b strings.Builder
	for _, rt := range rts {
		b.WriteString(rt.Text)
	}
	return b.String()
}

func iconString(ic *Icon) string {
	if ic == nil {
		return ""
	}
	if ic.Emoji != "" {
		return ic.Emoji
	}
	return ic.URL
}

// snippet trims and bounds text for the dump, collapsing internal newlines so a
// block stays on one line and replacing any run-over with an ellipsis.
func snippet(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= snippetLen {
		return s
	}
	// Trim on a rune boundary to avoid splitting a multibyte char.
	runes := []rune(s)
	if len(runes) <= snippetLen {
		return s
	}
	return string(runes[:snippetLen]) + "…"
}
