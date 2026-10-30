package sqlscan

import "slices"

// Parameters returns named SQL parameters in source order, excluding quoted
// text and comments. It does not parse or authorize the SQL expression.
func Parameters(text string) []string {
	var names []string
	ident := func(c byte) bool { return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
	for i := 0; i < len(text); {
		if region := At(text, i); region.End > i {
			i = region.End
			continue
		}
		if text[i] != '$' || i+1 == len(text) || !ident(text[i+1]) {
			i++
			continue
		}
		start := i + 1
		i += 2
		for i < len(text) && (ident(text[i]) || text[i] >= '0' && text[i] <= '9') {
			i++
		}
		name := text[start:i]
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}
