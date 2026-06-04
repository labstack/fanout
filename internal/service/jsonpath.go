package service

// jsonAttrPath builds a DuckDB JSON path that addresses a single top-level key by
// its literal name. Attribute keys are flat but dotted (e.g. "http.method"), so the
// segment must be double-quoted — an unquoted '$.http.method' would be parsed as a
// nested path and never match the flat {"http.method": ...} object written at ingest.
//
// The key is passed to json_extract_string as a bound parameter, so there is no
// SQL-injection surface. We deliberately do not escape embedded double-quotes:
// DuckDB has no accepted escape for them inside a quoted path member (doubling
// raises a binder error), such keys are vanishingly rare in OTLP, and this keeps
// us consistent with the attr() macro and attribute-discovery paths, which also
// quote without escaping.
func jsonAttrPath(key string) string {
	return `$."` + key + `"`
}
