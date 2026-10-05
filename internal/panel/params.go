package panel

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// listCast matches the rest of a CAST that wraps a list parameter, so each
// list value gets its own CAST(? AS T).
var listCast = regexp.MustCompile(`^ AS (VARCHAR|DOUBLE|TIMESTAMPTZ_NS)\)`)

// bindParams replaces every $name outside quotes and comments with
// placeholders and returns the values in order. A name looked up as a list
// renders as bare ?, ? (the IN parentheses and the CAST that types each
// parameter come from the canonical text; an empty list is NULL). The text
// must already be DuckDB's canonical rendering. describe is the same text
// with NULL in place of each placeholder, for DESCRIBE. A literal ? or a
// positional $1 is an error: values only ever enter through variables.
func bindParams(text string, lookup func(name string) (values []any, list bool, err error)) (query, describe string, args []any, err error) {
	var q, d strings.Builder
	write := func(s string) { q.WriteString(s); d.WriteString(s) }
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '\'' || c == '"':
			end := i + 1
			for end < len(text) {
				if text[end] == c {
					if end+1 < len(text) && text[end+1] == c {
						end += 2
						continue
					}
					break
				}
				end++
			}
			if end >= len(text) {
				return "", "", nil, errors.New("unterminated quoted text")
			}
			write(text[i : end+1])
			i = end
		case c == '-' && i+1 < len(text) && text[i+1] == '-':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			}
			write(text[i : i+end])
			i += end - 1
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return "", "", nil, errors.New("unterminated comment")
			}
			write(text[i : i+2+end+2])
			i += 2 + end + 1
		case c == '?':
			return "", "", nil, errors.New("use $variables instead of ? placeholders")
		case c == '$' && i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9':
			return "", "", nil, errors.New("positional parameters such as $1 are not allowed; use $name variables")
		case c == '$' && i+1 < len(text) && isIdentStart(text[i+1]):
			end := i + 1
			for end < len(text) && isIdentPart(text[end]) {
				end++
			}
			name := text[i+1 : end]
			values, list, lookupErr := lookup(name)
			if lookupErr != nil {
				return "", "", nil, lookupErr
			}
			if list {
				marks, nulls := make([]string, len(values)), make([]string, len(values))
				for j := range marks {
					marks[j], nulls[j] = "?", "NULL"
				}
				if len(values) == 0 {
					marks, nulls = []string{"NULL"}, []string{"NULL"}
				}
				// A list inside CAST($x AS T) becomes CAST(? AS T), CAST(? AS T).
				if m := listCast.FindStringSubmatch(text[end:]); m != nil && strings.HasSuffix(q.String(), "CAST(") {
					trimmed, trimmedD := strings.TrimSuffix(q.String(), "CAST("), strings.TrimSuffix(d.String(), "CAST(")
					q.Reset()
					d.Reset()
					q.WriteString(trimmed)
					d.WriteString(trimmedD)
					for j := range marks {
						if len(values) > 0 {
							marks[j], nulls[j] = "CAST(? AS "+m[1]+")", "CAST(NULL AS "+m[1]+")"
						}
					}
					end += len(m[0])
				}
				q.WriteString(strings.Join(marks, ", "))
				d.WriteString(strings.Join(nulls, ", "))
			} else {
				if len(values) != 1 {
					return "", "", nil, fmt.Errorf("$%s has %d values where one is expected", name, len(values))
				}
				q.WriteString("?")
				d.WriteString("NULL")
			}
			args = append(args, values...)
			i = end - 1
		default:
			q.WriteByte(c)
			d.WriteByte(c)
		}
	}
	return q.String(), d.String(), args, nil
}

func isIdentStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isIdentPart(c byte) bool  { return isIdentStart(c) || c >= '0' && c <= '9' }
