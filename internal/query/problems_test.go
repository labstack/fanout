package query

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// Problem includes optional slices; compare complete diagnostics by value.
func containsProblem(problems Problems, want Problem) bool {
	return slices.ContainsFunc(problems, func(got Problem) bool { return reflect.DeepEqual(got, want) })
}

func TestProblemJSONCarriesSpanAndFixIts(t *testing.T) {
	p := Problem{Path: "text", Message: "m", Span: &TextSpan{Start: 1, End: 4}, FixIts: []FixIt{{Label: "l", Span: TextSpan{Start: 1, End: 4}, Text: "x"}}}
	data, err := json.Marshal(p)
	if err != nil || string(data) != `{"path":"text","message":"m","span":{"start":1,"end":4},"fix_its":[{"label":"l","span":{"start":1,"end":4},"text":"x"}]}` {
		t.Fatalf("%s %v", data, err)
	}
	data, err = json.Marshal(Problem{Path: "p", Message: "m"})
	if err != nil || string(data) != `{"path":"p","message":"m"}` {
		t.Fatalf("%s %v", data, err)
	}
	shown := PresentProblems(Problems{p})
	if len(shown) != 1 || !reflect.DeepEqual(shown[0].Span, p.Span) || !reflect.DeepEqual(shown[0].FixIts, p.FixIts) {
		t.Fatal(shown)
	}
}
