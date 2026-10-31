// Package fanql parses the text form of Fanout's structured telemetry queries.
package fanql

import (
	"sort"

	"github.com/labstack/fanout/internal/query"
)

type Options struct {
	Panel     *query.Panel     `json:"panel,omitempty"`
	Source    string           `json:"source,omitempty"`
	Viz       string           `json:"viz,omitempty"`
	Variables []query.Variable `json:"variables,omitempty"`
	Schema    *query.Schema    `json:"-"`
}

type Compiled struct {
	Query      query.Spec
	Canonical  string
	Notices    []Notice
	Compatible []query.VizCompat
	Edits      []VizEdit
}

// Compile parses, resolves and lowers text without executing SQL. A problem
// always prevents an executable query from being returned.
func Compile(text string, opts Options) (*Compiled, query.Problems) {
	return compileWith(text, opts, currentVocabulary())
}
func compileWith(text string, opts Options, vocab vocabulary) (*Compiled, query.Problems) {
	q, problems := parseRecovered(text, opts.Source, vocab)
	if len(problems) != 0 {
		return nil, withRecoveredProblems(q, problems, opts, vocab)
	}
	r, problems := resolveWith(q, opts, vocab)
	if len(problems) != 0 {
		return nil, problems
	}
	pq, problems := lower(r)
	if len(problems) != 0 {
		return nil, problems
	}
	return &Compiled{Query: pq, Canonical: print(q), Notices: r.Notices, Compatible: r.Compatible, Edits: r.Edits}, nil
}

// withRecoveredProblems adds the resolver's problems in the recovered tree to
// the syntax problems, in source order and within the problem cap.
func withRecoveredProblems(q *queryTree, problems query.Problems, opts Options, vocab vocabulary) query.Problems {
	if q == nil || len(problems) > maxProblems {
		return problems
	}
	seen := map[query.TextSpan]string{}
	for _, p := range problems {
		if p.Span != nil {
			seen[*p.Span] = p.Message
		}
	}
	for _, p := range checkRecovered(q, opts, vocab) {
		if p.Span != nil && seen[*p.Span] == p.Message {
			continue
		}
		problems = append(problems, p)
	}
	sort.SliceStable(problems, func(i, j int) bool {
		return problems[i].Span != nil && problems[j].Span != nil && problems[i].Span.Start < problems[j].Span.Start
	})
	problems = presentProblems(problems)
	if len(problems) > maxProblems {
		problems = append(problems[:maxProblems], query.Problem{Path: "text", Message: message(diagMore), Span: problems[maxProblems].Span})
	}
	return problems
}
