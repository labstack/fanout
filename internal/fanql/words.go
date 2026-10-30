package fanql

import (
	"fmt"
	"github.com/labstack/fanout/internal/panel"
	"strings"
)

var sources = wordSet("spans logs metrics")
var clauses = wordSet("where by every sort limit")
var expressionWords = wordSet("and or not in between is null like ilike")
var modifiers = wordSet("as asc desc")
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
type diagnosticCode uint8

const (
	diagMeasureHyphen diagnosticCode = iota
	diagTraces
	diagTextBound
	diagTokensBound
	diagNodesBound
	diagDepthBound
	diagListBound
	diagMore
	diagSource
	diagFrom
	diagPageWhere
	diagPageFilter
	diagWhereEmpty
	diagWhereTwice
	diagMixed
	diagSides
	diagValue
	diagColon
	diagFieldBefore
	diagField
	diagBracket
	diagBetween
	diagLike
	diagNull
	diagList
	diagListValue
	diagCallSpace
	diagCallArg
	diagCallClose
	diagUnclosedGroup
	diagUnmatched
	diagQuote
	diagRaw
	diagUnexpected
	diagPlus
	diagWildcard
	diagVariable
	diagReserved
	diagForeignStage
	diagForeignCall
	diagForeignClause
	diagTime
	diagTimeCall
	diagEqeq
	diagRegex
	diagPipeOp
	diagCast
	diagSelector
	diagAt
	diagBang
	diagMeasure
	diagMeasureSides
	diagMeasureName
	diagAggregate
	diagShare
	diagHistogram
	diagHistogramField
	diagHistogramAlias
	diagMeasureArg
	diagMeasureComma
	diagStarArg
	diagAlias
	diagSecondStage
	diagEveryFirst
	diagByFirst
	diagBucket
	diagByMax
	diagHistogramBy
	diagDirection
	diagSortKey
	diagLimitBeforeSort
	diagLimit
	diagDuplicate
	diagOrder
)

