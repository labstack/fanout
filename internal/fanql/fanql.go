// Package fanql parses the versioned text form of Fanout's structured telemetry queries.
package fanql

import "github.com/labstack/fanout/internal/panel"

// Version identifies the interactive language contract.
const Version = 1

type Options struct {
	Source    string
	Viz       string
	Variables []panel.Variable
	Schema    *panel.Schema
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
	q, problems := Parse(text, opts.Source)
	if len(problems) != 0 {
		return nil, problems
	}
	r, problems := Resolve(q, ResolveOptions{Viz: opts.Viz, Variables: opts.Variables, Schema: opts.Schema})
	if len(problems) != 0 {
		return nil, problems
	}
	pq, problems := lower(r)
	if len(problems) != 0 {
		return nil, problems
	}
	return &Compiled{Query: pq, Canonical: Print(q), Notices: r.Notices, Compatible: r.Compatible, Edits: r.Edits}, nil
}
