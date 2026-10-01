package query

import (
	"math"
	"strings"
)

// JSON cannot encode nonfinite doubles. Keep them as the ProtoJSON strings
// NaN, Infinity and -Infinity at the product boundary; storage remains typed.
func jsonFloat(value float64) any {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	default:
		return value
	}
}

func normalizeJSONNumbers(text string) string {
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
