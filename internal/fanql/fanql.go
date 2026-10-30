// Package fanql parses the versioned text form of Fanout's structured telemetry queries.
package fanql

import "github.com/labstack/fanout/internal/panel"

// Version identifies the interactive language contract.
const Version = 1

type Options struct {
	Panel     *panel.Panel     `json:"panel,omitempty"`
	Source    string           `json:"source,omitempty"`
	Viz       string           `json:"viz,omitempty"`
	Variables []panel.Variable `json:"variables,omitempty"`
	Schema    *panel.Schema    `json:"-"`
}

type Compiled struct {
	Query      panel.Query
	Canonical  string
	Notices    []Notice
	Compatible []panel.VizCompat
	Edits      []VizEdit
}

// Compile parses, resolves and lowers text without executing SQL. A problem
// always prevents an executable query from being returned.
func Compile(text string, opts Options) (*Compiled, panel.Problems) {
	return compileWith(text, opts, currentVocabulary())
}
func compileWith(text string, opts Options, vocab vocabulary) (*Compiled, panel.Problems) {
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
