package fanql

import (
	"fmt"
	"strings"
	"unicode"
)

type Context uint8

const (
	FilterContext Context = iota
	MeasureContext
)

type Kind uint8

const (
	EOF Kind = iota
	Word
	Ident
	Path
	String
	Raw
	BracketKey
	Number
	Punct
	Bad
)

// Span uses code-point offsets, with an exclusive end.
type Span struct{ Start, End int }

type Token struct {
	Kind     Kind
	Text     string
	Quote    byte
	Unclosed bool
	Ctx      Context
	Span     Span
	Message  string
	Hint     string
}

type Tokens []Token

func identStart(r rune) bool    { return unicode.IsLetter(r) || r == '_' }
func identContinue(r rune) bool { return identStart(r) || unicode.IsDigit(r) }
func digit(r rune) bool         { return r >= '0' && r <= '9' }

// Lex never fails and always ends with EOF. A depth-zero pipe enters measure
// context; by/every/sort/limit or another pipe leaves it. count_if's matching
// parentheses enclose filter context, ignoring delimiters inside quoted tokens.
func Lex(text string) Tokens {
	r := []rune(text)
	ts := make(Tokens, 0, min(len(r)+1, 1024))
	ctx := FilterContext
	depth, brackets := 0, 0
	var conditional []int
	for i := 0; i < len(r); {
		if unicode.IsSpace(r[i]) {
			i++
			continue
		}
		start := i
		tok := Token{Ctx: ctx}
		c := r[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			tok.Kind, tok.Quote = String, byte(c)
			if c == '`' {
				tok.Kind = Raw
			} else if n := len(ts); n >= 2 && ts[n-1].Text == "[" && (strings.EqualFold(ts[n-2].Text, "attributes") || strings.EqualFold(ts[n-2].Text, "resource")) {
				tok.Kind = BracketKey
			}
			i++
			var body strings.Builder
			tok.Unclosed = true
			for i < len(r) {
				if r[i] == c {
					i++
					if i < len(r) && r[i] == c {
						body.WriteRune(c)
						i++
						continue
					}
					tok.Unclosed = false
					break
				}
				body.WriteRune(r[i])
				i++
			}
			tok.Text = body.String()
		case ctx == MeasureContext && identStart(c):
			tok.Kind = Ident
			i++
			for i < len(r) && identContinue(r[i]) {
				i++
			}
			for i+1 < len(r) && r[i] == '.' && identStart(r[i+1]) {
				tok.Kind = Path
				i += 2
				for i < len(r) && identContinue(r[i]) {
					i++
				}
			}
			tok.Text = string(r[start:i])
			if depth == 0 && brackets == 0 {
				switch strings.ToLower(tok.Text) {
				case "by", "every", "sort", "limit":
					ctx = FilterContext
					tok.Ctx = ctx
					tok.Kind = Word
				}
			}
		case ctx == MeasureContext && digit(c):
			tok.Kind = Number
			i = scanNumber(r, i)
		case c == '$':
			i++
			if ctx == FilterContext && i < len(r) && identStart(r[i]) {
				tok.Kind = Word
				for i < len(r) && identContinue(r[i]) {
					i++
				}
			} else {
				tok.Kind = Bad
				tok.Message = "unexpected $"
			}
		case strings.ContainsRune("|(),:=!<>~[]+-*", c) || (ctx == MeasureContext && c == '/'):
			tok.Kind = Punct
			i++
			if i < len(r) {
				pair := string(r[start : i+1])
				switch pair {
				case ">=", "<=", "!=", "<>", "!~":
					i++
				case "~~":
					i++
					tok.Kind = Bad
					tok.Hint = "~~ is SQL LIKE; use like, ilike or ~"
					tok.Message = tok.Hint
				}
			}
			if c == '!' && i == start+1 {
				tok.Kind = Bad
				tok.Message = "! negation is not FanQL"
			}
		case strings.ContainsRune("#{}@", c):
			tok.Kind = Bad
			i++
		case ctx == FilterContext:
			tok.Kind = Word
			i++
			for i < len(r) && !unicode.IsSpace(r[i]) && !strings.ContainsRune("|(),:=!<>~\"'`[]*${}#@", r[i]) {
				i++
			}
		default:
			tok.Kind = Bad
			i++
			if c == '.' && len(ts) > 0 && ts[len(ts)-1].Kind == Ident {
				prev := ts[len(ts)-1].Text
				tok.Message = fmt.Sprintf("unexpected . after %s; write the full key, e.g. %s.id", prev, prev)
			}
		}
		if tok.Text == "" && tok.Quote == 0 {
			tok.Text = string(r[start:i])
		}
		tok.Span = Span{start, i}
		if tok.Kind == Bad && tok.Message == "" {
			tok.Message = "unexpected " + tok.Text
		}
		if tok.Kind == Punct {
			switch tok.Text {
			case "[":
				brackets++
			case "]":
				brackets = max(0, brackets-1)
			case "(":
				depth++
				if ctx == MeasureContext && len(ts) > 0 && strings.EqualFold(ts[len(ts)-1].Text, "count_if") {
					conditional = append(conditional, depth)
					ctx = FilterContext
				}
			case ")":
				if n := len(conditional); n > 0 && depth == conditional[n-1] {
					ctx = MeasureContext
					tok.Ctx = ctx
					conditional = conditional[:n-1]
				}
				depth = max(0, depth-1)
			case "|":
				if depth == 0 && brackets == 0 {
					if ctx == FilterContext {
						ctx = MeasureContext
					} else {
						ctx = FilterContext
					}
				}
			}
		}
		ts = append(ts, tok)
	}
	return append(ts, Token{Kind: EOF, Ctx: FilterContext, Span: Span{len(r), len(r)}})
}

func scanNumber(r []rune, i int) int {
	for i < len(r) && digit(r[i]) {
		i++
	}
	if i < len(r) && r[i] == '.' {
		i++
		for i < len(r) && digit(r[i]) {
			i++
		}
	}
	if i < len(r) && (r[i] == 'e' || r[i] == 'E') {
		end := i + 1
		if end < len(r) && (r[end] == '+' || r[end] == '-') {
			end++
		}
		start := end
		for end < len(r) && digit(r[end]) {
			end++
		}
		if end > start {
			i = end
		}
	}
	return i
}
