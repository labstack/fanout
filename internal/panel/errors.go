package panel

import (
	"log/slog"
	"regexp"
)

// absolutePaths matches filesystem paths in engine errors. Quoted paths may
// contain spaces; an unquoted path needs at least two segments, so division
// in an echoed expression (duration_ms/1000) is left alone.
var absolutePaths = regexp.MustCompile(`(?i)(?:"(?:[a-z]:[\\/]|/)[^"]*"|'(?:[a-z]:[\\/]|/)[^']*'|(?:[a-z]:[\\/]|/)[^\s'"<>:,;()\[\]{}/]+[\\/][^\s'"<>:,;()\[\]{}]+)`)

// RedactPaths keeps operational filesystem details off the wire.
func RedactPaths(message string) string { return absolutePaths.ReplaceAllString(message, "<path>") }

// SafeError records the original database error before removing paths.
func SafeError(err error) string {
	slog.Warn("telemetry operation failed", "error", err)
	return RedactPaths(err.Error())
}
