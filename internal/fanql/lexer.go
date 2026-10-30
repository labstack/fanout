package fanql

import (
	"fmt"
	"strings"
	"unicode"
)

type tokenContext uint8

const (
	filterContext tokenContext = iota
	measureContext
)

type tokenKind uint8

const (
	eof tokenKind = iota
	tokenWord
	tokenIdent
	tokenPath
	tokenString
	tokenRaw
	tokenBracketKey
	tokenNumber
	tokenPunct
	tokenBad
)

// Span uses code-point offsets, with an exclusive end.
type span struct{ Start, End int }

type lexToken struct {
	Kind     tokenKind
	Text     string
	Quote    byte
	Unclosed bool
	Ctx      tokenContext
	Span     span
	Message  string
	Hint     string
}

type lexTokens []lexToken

func identStart(r rune) bool    { return unicode.IsLetter(r) || r == '_' }
func identContinue(r rune) bool { return identStart(r) || unicode.IsDigit(r) }
func digit(r rune) bool         { return r >= '0' && r <= '9' }

// Lex never fails and always ends with EOF. A depth-zero pipe enters measure
// context; by/every/sort/limit or another pipe leaves it. count_if's matching
// parentheses enclose filter context, ignoring delimiters inside quoted tokens.
func lex(text string) lexTokens {
	r := []rune(text)
	ts := make(lexTokens, 0, min(len(r)+1, 1024))
	ctx := filterContext
	depth, brackets := 0, 0
	var conditional []int
	for i := 0; i < len(r); {
		if unicode.IsSpace(r[i]) {
			i++
			continue
		}
		start := i
		tok := lexToken{Ctx: ctx}
		c := r[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			tok.Kind, tok.Quote = tokenString, byte(c)
			if c == '`' {
				tok.Kind = tokenRaw
			} else if n := len(ts); n >= 2 && ts[n-1].Text == "[" && (strings.EqualFold(ts[n-2].Text, "attributes") || strings.EqualFold(ts[n-2].Text, "resource")) {
				tok.Kind = tokenBracketKey
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
		case ctx == measureContext && identStart(c):
			tok.Kind = tokenIdent
			i++
			for i < len(r) && identContinue(r[i]) {
				i++
			}
			for i+1 < len(r) && r[i] == '.' && identStart(r[i+1]) {
				tok.Kind = tokenPath
				i += 2
				for i < len(r) && identContinue(r[i]) {
					i++
				}
			}
			tok.Text = string(r[start:i])
			if depth == 0 && brackets == 0 {
				switch strings.ToLower(tok.Text) {
				case "by", "every", "sort", "limit":
					ctx = filterContext
					tok.Ctx = ctx
					tok.Kind = tokenWord
				}
			}
		case ctx == measureContext && digit(c):
			tok.Kind = tokenNumber
			i = scanNumber(r, i)
		case c == '$':
			i++
			if i+1 < len(r) && r[i] == '_' && r[i+1] == '_' {
				for i < len(r) && identContinue(r[i]) {
					i++
				}
				tok.Kind = tokenBad
				tok.Text = string(r[start:i])
				tok.Message = message(diagVariable)
				break
			}
			if ctx == filterContext && i < len(r) && identStart(r[i]) {
				tok.Kind = tokenWord
				for i < len(r) && identContinue(r[i]) {
					i++
				}
			} else {
				tok.Kind = tokenBad
				tok.Message = "unexpected $"
			}
		case strings.ContainsRune("|(),:=!<>~[]+-*", c) || (ctx == measureContext && c == '/'):
			tok.Kind = tokenPunct
			i++
			if i < len(r) {
				pair := string(r[start : i+1])
				switch pair {
				case ">=", "<=", "!=", "<>", "!~":
					i++
				case "~~":
					i++
					tok.Kind = tokenBad
					tok.Message = "~~ is SQL LIKE; use like, ilike or ~"
					tok.Hint = "use like or ilike for SQL patterns; use ~ for a literal substring"
				}
			}
			if c == '!' && i == start+1 {
				tok.Kind = tokenBad
				tok.Message = "! negation is not FanQL"
			}
		case strings.ContainsRune("#{}@", c):
			tok.Kind = tokenBad
			i++
		case ctx == filterContext:
			tok.Kind = tokenWord
			i++
			for i < len(r) && !unicode.IsSpace(r[i]) && !strings.ContainsRune("|(),:=!<>~\"'`[]*${}#@", r[i]) {
				i++
			}
		default:
			tok.Kind = tokenBad
			i++
			if c == '.' && len(ts) > 0 && ts[len(ts)-1].Kind == tokenIdent {
				prev := ts[len(ts)-1].Text
				tok.Message = fmt.Sprintf("unexpected . after %s; write the full key, e.g. %s.id", prev, prev)
			}
		}
		if tok.Text == "" && tok.Quote == 0 {
			tok.Text = string(r[start:i])
		}
		tok.Span = span{start, i}
		if tok.Kind == tokenBad && tok.Message == "" {
			tok.Message = "unexpected " + tok.Text
		}
		if tok.Kind == tokenPunct {
			switch tok.Text {
			case "[":
				brackets++
			case "]":
				brackets = max(0, brackets-1)
			case "(":
				depth++
				if ctx == measureContext && len(ts) > 0 && strings.EqualFold(ts[len(ts)-1].Text, "count_if") {
					conditional = append(conditional, depth)
					ctx = filterContext
				}
			case ")":
				if n := len(conditional); n > 0 && depth == conditional[n-1] {
					ctx = measureContext
					tok.Ctx = ctx
					conditional = conditional[:n-1]
				}
				depth = max(0, depth-1)
			case "|":
				if depth == 0 && brackets == 0 {
					if ctx == filterContext {
						ctx = measureContext
					} else {
						ctx = filterContext
					}
				}
			}
		}
		ts = append(ts, tok)
	}
	return append(ts, lexToken{Kind: eof, Ctx: filterContext, Span: span{len(r), len(r)}})
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
