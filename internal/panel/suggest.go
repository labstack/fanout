package panel

import "strings"

// suggest returns "did you mean X?" for the closest candidate within a small
// edit distance, or "" when nothing is close.
func suggest(word string, candidates []string) string {
	// A candidate that contains the word ("route" in "http_route") is the
	// strongest hint; prefer the shortest such candidate.
	contained := ""
	for _, candidate := range candidates {
		if len(word) >= 3 && strings.Contains(candidate, word) && (contained == "" || len(candidate) < len(contained)) {
			contained = candidate
		}
	}
	if contained != "" && contained != word {
		return "did you mean " + contained + "?"
	}
	best, bestDistance := "", len(word)/3+2
	for _, candidate := range candidates {
		if d := editDistance(word, candidate); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	if best == "" {
		return ""
	}
	return "did you mean " + best + "?"
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
