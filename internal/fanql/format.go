package fanql

import (
	"context"
	"fmt"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/sqlscan"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Exactness string

const (
	Exact      Exactness = "exact"
	Normalized Exactness = "normalized"
)

type Requirement struct {
	Name      panel.VizRequirement `json:"name"`
	Satisfied bool                 `json:"satisfied"`
}
type Formatted struct {
	Requires  []Requirement `json:"requires"`
	Text      string        `json:"text"`
	Exactness Exactness     `json:"exactness"`
	Editable  bool          `json:"editable"`
	Reason    string        `json:"reason,omitempty"`
	Notices   []Notice      `json:"notices"`
}
type tier uint8

const (
	tierExact tier = iota
	tierNormalized
	tierRaw
)

// Format preserves each stored filter as one entry. Candidates are kept only
// after a byte or engine-AST exactness check; all other filters remain raw.
func Format(ctx context.Context, p panel.Parser, stored panel.Query, opts Options) (*Formatted, panel.Problems, error) {
	if opts.Panel != nil {
		opts.Viz = opts.Panel.Viz
	}
	vars := map[string]panel.Variable(nil)
	if opts.Variables != nil {
		vars = make(map[string]panel.Variable, len(opts.Variables))
		for _, v := range opts.Variables {
			vars[v.Name] = v
		}
	}
	// Most stored queries are valid tables. Prove shape validity there first
	// instead of reparsing every measure for every possible visualization.
	problems, _ := panel.QueryShape(&stored, "table", vars)
	if len(problems) > 0 && len(panel.CompatibleVizzes(&stored, vars)) == 0 {
		viz := opts.Viz
		if viz == "" {
			viz = "table"
		}
		problems, _ = panel.QueryShape(&stored, viz, vars)
		if len(problems) > 0 {
			return nil, presentProblems(problems), nil
		}
	}
	q := &queryTree{Source: stored.From}
	f := &Formatted{Exactness: Exact, Notices: []Notice{}, Requires: []Requirement{}}
	_, requirements := panel.QueryShape(&stored, opts.Viz, vars)
	for _, name := range requirements {
		satisfied := false
		if p := opts.Panel; p != nil {
			switch name {
			case panel.RequiresMinMax:
				satisfied = p.Min != nil && p.Max != nil && *p.Min < *p.Max
			case panel.RequiresThresholds:
				satisfied = len(p.Thresholds) > 0
			}
		}
		f.Requires = append(f.Requires, Requirement{Name: name, Satisfied: satisfied})
	}
	for i, text := range stored.Where {
		if opts.Variables == nil {
			for _, name := range sqlscan.Parameters(text) {
				f.Notices = append(f.Notices, Notice{Path: fmt.Sprintf("where[%d]", i), Message: "declared variables are needed to check $" + name})
			}
		}
		e, t, err := liftEntry(ctx, p, stored.From, text)
		if err != nil {
			if operationalFormatError(err) {
				return nil, nil, fmt.Errorf("format where[%d]: %w", i, err)
			}
			return nil, panel.Problems{{Path: fmt.Sprintf("where[%d]", i), Message: err.Error()}}, nil
		}
		q.Where = append(q.Where, e)
		if t == tierNormalized {
			f.Exactness = Normalized
			f.Notices = append(f.Notices, Notice{Path: fmt.Sprintf("where[%d]", i), Message: fmt.Sprintf("where[%d] is shown as %s; stored text is %q", i, printEntry(e, false), text)})
		}
	}
	if len(stored.Measures) > 0 || stored.Histogram != nil || len(stored.By) > 0 || stored.Bucket != "" {
		q.Stage = &aggregation{Bucket: stored.Bucket}
		if stored.Histogram != nil {
			field, ok := liftFieldText(stored.From, stored.Histogram.Field)
			if !ok {
				return nil, panel.Problems{{Path: "histogram.field", Message: "histogram field cannot be expressed in FanQL"}}, nil
			}
			q.Stage.Histogram = &field
		} else {
			for i, text := range stored.Measures {
				m, t, err := liftMeasure(ctx, p, stored.From, text)
				if err != nil {
					if operationalFormatError(err) {
						return nil, nil, fmt.Errorf("format measures[%d]: %w", i, err)
					}
					return nil, panel.Problems{{Path: fmt.Sprintf("measures[%d]", i), Message: err.Error()}}, nil
				}
				q.Stage.Measures = append(q.Stage.Measures, m)
				if t == tierNormalized {
					f.Exactness = Normalized
					f.Notices = append(f.Notices, Notice{Path: fmt.Sprintf("measures[%d]", i), Message: fmt.Sprintf("measures[%d] is shown as %s; stored text is %q", i, printMeasure(m), text)})
				}
			}
		}
		for _, text := range stored.By {
			field, ok := liftFieldText(stored.From, text)
			if !ok {
				return nil, panel.Problems{{Path: "by", Message: "grouping field cannot be expressed in FanQL"}}, nil
			}
			q.Stage.By = append(q.Stage.By, field)
		}
	}
	if stored.Sort != "" {
		q.Sort = &sortClause{Key: strings.TrimPrefix(stored.Sort, "+"), Ascending: strings.HasPrefix(stored.Sort, "+")}
	}
	if stored.Limit != 0 {
		q.Limit = &limitClause{N: stored.Limit}
	}
	if opts.Viz == "traces" && stored.Sort == "errors" {
		f.Notices = append(f.Notices, Notice{Path: "sort", Message: "sort errors also filters to error spans; new queries: write status:error … sort duration"})
	}
	f.Notices = append(f.Notices, ignoredClauseNotices(stored, opts.Viz)...)
	f.Text = print(q)
	f.Reason = editableReason(f.Text)
	f.Editable = f.Reason == ""
	return f, nil, nil
}

func groupedNumber(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func editableReason(text string) string {
	bound := func(n, max int, unit string) string {
		return fmt.Sprintf("formatted text is %s %s; the editor accepts %s", groupedNumber(n), unit, groupedNumber(max))
	}
	if n := utf8.RuneCountInString(text); n > maxTextRunes {
		return bound(n, maxTextRunes, "characters")
	}
	if len(text) > maxTextBytes {
		return bound(len(text), maxTextBytes, "bytes")
	}
	tokens := lex(text)
	if n := len(tokens) - 1; n > maxTokens {
		return bound(n, maxTokens, "tokens")
	}
	// The internal parser is unbounded; its counters still record the complete
	// tree, so oversized stored text is never truncated by interactive parsing.
	p := &parser{vocab: currentVocabulary(), runes: []rune(text), tokens: tokens, heights: make(map[any]int)}
	p.query("")
	if p.nodes > maxNodes {
		return bound(p.nodes, maxNodes, "nodes")
	}
	observedDepth, lexicalDepth := 0, 0
	for _, token := range tokens {
		if token.Kind == tokenPunct && token.Text == "(" {
			lexicalDepth++
			observedDepth = max(observedDepth, lexicalDepth)
		}
		if token.Kind == tokenPunct && token.Text == ")" {
			lexicalDepth--
		}
	}
	for _, height := range p.heights {
		observedDepth = max(observedDepth, height)
	}
	if observedDepth > maxDepth {
		return bound(observedDepth, maxDepth, "levels of nesting depth")
	}
	return ""
}
