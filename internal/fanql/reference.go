package fanql

import (
	"fmt"
	"slices"
	"strings"

	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/telemetry"
)

// Reference is the shared API, MCP resource and generated-site reference.
// Registry-owned names, field metadata and vocabularies are read on every call.
func Reference() string {
	var b strings.Builder
	b.WriteString("# FanQL\n\nPreview: the syntax may change before GA\n\n")
	b.WriteString("FanQL is the text form of a structured telemetry query. It produces the same `panel.Query` that dashboards store and the panel engine executes. Panels store the query; text is derived. Problems prevent execution; notices never block it. Each diagnostic says what happened and what to write instead, using a valid example from that field. Diagnostics use display names without internal visualization identifiers and report all independent problems in one pass, in source order. Only follow-on cascades are suppressed.\n\n")
	b.WriteString("## Shape and entry points\n\n`source [where] filters | measures by fields every bucket sort key limit n`\n\nExplore accepts a whole query. Logs and Traces search bars accept filters only; the page supplies source, `where` and visualization. The visualization picker accepts the query as written. Adding `| count() every auto` to make a timeseries is a visible edit. One aggregation stage is allowed; another pipe may introduce only `sort` or `limit`, and canonical text drops that pipe.\n\n")
	b.WriteString("## Sources and words\n\n")
	fmt.Fprintf(&b, "Sources: %s, recognized only as the first token. A bare source selects all rows in the chosen time range. `where` is optional; canonical text supplies it when filters exist.\n\n", referenceWords(referenceKeys(sources)))
	fmt.Fprintf(&b, "Clause words: %s and `|`, recognized at depth zero, outside quotes and brackets, at a term boundary. After operators they are values (`service:where`, `service in (sort, limit)`). Quote a clause word to search for it.\n\nExpression words: %s, recognized only in their grammatical positions. Modifiers: %s. Keywords, columns, functions, units and vocabulary aliases ignore case. Values and attribute/resource keys preserve case.\n\n", referenceWords(referenceKeys(clauses)), referenceWords(referenceKeys(expressionWords)), referenceWords(referenceKeys(modifiers)))
	b.WriteString("A word is a field only before a field operator; otherwise it searches log `body` or span `operation`. Metrics require an explicit field. Quotes use doubling as the only escape; backslashes are literal. Quoted `api*` and embedded `$` stay quoted. Wildcards are unsupported except `k:*` and `count(*)`. A leading `-` is negation except when a numeric value is expected. `+` is legal in a sort key or exponent.\n\n")
	b.WriteString("## Operators and missing values\n\n| Operator | Meaning |\n|---|---|\n")
	for _, row := range [][2]string{
		{":", "Equality with vocabulary aliases; missing values are excluded."}, {"=", "Equality, with the same typing and alias rules as `:`."},
		{"!=", "SQL spelling, SQL meaning: rows without `k` are excluded; `-k:v` includes them."}, {"<>", "SQL inequality, equivalent to `!=`; missing values are excluded."},
		{">", "Greater than; missing values are excluded."}, {">=", "Greater than or equal; missing values are excluded."}, {"<", "Less than; missing values are excluded."}, {"<=", "Less than or equal; missing values are excluded."},
		{"~", "Case-insensitive substring; `%` and `_` are ordinary characters."}, {"!~", "Null-inclusive substring exclusion, like `-k ~ v`."},
		{"in", "Membership in a nonempty list or multi-value variable; missing values are excluded."}, {"not in", "SQL spelling, SQL meaning: missing values are excluded; prefix `-` includes them."},
		{"between", "Inclusive lower and upper bounds separated by `and`; missing values are excluded."}, {"not between", "SQL spelling, SQL meaning: missing values are excluded; prefix `-` includes them."},
		{"is null", "Matches missing values, canonical `-k:*`."}, {"is not null", "Matches present values, canonical `k:*`."},
		{"like", "Case-sensitive SQL pattern; requires quoted text; `%` and `_` are wildcards."}, {"ilike", "Case-insensitive SQL pattern; requires quoted text."},
		{"not like", "SQL spelling, SQL meaning: missing values are excluded; prefix `-` includes them."}, {"not ilike", "SQL spelling, SQL meaning: missing values are excluded; prefix `-` includes them."},
	} {
		fmt.Fprintf(&b, "| `%s` | %s |\n", row[0], row[1])
	}
	b.WriteString("\nPrefix `-` or `not` excludes a term and includes rows where its predicate is NULL. Repeated negation cancels in pairs. SQL operators retain SQL null behavior. Compound negation follows De Morgan's law. TRY_CAST failures are NULL, so `-http_status_code between 400 and 599` includes uncastable codes.\n\nWhitespace and `and` join filters. An `or` chain must occupy its whole filter list or be parenthesized: `a:1 (b:2 or c:3)` and `(a:1 b:2) or c:3` differ. This rule also applies inside groups and `count_if`. Mixing conjunction and `or` without parentheses is a problem with suggested repairs.\n\n")
	b.WriteString("## Fields\n\nA single identifier is a source column when one exists, otherwise an attribute key with a notice. Dotted `http.target` means `attributes['http.target']`; `resource.k8s.pod.name` means `resource['k8s.pod.name']`. Explicit `attributes.<path>` uses the attributes map. Bracket keys are literal nonempty strings, including spaces, Unicode, punctuation and doubled quotes. Keys beginning `resource.` or `attributes.` retain brackets where needed to preserve the map.\n\nCanonical text brackets single-segment attribute keys, so new columns cannot reinterpret canonical or stored text. Map columns require a key. `duration` denotes `duration_ms` in filters, measure arguments and trace sorts. `duration_ms>500` takes a number; `duration>500ms` requires a unit. A measure alias named duration or duration_ms stays that alias in sort. Time columns accept range comparisons against quoted timestamps, not equality shorthand.\n\n")
	for _, signal := range panel.SignalNames() {
		fmt.Fprintf(&b, "### %s\n\n| Name | Type | Unit | Low cardinality | Description |\n|---|---|---|---|---|\n", signal)
		columns, _ := panel.SignalColumns(signal)
		lowCard := panel.LowCardinalityColumns(signal)
		for _, column := range columns {
			unit, low := column.Unit, ""
			if unit == "" {
				unit = "none"
			}
			if slices.Contains(lowCard, column.Name) {
				low = "yes"
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %s | %s |\n", column.Name, column.Type, unit, low, referenceCell(column.Description))
		}
		b.WriteByte('\n')
	}
	b.WriteString("`http_status_code` is text. `<`, `<=`, `>`, `>=` and `between` use `TRY_CAST(http_status_code AS INTEGER)` for literals and variables alike. Equality and membership stay text, so `http_status_code:500` means `'500'`.\n\n")
	b.WriteString("## Vocabularies and severity\n\nAliases apply under equality, inequality and membership, including prefix negation. Quoted values, contains, SQL patterns and function arguments are literal. Stored spellings are also accepted bare.\n\n")
	for _, vocabulary := range []struct {
		name    string
		aliases map[string]string
	}{{"status (spans)", panel.FieldAliases("status")}, {"kind (spans)", panel.FieldAliases("kind")}, {"type (metrics)", panel.FieldAliases("type")}, {"severity (logs)", telemetry.SeverityAliases()}} {
		fmt.Fprintf(&b, "### %s\n\n| Input | Stored |\n|---|---|\n", vocabulary.name)
		for _, alias := range referenceKeys(vocabulary.aliases) {
			fmt.Fprintf(&b, "| `%s` | `%s` |\n", alias, vocabulary.aliases[alias])
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "Severity bands, lowest first: %s. `unspecified` maps to `UNSPECIFIED`; unknown names are uppercased. Trailing digits are dropped (`INFO2` becomes `INFO`). Unknown severity is legal for equality; sampled stored levels may provide a notice. Unknown status or kind aliases are problems.\n\n", referenceWords(telemetry.SeverityBands[:]))
	b.WriteString("Every stored log severity is normalized once: a positive severity number has authority (21+ FATAL, 17+ ERROR, 13+ WARN, 9+ INFO, 5+ DEBUG, 1+ TRACE); absent/nonpositive numbers fall back to text. Invariant: no stored log row bypasses normalization. Severity ranges expand to contiguous band membership, never CASE. `severity>=warn` means WARN, ERROR, FATAL; `severity<error` means TRACE, DEBUG, INFO, WARN; `between info and error` is inclusive. Unknown and UNSPECIFIED levels have no rank and never satisfy a range. Use `severity_number>=14` for sublevels. Empty or reversed ranges are problems.\n\nOn normalized log rows, `error_rate()` counts exactly `severity>=error`. Its backend test is `upper(severity) IN ('ERROR', 'FATAL', 'CRITICAL') OR severity_number >= 17`; normalization makes legacy CRITICAL redundant. Span `error_rate()` counts `status:error`.\n\n")
	b.WriteString("## Literals and durations\n\n| Literal | Rule |\n|---|---|\n| String | Bare words or single/double quotes; double a quote to escape it. Quoted values are strings and bypass aliases. |\n| Number | Integers, decimals and exponents (`500`, `1.5`, `2e3`, `-3`); non-finite values are rejected. Numbers stay numeric on number columns and attributes, and stay text as typed on string columns. |\n| Boolean | Bare `true`/`false`, case-insensitive; attributes retain boolean type, string columns compare text, number columns reject them. |\n| Duration | `us`, `ms`, `s`, `m`, `h`, only on `duration`; converted to milliseconds. Examples: `500ms`, `1.5s`, `250us`, `2m`, `1h`. Negative, compound and overflowing durations are rejected. |\n| Timestamp | Quoted RFC 3339 or `YYYY-MM-DD HH:MM:SS`, optional fraction/offset; no offset means UTC. Time comparisons retain nanoseconds through TIMESTAMPTZ_NS. |\n| Existence | `k:*` is IS NOT NULL; `-k:*` is IS NULL. |\n| Free text | Case-insensitive substring of log body or span operation; explicit field required on metrics. |\n\nContains emits `contains(lower(field), lower(value))`; map values first cast to VARCHAR. SQL LIKE/ILIKE require quoted patterns; ESCAPE is not expressible, so use `~` when `%` should be literal.\n\n")
	b.WriteString("## Buckets\n\n")
	fmt.Fprintf(&b, "%s. `auto` lets the engine choose an interval. A bucket follows `every` and belongs to an aggregate query.\n\n", referenceWords(panel.BucketNames()))
	b.WriteString("## Measures\n\nThe registry defines accepted functions and signal availability. Fields follow the filter/grouping resolution rules. Numeric attribute measures use TRY_CAST AS DOUBLE; nonnumeric values become NULL. `count(*)` canonicalizes to `count()`.\n\n| Name | Form | Signals | Unit | Semantics |\n|---|---|---|---|---|\n")
	for _, m := range panel.MeasureSpecs() {
		form := m.Name + "()"
		if m.Args == 1 {
			form = m.Name + "(f)"
		} else if m.Args == 2 {
			form = m.Name + "(f, q)"
		}
		signals := m.Signals
		if len(signals) == 0 {
			signals = panel.SignalNames()
		}
		unit := m.Unit
		if unit == "" {
			unit = "field unit"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | %s |\n", m.Name, form, strings.Join(signals, ", "), unit, referenceMeasureMeaning(m.Name))
	}
	b.WriteString("\nMetric aggregates require a standalone `name:<metric>` equality entry. Counters use per-series increases and reset handling; gauge/updown use point values; histograms use observation counts, sums and buckets; summaries use available stored aggregates. Unsupported kind/function combinations report a diagnosis instead of treating counters as gauges.\n\n`histogram(duration)` on spans uses count with log2 duration buckets; `histogram(value)` on metrics uses explicit histogram buckets. Distributions stand alone, with at most one `by` field for a histogram; `every` selects a heatmap.\n\n")
	b.WriteString("## Expressions and ranking\n\n`count_if(filters)` takes a FanQL filter list as one entry and compiles to checked `count(*) FILTER (WHERE ...)::DOUBLE`. Raw entries are allowed; variables are forbidden. Spans and logs support `+ - * /`, constants, unary minus and parentheses on aggregate vizzes. Metrics, distributions and log_patterns do not support arithmetic. Every expression needs at least one aggregate. Other than a single call, an expression requires `as alias`; aliases are 1–40 lowercase letters/digits/underscores and start with a letter. `share()` stands alone. Division emits `a / nullif(b, 0)`, so zero denominators yield NULL.\n\nCount-family ratios use ratio units, `* 100` makes percent, addition/subtraction retains equal units, scaling retains the measure unit, other combinations use none. Expressions are not additive. Affine transformations inherit the underlying ranking and flip direction for a negative scale. Binomial count_if/count ratios use Wilson ranking with a 20-sample floor; other expressions use value ranking with leaf/denominator support. Ineligible series follow eligible ones by support, then dimension; result metadata discloses them.\n\nApdex uses disjoint predicates: `(count_if(duration<=500ms) + count_if(duration>500ms duration<=2s) / 2) / count() as apdex`. Exactly 500 ms counts once; NULL duration counts in the population and neither numerator.\n\nEach measure allows 256 tokens, 64 nodes, depth 8 and 8 aggregates. A query allows 256 expression nodes and 24 aggregates across at most 6 measures. A count_if filter is at most 500 bytes. Whitespace is not a token. Budgets apply to structured inputs too.\n\n")
	b.WriteString("## Filter functions and raw entries\n\n| Function | Signature |\n|---|---|\n")
	for _, name := range panel.FilterFunctionNames() {
		args := "field"
		if name == "starts_with" || name == "contains" || name == "regexp_matches" {
			args = "field, quoted_string"
		}
		fmt.Fprintf(&b, "| `%s` | `%s(%s)` |\n", name, name, args)
	}
	b.WriteString("\n`lower`, `upper` and `length` need a comparison. `starts_with`, `contains` and `regexp_matches` are predicates alone. Calls have no space before `(`. Variables may compare call results, but remain forbidden inside count_if.\n\nA backtick-delimited raw entry passes SQL to the existing filter gate; double a backtick to include it. A whole raw entry retains its stored spelling; raw fragments composed with other predicates are parenthesized. The gate accepts columns, literal `attributes['k']`/`resource['k']`, comparisons, boolean logic, IN, LIKE, IS NULL, BETWEEN, coalesce, variables, the filter functions above and casts to VARCHAR, BIGINT, INTEGER, DOUBLE, BOOLEAN, UBIGINT and TIMESTAMPTZ_NS. It rejects CASE, DISTINCT, ORDER BY and FILTER inside functions, qualified columns, subqueries and other functions. Raw cannot bypass the gate.\n\n")
	b.WriteString("## Variables and entry boundaries\n\n`$name` is a dashboard variable; `$__` names are reserved. It stays a parameter, never a string literal. `k in $multi` accepts multiple values; scalar operators reject multi variables. Without declarations, references produce notices so editing can continue; execution checks them.\n\nAll rule: a filter that mentions a variable set to All is removed; keep fixed conditions in their own terms. An empty variable list makes its entry FALSE. Top-level terms are separate entries; a parenthesized group, raw entry or OR chain is one entry. Stored order and boundaries survive formatting.\n\n| Filters | Entries | `$service` = All |\n|---|---|---|\n| `service:$service status:error` | 2 | Keep status. |\n| `(service:$service status:error)` | 1 | Drop both. |\n| `service:$service or service:worker` | 1 | Drop whole entry. |\n| `service:$service (status:error or status:unset)` | 2 | Keep status group. |\n| `-service:$service` | 1 | Drop entry; nothing excluded. |\n| Raw SQL mentioning `$service` | 1 | Drop whole entry. |\n| `count_if(service:$service)` | n/a | Rejected. |\n\n")
	b.WriteString("## Visualization and trace semantics\n\nLogs/traces use logs/spans respectively and forbid measures, grouping, buckets and histogram. Log_patterns uses `count() by body_template`, optionally a bucket. Timeseries needs a bucket and at most one grouping field, with one measure when grouped. Tables allow three grouping fields; bars two, with one measure when grouped by two. Stat/gauge need one measure and no grouping/bucket; gauge needs min/max. Scatter needs two measures and one or two grouping fields. State_timeline needs one measure, one grouping field, a bucket and thresholds.\n\nTraces are `(namespace, trace_id)` candidates with at least one matching span in the window, ranked by matching spans' max(duration_ms), or min(start_time) for `sort +start`. Limit applies to candidates; the engine reads at most 1,001 for truncation detection. Each returns one root span, or its earliest span in the window if the root is outside it, with error status if any span in the trace errored. Displayed service and duration are root service and root duration. A child can match while the returned root has different values.\n\nTrace sorts: descending `duration` (stored duration_ms), `errors`, or ascending `+start`. `sort errors` is the legacy combined key: it also filters to error spans, then orders by duration. Prefer `status:error` with `sort duration`. Logs sort by `time` or `+time`; log_patterns by `count`. Aggregate sorts name a measure alias. Default/desc is descending; +/asc is ascending; choose one direction. Bucketed families use time order and options.top for series selection; sort/limit are accepted with a notice when ignored.\n\n")
	b.WriteString("## Reserved words and foreign syntax\n\n")
	fmt.Fprintf(&b, "%s are reserved at clause positions. Quote them to search for the word. Foreign syntax gets a diagnostic and FanQL hint, never an alias; an ordinary foreign word in filter position stays free text.\n\n", referenceWords(referenceKeys(reserved)))
	fmt.Fprintf(&b, "Recognized foreign stages: %s. Foreign call shapes: %s. `from`, `index=`, `source=`, first-token `search`, `ago(`, `now(`, `earliest=`, `_time:`, `order by`, `group by`, `facet`, `==`, `=~`, `|=`, `|~`, `::`, selectors and `@field:` get targeted hints. Time ranges belong to the time picker.\n\n", referenceWords(referenceKeys(foreignStages)), referenceWords(referenceKeys(foreignCalls)))
	b.WriteString("## Bounds and diagnostics\n\nInteractive text allows 4,000 code points and 8,192 bytes, 1,024 tokens, 512 AST nodes, nesting depth 16 and lists of 100 values. Queries lower to 16 entries of 500 bytes each, 6 measures and 3 grouping fields, further constrained by the viz. Completion returns at most 50 items. Problems have code-point spans and optional fix-its, follow source order and are capped at 20 plus a final more-problems diagnostic. Any problem prevents execution; requested repair previews are labeled not executable.\n\nFormat returns complete text and exactness `exact` or `normalized`, with notices for normalization. Raw preserves whole SQL entries. Editable canonical text reparses under its tier without merging entries. Beyond any interactive bound, `editable: false` carries a reason and complete text; the envelope must preserve the original query and consumers must not parse the oversized text.\n\n")
	b.WriteString("## Examples\n\n```fanql\n")
	for _, example := range referenceExamples {
		b.WriteString(example)
		b.WriteByte('\n')
	}
	b.WriteString("```\n\n## Transport\n\nMachine text inputs carry visualization, time, comparison, declarations, resolved values, namespace and options as needed. The structured query is required when editable is false. Panels keep storing queries, never text.\n\nCompletion accepts a relative `window` duration or absolute `from` and `to` timestamps. Absolute windows and durations over 24h use recent 24h schema, disclosed by `schema.recent` and `schema.notice`. With no selected window, the same recent fallback applies.\n\nHTTP exposes `/api/fanql/parse`, `/api/fanql/format`, `/api/fanql/complete` and `/api/fanql/reference`, requiring telemetry:read. MCP query_telemetry accepts optional text beside panel; agents should prefer panel.query, or panel.sql for complex work. This reference is also `fanout://fanql/reference`.\n")
	return b.String()
}

func referenceKeys[V any](table map[string]V) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func referenceWords(words []string) string {
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = "`" + word + "`"
	}
	return strings.Join(quoted, ", ")
}

func referenceCell(text string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "<", "&lt;", ">", "&gt;").Replace(text)
}

func referenceMeasureMeaning(name string) string {
	switch name {
	case "count":
		return "Rows; metric observation/point count according to kind."
	case "rate":
		return "Spans/logs: rows per second over bucket/window. Metrics: coverage-weighted counter increase per second; counters only."
	case "error_rate":
		return "Spans: percent status:error. Logs: percent severity>=error on normalized rows. Unavailable for metrics."
	case "share":
		return "Percent of scoped rows in the group; metrics use series population. Standalone only."
	case "avg":
		return "Numeric average. Metrics: gauge/updown point average; histogram sum/count; counter has no value and reports a diagnosis."
	case "min":
		return "Minimum numeric value, retaining metric-kind semantics."
	case "max":
		return "Maximum numeric value, retaining metric-kind semantics."
	case "sum":
		return "Numeric total; metric counters use increases instead of summing cumulative points."
	case "last":
		return "Metrics only: kind-aware last value."
	case "count_distinct":
		return "Distinct values of any field, including string attributes."
	case "quantile":
		return "Quantile q strictly between 0 and 1; numeric observations or native metric distribution semantics."
	default:
		return "Registered percentile of numeric observations; metric distributions retain native semantics."
	}
}

var referenceExamples = []string{
	"spans where kind:server | error_rate() by service every 1m",
	"spans where service:checkout duration>500ms sort duration limit 50",
	"spans where service:$service (status:error or status:unset) | count() by http_route",
	"spans where kind:server | count_if(http_status_code>=500) / count() * 100 as pct_5xx by http_route",
	"spans where kind:server | (count_if(duration<=500ms) + count_if(duration>500ms duration<=2s) / 2) / count() as apdex",
	"spans | histogram(duration) by service",
	"spans where service:checkout status:error -http.target:/healthz | count() by http_route",
	"spans where user.id:* | count_distinct(user.id) by service",
	"logs where service:api severity:error timeout sort time limit 200",
	"logs where severity>=warn | count() by body_template sort count limit 10",
	"metrics where name:http.server.requests | rate() by service every 1m",
	"metrics where name:k8s.container.restarts | last(value) by resource.k8s.pod.name",
}
