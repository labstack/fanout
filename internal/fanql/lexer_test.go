package fanql

import (
	"reflect"
	"strings"
	"testing"
)

func tokenTexts(ts lexTokens) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Text)
	}
	return out
}

func TestLexFilterContextTokens(t *testing.T) {
	ts := lex("spans where http.target:/a:b duration>=500ms -k:v us-east-1 $svc")
	want := []string{"spans", "where", "http.target", ":", "/a", ":", "b", "duration", ">=", "500ms", "-", "k", ":", "v", "us-east-1", "$svc", ""}
	if !reflect.DeepEqual(tokenTexts(ts), want) {
		t.Fatalf("tokens: %#v", ts)
	}
	for i, tkn := range ts {
		kind := tokenWord
		if strings.Contains(" : >= - ", " "+tkn.Text+" ") {
			kind = tokenPunct
		}
		if i == len(ts)-1 {
			kind = eof
		}
		if tkn.Kind != kind || tkn.Ctx != filterContext {
			t.Errorf("token %d: %#v", i, tkn)
		}
	}
}

func TestLexLongestMatchOperators(t *testing.T) {
	var got []string
	for _, tok := range lex("a>=b a<>b a!~b a!=b a:>b a<=b") {
		if tok.Kind == tokenPunct {
			got = append(got, tok.Text)
		}
	}
	if !reflect.DeepEqual(got, []string{">=", "<>", "!~", "!=", ":", ">", "<="}) {
		t.Fatal(got)
	}
}

func TestLexBadTokens(t *testing.T) {
	for _, tc := range []struct{ text, bad, message, hint string }{
		{"!service:api", "!", "! negation is not FanQL", ""},
		{"body ~~ x", "~~", "", "~~ is SQL LIKE; use like, ilike or ~"},
		{"#tag", "#", "", ""}, {"$ x", "$", "unexpected $", ""},
	} {
		t.Run(tc.text, func(t *testing.T) {
			for _, tok := range lex(tc.text) {
				if tok.Kind == tokenBad && tok.Text == tc.bad {
					if tc.message != "" && tok.Message != tc.message {
						t.Fatal(tok)
					}
					if tc.hint != "" && tok.Hint != tc.hint {
						t.Fatal(tok)
					}
					return
				}
			}
			t.Fatal("missing Bad token")
		})
	}
}

func TestLexStringsAndQuoteDoubling(t *testing.T) {
	for _, tc := range []struct {
		text, decoded string
		quote         byte
		unclosed      bool
	}{
		{`k:"a ""b"" c"`, `a "b" c`, '"', false}, {`k:'it''s'`, "it's", '\'', false}, {`k:"unclosed`, "unclosed", '"', true},
	} {
		tok := lex(tc.text)[2]
		if tok.Kind != tokenString || tok.Text != tc.decoded || tok.Quote != tc.quote || tok.Unclosed != tc.unclosed {
			t.Errorf("%s: %#v", tc.text, tok)
		}
		if tc.unclosed && tok.Span.End != len([]rune(tc.text)) {
			t.Fatal(tok)
		}
	}
}

func TestLexRawEntries(t *testing.T) {
	for _, tc := range []struct {
		text, decoded string
		unclosed      bool
	}{{"`coalesce(body, '``') = ''`", "coalesce(body, '`') = ''", false}, {"`coalesce(a", "coalesce(a", true}} {
		tok := lex(tc.text)[0]
		if tok.Kind != tokenRaw || tok.Text != tc.decoded || tok.Unclosed != tc.unclosed || tok.Quote != '`' {
			t.Fatal(tok)
		}
	}
}

func TestLexBracketKeys(t *testing.T) {
	ts := lex(`attributes['http.target']:x`)
	if !reflect.DeepEqual(tokenTexts(ts), []string{"attributes", "[", "http.target", "]", ":", "x", ""}) || ts[2].Kind != tokenBracketKey {
		t.Fatal(ts)
	}
	tok := lex(`resource["k8s.`)[2]
	if tok.Kind != tokenBracketKey || tok.Text != "k8s." || !tok.Unclosed {
		t.Fatal(tok)
	}
	if lex(`k:'x'`)[2].Kind != tokenString {
		t.Fatal("ordinary quote became bracket key")
	}
}

