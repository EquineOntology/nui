package render

import (
	"strings"

	"github.com/EQuineOntology/nui/internal/doc"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// code.go bridges chroma (syntax highlighting) into the structured Segment model.
// CRITICAL (SPEC §6 paint rule + G2 note): chroma must NEVER emit ANSI here. We
// use only its LEXER to tokenize source into chroma.Token values, then map each
// token type to a doc.Style. ANSI is produced once, in paint.go. We deliberately
// do not import chroma/formatters or chroma/styles — only the lexer.

// highlightCode tokenizes source in the given language and returns it as rows of
// styled segments (one row per source line). On any lexing failure, or an unknown
// language, it degrades to a single default-styled segment per line (the code
// still renders, just unhighlighted — never a panic).
func highlightCode(source, language string, theme *Theme) [][]doc.Segment {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	if source == "" {
		return [][]doc.Segment{nil}
	}

	lexer := lexerFor(language, source)
	if lexer == nil {
		return plainCodeRows(source)
	}

	iter, err := lexer.Tokenise(nil, source)
	if err != nil {
		return plainCodeRows(source)
	}

	var rows [][]doc.Segment
	var cur []doc.Segment
	for _, tok := range iter.Tokens() {
		style := tokenStyle(tok.Type, theme)
		// A token's value may span multiple lines; split so each row is a line.
		parts := strings.Split(tok.Value, "\n")
		for i, part := range parts {
			if part != "" {
				cur = append(cur, doc.Segment{Text: part, Style: style})
			}
			if i < len(parts)-1 {
				rows = append(rows, cur)
				cur = nil
			}
		}
	}
	rows = append(rows, cur)
	return rows
}

// lexerFor resolves a chroma lexer by Notion language name, falling back to
// content analysis, then to the plaintext lexer (nil signals "no highlight").
func lexerFor(language, source string) chroma.Lexer {
	if language != "" && language != "plain text" && language != "plaintext" {
		if l := lexers.Get(language); l != nil {
			return l
		}
	}
	if l := lexers.Analyse(source); l != nil {
		return l
	}
	return nil
}

// plainCodeRows splits source into rows of one default-styled segment each.
func plainCodeRows(source string) [][]doc.Segment {
	lines := strings.Split(source, "\n")
	rows := make([][]doc.Segment, 0, len(lines))
	for _, ln := range lines {
		if ln == "" {
			rows = append(rows, nil)
			continue
		}
		rows = append(rows, []doc.Segment{{Text: ln}})
	}
	return rows
}

// tokenStyle maps a chroma token type to a doc.Style using the Notion palette, so
// highlighted code matches the rest of the theme. The mapping is coarse (the
// common token classes); anything unmapped inherits the terminal default.
func tokenStyle(t chroma.TokenType, theme *Theme) doc.Style {
	if theme == nil {
		return doc.Style{}
	}
	color := func(name string) string { return theme.Foreground(name) }

	switch {
	case t.InCategory(chroma.Comment):
		return doc.Style{Fg: color("gray"), Italic: true}
	case t.InCategory(chroma.Keyword):
		return doc.Style{Fg: color("purple"), Bold: true}
	case t.InCategory(chroma.LiteralString):
		return doc.Style{Fg: color("green")}
	case t.InCategory(chroma.LiteralNumber):
		return doc.Style{Fg: color("orange")}
	case t.InCategory(chroma.Name):
		switch t {
		case chroma.NameFunction, chroma.NameClass:
			return doc.Style{Fg: color("blue")}
		case chroma.NameBuiltin, chroma.NameBuiltinPseudo:
			return doc.Style{Fg: color("yellow")}
		case chroma.NameTag:
			return doc.Style{Fg: color("red")}
		default:
			return doc.Style{}
		}
	case t.InCategory(chroma.Operator):
		return doc.Style{Fg: color("pink")}
	default:
		return doc.Style{}
	}
}
