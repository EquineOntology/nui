package render

import "github.com/EQuineOntology/nui/internal/doc"

// RichText projects a run of doc.RichText spans onto styled doc.Segments: each
// span's Annotations become a doc.Style (bold/italic/strike/underline/code +
// resolved color), and its Href is carried verbatim for OSC-8 wrapping at paint
// time. This is pure structure — NO ANSI is produced here (the paint rule).
//
// A mention span (rt.Mention != nil) renders as its resolved Label if its Text is
// empty, so a mention always has something to show; navigation off the mention is
// G3 and rides on the IR, not on the Segment.
func RichText(rts []doc.RichText, theme *Theme) []doc.Segment {
	if len(rts) == 0 {
		return nil
	}
	out := make([]doc.Segment, 0, len(rts))
	for _, rt := range rts {
		text := rt.Text
		if text == "" && rt.Mention != nil && rt.Mention.Label != "" {
			text = rt.Mention.Label
		}
		if text == "" {
			continue
		}
		out = append(out, doc.Segment{
			Text:  text,
			Style: styleFor(rt.Ann, rt.Href, theme),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// styleFor resolves one span's annotations + href into a doc.Style. The Notion
// color name is split into a foreground or a background tint depending on whether
// it is a "*_background" variant (Notion uses one name field for both). Unknown
// names resolve to "" (inherit), never an error.
func styleFor(ann doc.Annotations, href string, theme *Theme) doc.Style {
	s := doc.Style{
		Bold:      ann.Bold,
		Italic:    ann.Italic,
		Underline: ann.Underline,
		Strike:    ann.Strikethrough,
		Code:      ann.Code,
		Href:      href,
	}
	if theme != nil && ann.Color != "" && ann.Color != "default" {
		if IsBackground(ann.Color) {
			s.Bg = theme.Background(ann.Color)
		} else {
			s.Fg = theme.Foreground(ann.Color)
		}
	}
	return s
}
