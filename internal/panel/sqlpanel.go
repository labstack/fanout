package panel

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	windowMacro = regexp.MustCompile(`\$__window\(\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\)`)
	bucketMacro = regexp.MustCompile(`\$__bucket\(\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\)`)
	macroToken  = regexp.MustCompile(`\$__([A-Za-z_]+)`)
)

// expandMacros rewrites $__window(col) into the binding the AGENTS.md
// timestamp rule requires and $__bucket(col) into a time_bucket call with the
// panel's interval. Only identifier arguments are accepted, so a macro can
// never carry SQL. Every SQL panel must filter time with $__window: the read
// window prunes files, and a panel that ignored it would silently see only
// the files that overlap the window.
func expandMacros(sql string, interval time.Duration) (string, error) {
	if !windowMacro.MatchString(sql) {
		return "", errors.New("SQL panels must filter time with $__window(time_column)")
	}
	out := windowMacro.ReplaceAllString(sql, `$1 >= $$__from::TIMESTAMP_NS::TIMESTAMPTZ_NS AND $1 < $$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS`)
	seconds := int64(interval / time.Second)
	if seconds <= 0 {
		seconds = 60
	}
	out = bucketMacro.ReplaceAllString(out, fmt.Sprintf(`time_bucket(INTERVAL '%d seconds', $1::TIMESTAMP_NS)`, seconds))
	for _, m := range macroToken.FindAllStringSubmatch(stripQuoted(out), -1) {
		if m[1] != "from" && m[1] != "to" {
			return "", fmt.Errorf("unknown macro $__%s; use $__window(column) or $__bucket(column)", m[1])
		}
	}
	return out, nil
}

// canonicalSQL parses macro-expanded panel SQL, renders it back with DuckDB,
// and requires the rendering to carry the read window. Callers bind and run
// the returned text, never the author's: a $__window inside a comment or
// string, a literal ?, or an unusual lexical form cannot survive the
// rendering. The order is expandMacros, canonicalSQL, bindParams,
// PrepareTelemetrySQL; the executor must follow it.
func canonicalSQL(ctx context.Context, parser Parser, expanded string) (string, error) {
	node, err := parser.ParseSQL(ctx, expanded)
	if err != nil {
		return "", err
	}
	rendered, err := parser.RenderSQL(ctx, node)
	if err != nil {
		return "", err
	}
	code := stripQuoted(rendered)
	if !strings.Contains(code, "$__from") || !strings.Contains(code, "$__to") {
		return "", errors.New("SQL panels must filter time with $__window(time_column)")
	}
	return rendered, nil
}