func message(code diagnosticCode) string {
	switch code {
	case diagMeasureHyphen:
		return "unexpected - in %s; write attributes[%s]"
	case diagTextBound:
		return "a query is at most 4000 characters (8192 bytes)"
	case diagTokensBound:
		return "a query is at most 1024 tokens"
	case diagNodesBound:
		return "a query is at most 512 nodes"
	case diagDepthBound:
		return "a query has at most 16 levels of nesting depth"
	case diagListBound:
		return "an in list is at most 100 values"
	case diagMore:
		return "more problems follow; fix these first"
	case diagSource:
		return "unknown source %q"
	case diagFrom:
		return "%q is not FanQL; start with the source: spans where ..."
	case diagPageWhere:
		return "the page supplies where"
	case diagPageFilter:
		return "the page accepts filters only; measures, sort and limit belong in Explore"
	case diagWhereEmpty:
		return "where needs a filter"
	case diagWhereTwice:
		return "where appears twice; combine the filters into one where"
	case diagMixed:
		return "mixed and/or needs parentheses"
	case diagSides:
		return "%s needs a filter on both sides"
	case diagValue:
		return "%s%s needs a value; e.g. %s%s%s"
	case diagColon:
		return "unexpected : after %s"
	case diagFieldBefore:
		return "a filter needs a field before %s"
	case diagField:
		return "a field needs a name, e.g. service or attributes['key']"
	case diagBracket:
		return "a bracket field needs a quoted key and ]"
	case diagBetween:
		return "between needs two values"
	case diagLike:
		return "like needs a quoted pattern"
	case diagNull:
		return "is needs null or not null"
	case diagList:
		return "in needs a list or variable, e.g. in (api, web)"
	case diagListValue:
		return "a list needs a value after each comma"
	case diagCallSpace:
		return "write %s(...) without a space"
	case diagCallArg:
		return "%s needs a field, quoted string, number or nested call"
	case diagCallClose:
		return "unclosed call; add )"
	case diagUnclosedGroup:
		return "unclosed (; add ) before |, sort, limit or the end"
	case diagUnmatched:
		return "unmatched ); remove it or add the opening ("
	case diagQuote:
		return "unclosed quote; close the value with %c"
	case diagRaw:
		return "unclosed raw filter; close it with `"
	case diagUnexpected:
		return "unexpected %s"
	case diagPlus:
		return "unexpected +"
	case diagWildcard:
		return "* wildcards are not in FanQL 1"
	case diagVariable:
		return "$__ names are reserved; use a dashboard variable name"
	case diagReserved:
		return "%q is reserved for a future FanQL feature"
	case diagForeignStage:
		return "%q is not FanQL; write the measures after |, e.g. | count() by service"
	case diagForeignCall:
		return "%s(...) is not FanQL; use the FanQL measure functions"
	case diagForeignClause:
		return "%s is not FanQL; use by after measures, e.g. | count() by service"
	case diagTime:
		return "the time range is not part of FanQL 1; use the time picker (%s is reserved)"
	case diagTimeCall:
		return "%s(...) is not FanQL; use the time picker"
	case diagEqeq:
		return "== is not FanQL; use key:value or ="
	case diagRegex:
		return "=~ is not FanQL; ~ is contains; use regexp_matches(field, 're') for a regular expression"
	case diagPipeOp:
		return "%s is not FanQL; use body ~ \"text\" for contains"
	case diagCast:
		return ":: is not FanQL; use field comparisons or a backtick raw filter"
	case diagSelector:
		return "{ selectors are not FanQL; write labels as key:value, e.g. service:api"
	case diagAt:
		return "%q is not a field name; write the attribute without @: %s"
	case diagBang:
		return "! negation is not FanQL; write -%s"
	case diagMeasure:
		return "a measure needs a call, number or parenthesised expression"
	case diagMeasureSides:
		return "%s needs a measure or number on both sides"
	case diagMeasureName:
		return "name this expression with as, e.g. count() / 2 as half"
	case diagAggregate:
		return "a measure needs an aggregate; constants can only scale one, e.g. count() * 100"
	case diagShare:
		return "share() cannot be combined; it is already a percentage of the scope"
	case diagHistogram:
		return "distribution counts require count(); histogram(...) stands alone"
	case diagHistogramField:
		return "histogram needs one field, e.g. histogram(duration)"
	case diagHistogramAlias:
		return "histogram(...) does not take an alias"
	case diagMeasureArg:
		return "a measure argument needs a field, number or *"
	case diagMeasureComma:
		return "separate measures with a comma"
	case diagStarArg:
		return "* is only allowed as the sole argument of count(*)"
	case diagAlias:
		return "as needs a measure alias"
	case diagSecondStage:
		return "FanQL 1 runs one aggregation stage"
	case diagEveryFirst:
		return "every needs measures first; write | count() every 1m"
	case diagByFirst:
		return "by needs measures first; write | count() by service"
	case diagBucket:
		return "unknown bucket %q; use auto or one of " + strings.Join(panel.BucketNames()[1:], ", ")
	case diagByMax:
		return "at most 3 grouping fields"
	case diagHistogramBy:
		return "histogram accepts at most one grouping field"
	case diagDirection:
		return "sort takes one direction"
	case diagSortKey:
		return "sort needs a key, e.g. sort duration"
	case diagLimitBeforeSort:
		return "sort comes before limit: … sort %s limit %d"
	case diagLimit:
		return "limit needs a whole number from 1 to 1000; e.g. limit 50"
	case diagDuplicate:
		return "%s appears twice"
	case diagOrder:
		return "%s must follow measures and come before sort and limit"

	}
	panic("unknown diagnostic code")
}

func hint(code diagnosticCode) string {
	switch code {
	case diagSource:
		return "use spans, logs or metrics"
	case diagTraces:
		return "use spans; traces is a viz of spans"
	case diagReserved:
		return "quote it to search for the word"
	case diagShare:
		return "use share() alone, or count_if(...) / count() * 100 for a custom ratio"
	case diagSecondStage:
		return "remove the second |; more stages come in a later version"

	}
	return ""
}

func diagnostic(code diagnosticCode, args ...any) string { return fmt.Sprintf(message(code), args...) }
func whereHint(word string) string {
	return fmt.Sprintf("quote %q to search for the word, or drop where", word)
}
func wildcardHint(word string) string { return fmt.Sprintf("use ~ for contains, or quote %q", word) }
func quoteValueHint(field, value string) string {
	return fmt.Sprintf("quote the value: %s%q", field, value)
}
func groupingHint(a, b string) string { return "write " + a + " or " + b }
