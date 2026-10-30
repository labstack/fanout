// Package fanql parses the text form of Fanout's structured telemetry queries.
package fanql

import "github.com/labstack/fanout/internal/query"

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
	q, problems := parseWith(text, opts.Source, vocab)
	if len(problems) != 0 {
		return nil, problems
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
