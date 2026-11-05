package engine

import "strings"

// NormalizeJSON makes the JSON text of a serialized column parseable. DuckDB
// writes nonfinite doubles as bare NaN, Infinity and -Infinity, which JSON
// cannot encode; they become the ProtoJSON strings of the same names. Storage
// remains typed.
func NormalizeJSON(text string) string {
	var output strings.Builder
	quoted, escaped := false, false
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if c != 'N' && c != 'I' && c != '-' {
			continue
		}
		end := i
		for end < len(text) && !strings.ContainsRune(" ,]}:\t\r\n", rune(text[end])) {
			end++
		}
		token := text[i:end]
		if token == "NaN" || token == "Infinity" || token == "-Infinity" {
			output.WriteString(text[start:i])
			output.WriteByte('"')
			output.WriteString(token)
			output.WriteByte('"')
			start = end
		}
		i = end - 1
	}
	if start == 0 {
		return text
	}
	output.WriteString(text[start:])
	return output.String()
}
