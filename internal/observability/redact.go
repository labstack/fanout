package observability

import "regexp"

var sensitiveLogPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{
		pattern:     regexp.MustCompile(`(?i)([?&](?:api[_-]?key|access[_-]?token|auth(?:orization)?|client[_-]?secret|password|passwd|secret|token)=)[^&\s'\"]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)(\b(?:bearer|basic)\s+)[A-Za-z0-9._~+/=-]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|authorization|client[_-]?secret|password|passwd|secret|token)\b\s*[:=]\s*)(?:\"[^\"]*\"|'[^']*'|[^\s,;&]+(?:\s+\[REDACTED\])?)`),
		replacement: `${1}[REDACTED]`,
	},
}

func redactLogBody(body string) string {
	for _, item := range sensitiveLogPatterns {
		body = item.pattern.ReplaceAllString(body, item.replacement)
	}
	return body
}
