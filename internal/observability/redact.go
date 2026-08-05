package observability

import (
	"regexp"
	"strings"
)

// Log-body redaction is the barrier between log-borne secrets and authenticated
// viewers who are allowed to inspect telemetry, so treat every change in this
// file as security-sensitive.
//
// Coverage boundary: redaction is applied ONLY to log body fields on the
// read path — Logs entries, trace-correlated logs, and the SQL-side search
// filter built from these same patterns (see redactLogBodySQL). Span
// status_message, span/log attributes JSON, and all on-disk data are NOT
// redacted.
//
// The patterns are exported string constants (not just compiled regexps) so
// the identical RE2 source can be embedded in DuckDB regexp_replace() calls:
// DuckDB uses RE2, the same engine as Go's regexp, which keeps Go-side
// display redaction and SQL-side search redaction in lockstep
// (TestRedactSQLMatchesGo asserts the parity). Case-insensitivity comes
// from the inline (?i) flags, never from engine options.
const (
	// SensitiveURLParamPattern redacts secret-bearing URL query parameter
	// values (?api_key=..., &token=...).
	SensitiveURLParamPattern = `(?i)([?&](?:api[_-]?key|access[_-]?token|auth(?:orization)?|client[_-]?secret|password|passwd|secret|token)=)[^&\s'"]+`
	// SensitiveAuthSchemePattern redacts HTTP auth-scheme credentials
	// (Bearer eyJ..., Basic dXNlcjpwYXNz).
	SensitiveAuthSchemePattern = `(?i)(\b(?:bearer|basic)\s+)[A-Za-z0-9._~+/=-]+`
	// SensitiveKeyValuePattern redacts bare key=value / key: value forms.
	// The trailing (?:\s+\[REDACTED\])? exists solely to collapse
	// SensitiveAuthSchemePattern's output ("Authorization: Bearer [REDACTED]"
	// -> "Authorization: [REDACTED]", see redact_test.go), which only works
	// because this pattern runs AFTER the auth-scheme pattern in
	// sensitiveLogPatterns. Reordering the slice silently changes results.
	SensitiveKeyValuePattern = `(?i)(\b(?:api[_-]?key|access[_-]?token|authorization|client[_-]?secret|password|passwd|secret|token)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;&]+(?:\s+\[REDACTED\])?)`
	// SensitiveJSONFieldPattern redacts quoted-key JSON string fields
	// ({"password":"hunter2"}), the dominant structured-log form and this
	// project's own logging convention. It replaces the entire quoted value,
	// so after SensitiveAuthSchemePattern has run,
	// {"Authorization":"Bearer x"} collapses to
	// {"Authorization":"[REDACTED]"}. Best-effort limits: only the exact key
	// names listed match (a compound key like "refresh_token" does NOT,
	// mirroring the \b behavior of SensitiveKeyValuePattern), only string
	// values are redacted (numeric/object values pass through), and escaped
	// quotes inside the value ("pa\"ss") are handled but an escaped quote
	// inside the KEY defeats the match.
	SensitiveJSONFieldPattern = `(?i)("(?:api[_-]?key|access[_-]?token|auth(?:orization)?|client[_-]?secret|password|passwd|secret|token)"\s*:\s*)"(?:[^"\\]|\\.)*"`
)

// sensitiveLogPatterns is order-sensitive: SensitiveKeyValuePattern and
// SensitiveJSONFieldPattern both depend on running after
// SensitiveAuthSchemePattern to collapse its "[REDACTED]" output (see the
// constant docs above). redactLogBodySQL replays the same patterns in the
// same order on the SQL side.
var sensitiveLogPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{
		pattern:     regexp.MustCompile(SensitiveURLParamPattern),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(SensitiveAuthSchemePattern),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(SensitiveKeyValuePattern),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(SensitiveJSONFieldPattern),
		replacement: `${1}"[REDACTED]"`,
	},
}

// redactLogBody strips sensitive values from a log body before it leaves the
// read path. See the coverage-boundary note at the top of this file.
func redactLogBody(body string) string {
	for _, item := range sensitiveLogPatterns {
		body = item.pattern.ReplaceAllString(body, item.replacement)
	}
	return body
}

// redactLogBodySQL returns a DuckDB expression applying the same redaction as
// redactLogBody to the given column. It is built entirely from the
// compile-time pattern constants above — no runtime input reaches the SQL
// text, so there is no injection surface. Go's `${1}` rewrite syntax becomes
// RE2/DuckDB's `\1`; single quotes are doubled for the SQL literal.
func redactLogBodySQL(column string) string {
	expr := column
	for _, item := range sensitiveLogPatterns {
		pattern := strings.ReplaceAll(item.pattern.String(), `'`, `''`)
		rewrite := strings.ReplaceAll(item.replacement, `${1}`, `\1`)
		rewrite = strings.ReplaceAll(rewrite, `'`, `''`)
		expr = "regexp_replace(" + expr + ", '" + pattern + "', '" + rewrite + "', 'g')"
	}
	return expr
}
