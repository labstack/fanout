// Package search provides query parsing for log/trace search.
package search

import (
	"strings"
	"unicode"
)

// Query represents a parsed search query.
type Query struct {
	Terms   []string            // text patterns to match (AND'd)
	Exclude []string            // patterns to exclude (-term)
	Fields  map[string][]string // field:value filters
}

// Parse parses a search query string into a Query struct.
//
// Syntax:
//
//	word              - match logs containing "word"
//	"multi word"      - match exact phrase
//	-word             - exclude logs containing "word"
//	field:value       - filter by field (service, severity)
//	field:val1,val2   - filter by multiple values
//
// Multiple terms are AND'd together.
func Parse(input string) *Query {
	q := &Query{
		Fields: make(map[string][]string),
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return q
	}

	tokens := tokenize(input)
	for _, tok := range tokens {
		switch {
		case strings.HasPrefix(tok, "-") && len(tok) > 1:
			// Exclude pattern
			q.Exclude = append(q.Exclude, tok[1:])

		case strings.Contains(tok, ":"):
			// Field filter
			parts := strings.SplitN(tok, ":", 2)
			if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
				field := strings.ToLower(parts[0])
				values := strings.Split(parts[1], ",")
				for _, v := range values {
					v = strings.TrimSpace(v)
					if v != "" {
						q.Fields[field] = append(q.Fields[field], v)
					}
				}
			}

		default:
			// Regular search term
			if tok != "" {
				q.Terms = append(q.Terms, tok)
			}
		}
	}

	return q
}

// tokenize splits input into tokens, respecting quoted strings.
func tokenize(input string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range input {
		switch {
		case (r == '"' || r == '\'') && !inQuote:
			inQuote = true
			quoteChar = r

		case r == quoteChar && inQuote:
			// End quote - add token
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			inQuote = false
			quoteChar = 0

		case unicode.IsSpace(r) && !inQuote:
			// Space outside quotes - end token
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}

		default:
			current.WriteRune(r)
		}
	}

	// Add final token
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// IsEmpty returns true if the query has no filters.
func (q *Query) IsEmpty() bool {
	return len(q.Terms) == 0 && len(q.Exclude) == 0 && len(q.Fields) == 0
}

// Service returns service filter values, or nil if not set.
func (q *Query) Service() []string {
	return q.Fields["service"]
}

// Severity returns severity filter values, or nil if not set.
func (q *Query) Severity() []string {
	return q.Fields["severity"]
}

// Name returns name filter values, or nil if not set.
func (q *Query) Name() []string {
	return q.Fields["name"]
}

// Type returns type filter values, or nil if not set.
func (q *Query) Type() []string {
	return q.Fields["type"]
}

// Status returns status filter values (error, slow), or nil if not set.
func (q *Query) Status() []string {
	return q.Fields["status"]
}

// Duration returns duration filter (e.g., ">1000", "<500"), or empty if not set.
func (q *Query) Duration() string {
	if vals := q.Fields["duration"]; len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// Operation returns operation filter values, or nil if not set.
func (q *Query) Operation() []string {
	return q.Fields["op"]
}

// Namespace returns namespace filter values, or nil if not set.
func (q *Query) Namespace() []string {
	return q.Fields["namespace"]
}

// Attr returns attribute filters as key=value pairs from attr:key=value syntax.
func (q *Query) Attr() map[string]string {
	attrs := make(map[string]string)
	for _, v := range q.Fields["attr"] {
		if idx := strings.Index(v, "="); idx > 0 {
			attrs[v[:idx]] = v[idx+1:]
		}
	}
	return attrs
}

// TraceID returns trace_id filter value, or empty if not set.
func (q *Query) TraceID() string {
	if vals := q.Fields["trace"]; len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// SpanID returns span_id filter value, or empty if not set.
func (q *Query) SpanID() string {
	if vals := q.Fields["span"]; len(vals) > 0 {
		return vals[0]
	}
	return ""
}
