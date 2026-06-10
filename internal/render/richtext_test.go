package render_test

import (
	"testing"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/EQuineOntology/nui/internal/render"
)

// TestRichTextAnnotations asserts each annotation maps onto the right doc.Style
// field, and that a Notion color name resolves to a foreground hex while a
// "*_background" name resolves to a background tint (the one-name-two-meanings
// quirk).
func TestRichTextAnnotations(t *testing.T) {
	theme := render.DefaultTheme()
	in := []doc.RichText{
		{Text: "b", Ann: doc.Annotations{Bold: true}},
		{Text: "i", Ann: doc.Annotations{Italic: true}},
		{Text: "s", Ann: doc.Annotations{Strikethrough: true}},
		{Text: "u", Ann: doc.Annotations{Underline: true}},
		{Text: "c", Ann: doc.Annotations{Code: true}},
		{Text: "fg", Ann: doc.Annotations{Color: "red"}},
		{Text: "bg", Ann: doc.Annotations{Color: "blue_background"}},
		{Text: "link", Href: "https://x.test"},
	}
	got := render.RichText(in, theme)
	if len(got) != len(in) {
		t.Fatalf("segment count = %d, want %d", len(got), len(in))
	}
	if !got[0].Style.Bold {
		t.Error("bold not mapped")
	}
	if !got[1].Style.Italic {
		t.Error("italic not mapped")
	}
	if !got[2].Style.Strike {
		t.Error("strikethrough -> Strike not mapped")
	}
	if !got[3].Style.Underline {
		t.Error("underline not mapped")
	}
	if !got[4].Style.Code {
		t.Error("code not mapped")
	}
	if got[5].Style.Fg == "" || got[5].Style.Bg != "" {
		t.Errorf("color %q: want fg set, bg empty; got fg=%q bg=%q", "red", got[5].Style.Fg, got[5].Style.Bg)
	}
	if got[6].Style.Bg == "" || got[6].Style.Fg != "" {
		t.Errorf("color %q: want bg set, fg empty; got fg=%q bg=%q", "blue_background", got[6].Style.Fg, got[6].Style.Bg)
	}
	if got[7].Style.Href != "https://x.test" {
		t.Errorf("href not carried: %q", got[7].Style.Href)
	}
}

// TestRichTextDefaultColorInherits asserts the "default" color leaves both fg and
// bg empty (inherit the terminal default) rather than resolving to a hex.
func TestRichTextDefaultColorInherits(t *testing.T) {
	got := render.RichText([]doc.RichText{{Text: "x", Ann: doc.Annotations{Color: "default"}}}, render.DefaultTheme())
	if len(got) != 1 {
		t.Fatalf("want 1 segment, got %d", len(got))
	}
	if got[0].Style.Fg != "" || got[0].Style.Bg != "" {
		t.Errorf("default color must inherit; got fg=%q bg=%q", got[0].Style.Fg, got[0].Style.Bg)
	}
}

// TestRichTextMentionLabel asserts a mention with empty Text falls back to its
// resolved Label so it is not dropped.
func TestRichTextMentionLabel(t *testing.T) {
	got := render.RichText([]doc.RichText{
		{Text: "", Mention: &doc.Mention{Kind: "page", Label: "Roadmap"}},
	}, render.DefaultTheme())
	if len(got) != 1 || got[0].Text != "Roadmap" {
		t.Errorf("mention label fallback failed: %+v", got)
	}
}

// TestRichTextEmpty asserts an empty/whitespace-stripped run yields no segments.
func TestRichTextEmpty(t *testing.T) {
	if got := render.RichText(nil, render.DefaultTheme()); got != nil {
		t.Errorf("nil input must yield nil, got %v", got)
	}
	if got := render.RichText([]doc.RichText{{Text: ""}}, render.DefaultTheme()); got != nil {
		t.Errorf("empty-text input must yield nil, got %v", got)
	}
}
