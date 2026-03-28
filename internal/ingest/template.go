package ingest

import (
	"regexp"
)

var (
	reUUID      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	reIPv4      = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d+)?\b`)
	reEmail     = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	reHexLong   = regexp.MustCompile(`\b0x[0-9a-fA-F]{4,}\b|\b[0-9a-fA-F]{8,}\b`)
	rePath      = regexp.MustCompile(`(/[a-zA-Z0-9._-]+){2,}(\?[^\s"]*)?`)
	reQuoted    = regexp.MustCompile(`"[^"]{1,200}"`)
	reNumber    = regexp.MustCompile(`\b\d+(\.\d+)?\b`)
)

// normalizeText runs all 8 regexes in order against a plain-text log body,
// replacing variable content with typed placeholders.
func normalizeText(body string) string {
	body = reUUID.ReplaceAllString(body, "<uuid>")
	body = reTimestamp.ReplaceAllString(body, "<ts>")
	body = reIPv4.ReplaceAllString(body, "<ip>")
	body = reEmail.ReplaceAllString(body, "<email>")
	body = reHexLong.ReplaceAllString(body, "<hex>")
	body = rePath.ReplaceAllString(body, "<path>")
	body = reQuoted.ReplaceAllString(body, "<str>")
	body = reNumber.ReplaceAllString(body, "<num>")
	return body
}
