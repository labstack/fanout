package mcp

// Helper functions for SQL escaping

func escapeLike(str string) string {
	result := ""
	for _, c := range str {
		if c == '\'' {
			result += "''"
		} else if c == '\\' {
			result += "\\\\"
		} else {
			result += string(c)
		}
	}
	return result
}
