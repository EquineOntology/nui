// Package render is the pure presentation layer of nui: it turns the doc IR into
// the structured layout IR (doc.Line / doc.Segment) and, in exactly one file
// (paint.go), into ANSI. It imports doc but never notion, tui, a TTY, or the
// network (SPEC §4 dependency rule).
//
// THE PAINT RULE (SPEC §6): every file here EXCEPT paint.go emits structured
// []doc.Line / []doc.Segment and MUST NOT call lipgloss Style.Render() to produce
// a string. Lip Gloss is used only for (a) style DEFINITIONS in this file and
// (b) width math (lipgloss.Width). ANSI is produced once, in paint.go.
package render

import "github.com/charmbracelet/lipgloss"

// Theme holds the resolved style definitions and the Notion color-name -> color
// map. It is defined with Lip Gloss styles for documentation and width math, but
// the per-Segment style flags that actually drive painting are the booleans and
// color names carried on doc.Style — paint.go rebuilds a lipgloss.Style from
// those. Keeping the map here (definitions only) is what satisfies the paint rule.
type Theme struct {
	// colors maps a Notion color name (foreground variants, e.g. "blue") to a
	// terminal color value. The "*_background" variants are handled separately
	// (see Background): Notion's "blue_background" tints the background, not the fg.
	colors map[string]lipgloss.Color

	// backgrounds maps a Notion "*_background" color name to its background tint.
	backgrounds map[string]lipgloss.Color

	// Heading styles are kept as Lip Gloss definitions for width/measurement and
	// to document intent; their boolean projection (Bold etc.) is what flows onto
	// doc.Style. These are never .Render()'d here.
	H1, H2, H3 lipgloss.Style

	// Code is the inline/code-block base style definition.
	Code lipgloss.Style

	// Quote is the left-bar / quote body style definition.
	Quote lipgloss.Style
}

// Notion's standard palette. Values are ANSI-256 hex-ish picks that read well on
// both light and dark terminals; they are deliberately muted to match Notion's
// own restraint. The names are exactly the Notion color names (SPEC §5/§6) so the
// IR's Annotations.Color maps in with no translation table elsewhere.
//
// These are the FOREGROUND colors. "default" maps to "" (terminal default) so an
// unstyled run inherits the terminal's own fg.
var notionFg = map[string]string{
	"default": "",
	"gray":    "#9b9a97",
	"brown":   "#a27763",
	"orange":  "#d9730d",
	"yellow":  "#cb912f",
	"green":   "#448361",
	"blue":    "#337ea9",
	"purple":  "#9065b0",
	"pink":    "#c14c8a",
	"red":     "#d44c47",
}

// Notion's "*_background" tints. The name (e.g. "blue_background") sets the
// background; the foreground is left to inherit so text stays readable. Values
// are the muted background tints Notion uses.
var notionBg = map[string]string{
	"default_background": "",
	"gray_background":    "#ebeced",
	"brown_background":   "#e9e5e3",
	"orange_background":  "#faebdd",
	"yellow_background":  "#fbf3db",
	"green_background":   "#ddedea",
	"blue_background":    "#ddebf1",
	"purple_background":  "#eae4f2",
	"pink_background":    "#f4dfeb",
	"red_background":     "#fbe4e4",
}

// DefaultTheme builds the standard nui theme: the Notion palette plus the heading
// / code / quote style definitions. It is pure and allocation-cheap; callers may
// build one per Layout pass.
func DefaultTheme() *Theme {
	colors := make(map[string]lipgloss.Color, len(notionFg))
	for name, hex := range notionFg {
		colors[name] = lipgloss.Color(hex)
	}
	backgrounds := make(map[string]lipgloss.Color, len(notionBg))
	for name, hex := range notionBg {
		backgrounds[name] = lipgloss.Color(hex)
	}

	return &Theme{
		colors:      colors,
		backgrounds: backgrounds,
		H1:          lipgloss.NewStyle().Bold(true),
		H2:          lipgloss.NewStyle().Bold(true),
		H3:          lipgloss.NewStyle().Bold(true),
		Code:        lipgloss.NewStyle().Foreground(lipgloss.Color(notionFg["red"])),
		Quote:       lipgloss.NewStyle().Italic(true),
	}
}

// Foreground resolves a Notion color name to a foreground hex value, or "" for
// the terminal default / unknown name. A "*_background" name has no foreground of
// its own, so it resolves to "" (the background is applied via Background).
func (t *Theme) Foreground(colorName string) string {
	if colorName == "" {
		return ""
	}
	if c, ok := t.colors[colorName]; ok {
		return string(c)
	}
	return ""
}

// Background resolves a Notion "*_background" color name to a background hex
// value, or "" for none / unknown. A plain foreground name has no background, so
// it resolves to "".
func (t *Theme) Background(colorName string) string {
	if colorName == "" {
		return ""
	}
	if c, ok := t.backgrounds[colorName]; ok {
		return string(c)
	}
	return ""
}

// IsBackground reports whether a Notion color name is a "*_background" variant.
func IsBackground(colorName string) bool {
	const suffix = "_background"
	return len(colorName) > len(suffix) && colorName[len(colorName)-len(suffix):] == suffix
}
