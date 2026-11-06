// Package sqlscan scans SQL quoted regions and comments without interpreting SQL.
package sqlscan

// Region identifies a string, quoted identifier, or comment starting at a byte
// offset. End is exclusive; Message is nonempty for an unterminated region.
type Region struct {
	End     int
	Quote   byte
	Comment bool
	Message string
}

// At returns the region at start, or an empty region for ordinary SQL text.
// SQL quotes escape themselves by doubling; block comments may nest.
func At(text string, start int) Region {
	if start >= len(text) {
		return Region{}
	}
	c := text[start]
	if c == '\'' || c == '"' {
		for i := start + 1; i < len(text); i++ {
			if text[i] == c {
				if i+1 < len(text) && text[i+1] == c {
					i++
					continue
				}
				return Region{End: i + 1, Quote: c}
			}
		}
		return Region{End: len(text), Quote: c, Message: "unterminated quoted literal"}
	}
	if start+1 >= len(text) {
		return Region{}
	}
	if text[start:start+2] == "--" {
		i := start + 2
		for i < len(text) && text[i] != '\n' && text[i] != '\r' {
			i++
		}
		return Region{End: i, Comment: true}
	}
	if text[start:start+2] == "/*" {
		i, depth := start+2, 1
		for i < len(text) {
			switch {
			case i+1 < len(text) && text[i:i+2] == "/*":
				depth++
				i += 2
			case i+1 < len(text) && text[i:i+2] == "*/":
				depth--
				i += 2
				if depth == 0 {
					return Region{End: i, Comment: true}
				}
			default:
				i++
			}
		}
		return Region{End: i, Comment: true, Message: "unterminated block comment"}
	}
	return Region{}
}
