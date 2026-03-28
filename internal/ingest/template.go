package ingest

import (
	"bytes"
	"encoding/json"
	"regexp"
	"unicode/utf8"
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

// truncateUTF8 truncates s to at most maxBytes bytes without splitting a
// multi-byte rune.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk back from maxBytes until we find a rune-start boundary.
	b := maxBytes
	for b > 0 && !utf8.RuneStart(s[b]) {
		b--
	}
	return s[:b]
}

// normalizeTemplate is the entry point. It truncates to 500 bytes (UTF-8
// safe), detects JSON by leading '{', and dispatches accordingly.
func normalizeTemplate(body string) string {
	body = truncateUTF8(body, 500)
	if len(body) > 0 && body[0] == '{' {
		return normalizeJSON(body)
	}
	return normalizeText(body)
}

// normalizeJSON unmarshals, walks values, and re-marshals. Falls back to
// normalizeText on invalid JSON.
func normalizeJSON(body string) string {
	var v interface{}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return normalizeText(body)
	}
	normalized := normalizeJSONValues(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalized); err != nil {
		return normalizeText(body)
	}
	// json.Encoder.Encode appends a trailing newline; trim it.
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

// normalizeJSONValues recursively normalizes JSON values:
//   - string → normalizeText applied
//   - float64 → "<num>"
//   - bool/nil → preserved
//   - map/slice → recursed
func normalizeJSONValues(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return normalizeText(val)
	case float64:
		return "<num>"
	case bool:
		return val
	case nil:
		return nil
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, child := range val {
			out[k] = normalizeJSONValues(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, child := range val {
			out[i] = normalizeJSONValues(child)
		}
		return out
	default:
		return val
	}
}