func TestLexMeasureContextSwitching(t *testing.T) {
	ts := lex("spans where a:1 | count_if(status:error -http.target:/healthz) / count() * 1.5 as x by resource.k8s.pod.name every 1m")
	want := []string{"spans", "where", "a", ":", "1", "|", "count_if", "(", "status", ":", "error", "-", "http.target", ":", "/healthz", ")", "/", "count", "(", ")", "*", "1.5", "as", "x", "by", "resource.k8s.pod.name", "every", "1m", ""}
	if !reflect.DeepEqual(tokenTexts(ts), want) {
		t.Fatal(ts)
	}
	for i, tok := range ts {
		ctx := filterContext
		if i >= 6 && i <= 23 && !(i >= 8 && i <= 14) {
			ctx = measureContext
		}
		if tok.Ctx != ctx {
			t.Errorf("%d: %#v", i, tok)
		}
	}
	if ts[6].Kind != tokenIdent || ts[21].Kind != tokenNumber || ts[24].Kind != tokenWord {
		t.Fatal(ts)
	}
}

func TestLexPathAndNumberInMeasureContext(t *testing.T) {
	ts := lex("spans | count_distinct(user.id) + avg(attributes.http.response_size) - p95(resource.k) * 1e3 as spread")
	var paths []string
	for _, tok := range ts {
		if tok.Kind == tokenPath {
			paths = append(paths, tok.Text)
		}
		if tok.Text == "1e3" && tok.Kind != tokenNumber {
			t.Fatal(tok)
		}
	}
	if !reflect.DeepEqual(paths, []string{"user.id", "attributes.http.response_size", "resource.k"}) {
		t.Fatal(paths)
	}
	ts = lex("spans | avg(user.)")
	if ts[4].Kind != tokenIdent || ts[5].Kind != tokenBad || ts[5].Text != "." || ts[5].Message != "unexpected . after user; write the full key, e.g. user.id" {
		t.Fatal(ts)
	}
	if got := tokenTexts(lex("spans | count_distinct(x-y)")); !reflect.DeepEqual(got, []string{"spans", "|", "count_distinct", "(", "x", "-", "y", ")", ""}) {
		t.Fatal(got)
	}
	ts = lex("spans | count() * -1 as neg")
	if ts[5].Text != "*" || ts[6].Text != "-" || ts[7].Kind != tokenNumber {
		t.Fatal(ts)
	}
}

func TestLexCountIfParenDepthIgnoresStringsAndRaw(t *testing.T) {
	ts := lex("spans | count_if(body:\"a)b\" `x = ')'`) / count() as r")
	for _, tok := range ts {
		if tok.Text == "/" {
			if tok.Kind != tokenPunct || tok.Ctx != measureContext {
				t.Fatal(tok)
			}
			return
		}
	}
	t.Fatal("missing division")
}

func TestLexSpansAreCodePoints(t *testing.T) {
	ts := lex(`logs where body:"héllo ✓" x`)
	if ts[4].Span != (span{16, 25}) || ts[5].Span != (span{26, 27}) {
		t.Fatal(ts)
	}
}

func TestLexClauseWordsInsideDelimitersStayTokens(t *testing.T) {
	ts := lex(`logs where body:"a | b sort c" attributes['x.by']:y`)
	counts := map[tokenKind]int{}
	for _, tok := range ts {
		counts[tok.Kind]++
		if tok.Text == "|" || tok.Text == "sort" {
			t.Fatal(tok)
		}
	}
	if counts[tokenString] != 1 || counts[tokenBracketKey] != 1 || ts[4].Text != "a | b sort c" || ts[7].Text != "x.by" {
		t.Fatal(ts)
	}
	ts = lex("logs where `coalesce(body,'|')=''` | count()")
	var raw, pipes int
	for _, tok := range ts {
		if tok.Kind == tokenRaw {
			raw++
		}
		if tok.Text == "|" {
			pipes++
		}
	}
	if raw != 1 || pipes != 1 {
		t.Fatal(ts)
	}
}

func TestLexAlwaysEndsWithEOFAndNeverPanics(t *testing.T) {
	for _, input := range []string{"", "|", "((((", "\"", "`", "attributes[", "$", strings.Repeat("-", 100), "\xff\xfe"} {
		ts := lex(input)
		if len(ts) == 0 || ts[len(ts)-1].Kind != eof {
			t.Fatalf("%q: %#v", input, ts)
		}
	}
}
