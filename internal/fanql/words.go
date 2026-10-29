package fanql

import (
	"fmt"
	"strings"
)

var sources = wordSet("spans logs metrics")
var clauses = wordSet("where by every sort limit")
var expressionWords = wordSet("and or not in between is null like ilike")
var modifiers = wordSet("as asc desc")
var buckets = wordSet("auto 10s 30s 1m 5m 10m 15m 30m 1h 3h 6h 12h 1d")
var reserved = wordSet("let join correlate compare since until top eval extend having fields rename over window")
var foreignStages = wordSet("stats summarize select project extend eval fields rename take head top order sort")
var foreignCalls = wordSet("bin span percentile perc95 pct count_over_time quantile_over_time mean median dc distinct_count cardinality")

func wordSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, word := range strings.Fields(s) {
		set[word] = true
	}
	return set
}

// Centralize diagnostic wording so the parser and future completion share it.
var messages = map[string]string{
	"textBound":       "a query is at most 4000 characters (8192 bytes)",
	"tokensBound":     "a query is at most 1024 tokens",
	"nodesBound":      "a query is at most 512 nodes",
	"depthBound":      "a query has at most 16 levels of nesting depth",
	"listBound":       "an in list is at most 100 values",
	"more":            "more problems follow; fix these first",
	"source":          "unknown source %q",
	"from":            "%q is not FanQL; start with the source: spans where ...",
	"pageWhere":       "the page supplies where",
	"pageFilter":      "the page accepts filters only; measures, sort and limit belong in Explore",
	"whereEmpty":      "where needs a filter",
	"whereTwice":      "where appears twice; combine the filters into one where",
	"mixed":           "mixed and/or needs parentheses",
	"sides":           "%s needs a filter on both sides",
	"value":           "%s%s needs a value; e.g. %s%s%s",
	"colon":           "unexpected : after %s",
	"fieldBefore":     "a filter needs a field before %s",
	"field":           "a field needs a name, e.g. service or attributes['key']",
	"bracket":         "a bracket field needs a quoted key and ]",
	"between":         "between needs two values",
	"like":            "like needs a quoted pattern",
	"null":            "is needs null or not null",
	"list":            "in needs a list or variable, e.g. in (api, web)",
	"listValue":       "a list needs a value after each comma",
	"callSpace":       "write %s(...) without a space",
	"callArg":         "%s needs a field, quoted string, number or nested call",
	"callClose":       "unclosed call; add )",
	"unclosedGroup":   "unclosed (; add ) before |, sort, limit or the end",
	"unmatched":       "unmatched ); remove it or add the opening (",
	"quote":           "unclosed quote; close the value with %c",
	"raw":             "unclosed raw filter; close it with `",
	"unexpected":      "unexpected %s",
	"plus":            "unexpected +",
	"wildcard":        "* wildcards are not in FanQL 1",
	"variable":        "$__ names are reserved; use a dashboard variable name",
	"reserved":        "%q is reserved for a future FanQL feature",
	"foreignStage":    "%q is not FanQL; write the measures after |, e.g. | count() by service",
	"foreignCall":     "%s(...) is not FanQL; use the FanQL measure functions",
	"foreignClause":   "%s is not FanQL; use by after measures, e.g. | count() by service",
	"time":            "the time range is not part of FanQL 1; use the time picker (%s is reserved)",
	"timeCall":        "%s(...) is not FanQL; use the time picker",
	"eqeq":            "== is not FanQL; use key:value or =",
	"regex":           "=~ is not FanQL; ~ is contains; use regexp_matches(field, 're') for a regular expression",
	"pipeOp":          "%s is not FanQL; use body ~ \"text\" for contains",
	"cast":            ":: is not FanQL; use field comparisons or a backtick raw filter",
	"selector":        "{ selectors are not FanQL; write labels as key:value, e.g. service:api",
	"at":              "%q is not a field name; write the attribute without @: %s",
	"bang":            "! negation is not FanQL; write -%s",
	"measure":         "a measure needs a call, number or parenthesised expression",
	"measureSides":    "%s needs a measure or number on both sides",
	"measureName":     "name this expression with as, e.g. count() / 2 as half",
	"aggregate":       "a measure needs an aggregate; constants can only scale one, e.g. count() * 100",
	"share":           "share() cannot be combined; it is already a percentage of the scope",
	"histogram":       "distribution counts require count(); histogram(...) stands alone",
	"histogramField":  "histogram needs one field, e.g. histogram(duration)",
	"histogramAlias":  "histogram(...) does not take an alias",
	"measureArg":      "a measure argument needs a field, number or *",
	"measureComma":    "separate measures with a comma",
	"starArg":         "* is only allowed as the sole argument of count(*)",
	"alias":           "as needs a measure alias",
	"secondStage":     "FanQL 1 runs one aggregation stage",
	"everyFirst":      "every needs measures first; write | count() every 1m",
	"byFirst":         "by needs measures first; write | count() by service",
	"bucket":          "unknown bucket %q; use auto or one of 10s, 30s, 1m, 5m, 10m, 15m, 30m, 1h, 3h, 6h, 12h, 1d",
	"byMax":           "at most 3 grouping fields",
	"histogramBy":     "histogram accepts at most one grouping field",
	"direction":       "sort takes one direction",
	"sortKey":         "sort needs a key, e.g. sort duration",
	"limitBeforeSort": "limit comes before sort here; write ... sort duration limit 10",
	"limit":           "limit needs a whole number from 1 to 1000; e.g. limit 50",
	"duplicate":       "%s appears twice",
	"order":           "%s must follow measures and come before sort and limit",
}

var hints = map[string]string{
	"source":      "use spans, logs or metrics",
	"traces":      "use spans; traces is a viz of spans",
	"reserved":    "quote it to search for the word",
	"share":       "use share() alone, or count_if(...) / count() * 100 for a custom ratio",
	"secondStage": "remove the second |; more stages come in a later version",
}

func diagnostic(code string, args ...any) string { return fmt.Sprintf(messages[code], args...) }
func whereHint(word string) string {
	return fmt.Sprintf("quote %q to search for the word, or drop where", word)
}
func wildcardHint(word string) string { return fmt.Sprintf("use ~ for contains, or quote %q", word) }
func quoteValueHint(field, value string) string {
	return fmt.Sprintf("quote the value: %s%q", field, value)
}
func groupingHint(a, b string) string { return "write " + a + " or " + b }
