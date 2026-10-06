package panel

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func patternLimit(p *Panel) int {
	if p.Query.Limit > 0 {
		return min(50, p.Query.Limit)
	}
	return 20
}
func rowLimitForPanel(p *Panel) int {
	if p.Query.Limit > 0 {
		return min(1000, p.Query.Limit)
	}
	return 1000
}
func compileRows(p *Panel, filters []Filter, scope Scope) (Compiled, error) {
	sig, ok := lookupSignal(p.Query.From)
	if !ok {
		return Compiled{}, fmt.Errorf("unknown signal %q", p.Query.From)
	}
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	if p.Viz == "traces" {
		order := "duration_ms DESC,trace_id,namespace"
		if p.Query.Sort == "errors" {
			where += " AND status IN ('STATUS_CODE_ERROR','ERROR')"
		}
		if p.Query.Sort == "+start" {
			order = "start_time ASC,trace_id,namespace"
		}
		text := checkedTraceRowsSQL(where, rowLimitForPanel(p)+1, order)
		args = append(args, scope.Start.UTC(), scope.End.UTC())
		return Compiled{SQL: text, Args: args, Columns: []Column{{Name: "trace_id", Type: "string", Role: "dimension"}, {Name: "namespace", Type: "string", Role: "dimension"}, {Name: "service", Type: "string", Role: "dimension"}, {Name: "operation", Type: "string", Role: "dimension"}, {Name: "duration_ms", Type: "number", Role: "measure", Unit: "ms"}, {Name: "status", Type: "string", Role: "dimension"}, {Name: "start", Type: "time", Role: "time"}}}, nil
	}
	redacted := redactedLogSource()
	if p.Options != nil && p.Options.Highlight != "" {
		where += " AND contains(lower(body),lower(?))"
		args = append(args, p.Options.Highlight)
	}
	base := "WITH redacted AS (" + redacted + "),base AS (SELECT * FROM redacted WHERE " + where + ")"
	if p.Viz == "logs" {
		order := "DESC"
		if p.Query.Sort == "+time" {
			order = "ASC"
		}
		return Compiled{SQL: base + ` SELECT epoch_ms(time::TIMESTAMP_NS)::BIGINT AS time,coalesce(severity,''),coalesce(service,''),CASE WHEN length(body)>2000 THEN left(body,1999)||'…' ELSE coalesce(body,'') END,coalesce(trace_id,''),coalesce(namespace,'') FROM base ORDER BY base.time ` + order + fmt.Sprintf(" LIMIT %d", rowLimitForPanel(p)+1), Args: args, Columns: []Column{{Name: "time", Type: "time", Role: "time"}, {Name: "severity", Type: "string", Role: "dimension"}, {Name: "service", Type: "string", Role: "dimension"}, {Name: "body", Type: "string", Role: "dimension"}, {Name: "trace_id", Type: "string", Role: "dimension"}, {Name: "namespace", Type: "string", Role: "dimension"}}}, nil
	}
	interval := max(scope.Interval, AutoInterval(scope.End.Sub(scope.Start), 960))
	seconds := int64(interval / time.Second)
	lo := alignedBucketMillis(scope.Start, interval)
	points := int((scope.End.Sub(time.UnixMilli(lo))-time.Nanosecond)/interval + 1)
	for points > 240 {
		interval *= 2
		seconds = int64(interval / time.Second)
		lo = alignedBucketMillis(scope.Start, interval)
		points = int((scope.End.Sub(time.UnixMilli(lo))-time.Nanosecond)/interval + 1)
	}
	limit := patternLimit(p)
	text := base + fmt.Sprintf(`,
patterns AS (SELECT coalesce(body_template,'') AS pattern,count(*)::DOUBLE AS total FROM base GROUP BY 1 ORDER BY total DESC,pattern LIMIT %d),
buckets AS (SELECT coalesce(body_template,'') AS pattern,epoch_ms(time_bucket(INTERVAL '%d seconds',time::TIMESTAMP_NS,'1970-01-01'::TIMESTAMP_NS))::BIGINT AS point,count(*)::DOUBLE AS n FROM base SEMI JOIN patterns p ON coalesce(base.body_template,'')=p.pattern GROUP BY 1,2),
dense AS (SELECT p.pattern,p.total,%d+r.i*%d AS point FROM patterns p CROSS JOIN range(%d) r(i))
SELECT d.pattern,d.total,CAST(to_json(list(coalesce(b.n,0) ORDER BY d.point)) AS VARCHAR) AS trend
FROM dense d LEFT JOIN buckets b ON d.pattern=b.pattern AND d.point=b.point
GROUP BY d.pattern,d.total ORDER BY d.total DESC,d.pattern`, limit+1, seconds, lo, interval.Milliseconds(), points)
	return Compiled{SQL: text, Args: args, TrendInterval: interval, TrendStart: time.UnixMilli(lo).UTC(), Columns: []Column{{Name: "body_template", Type: "string", Role: "dimension"}, {Name: "count", Type: "number", Role: "measure", Unit: "count"}, {Name: "trend", Type: "json", Role: "dimension", Unit: "count"}}}, nil
}
func alignedBucketMillis(t time.Time, interval time.Duration) int64 {
	n, width := t.UnixMilli(), interval.Milliseconds()
	q := n / width
	if n%width < 0 {
		q--
	}
	return q * width
}

func validateRows(p *Panel, path string, problems *Problems) {
	isRow := p.Viz == "logs" || p.Viz == "traces" || p.Viz == "log_patterns"
	if p.Options != nil && p.Options.Highlight != "" {
		if p.Viz != "logs" && p.Viz != "log_patterns" {
			problems.addHint(path+".options.highlight", "highlight applies only to logs and log_patterns", "remove highlight or use a logs or log_patterns panel")
		}
		if utf8.RuneCountInString(p.Options.Highlight) > 200 {
			problems.addHint(path+".options.highlight", "highlight is limited to 200 characters", "shorten highlight to at most 200 characters")
		}
	}
	if !isRow {
		return
	}
	if p.Unit != "" {
		problems.addHint(path+".unit", "row panels cannot set unit", "remove unit; row columns have fixed units")
	}
	if len(p.Thresholds) > 0 {
		problems.addHint(path+".thresholds", "row panels cannot set thresholds", "remove thresholds")
	}
	if p.Options != nil {
		if strings.TrimSpace(p.Options.Style) != "" {
			problems.addHint(path+".options.style", "row panels cannot set chart styles", "remove options.style")
		}
		if p.Options.Top != 0 {
			problems.addHint(path+".options.top", "row panels cannot set options.top", "remove options.top; use query.limit")
		}
		if p.Options.Scale != "" {
			problems.addHint(path+".options.scale", "row panels cannot set options.scale", "remove options.scale")
		}
	}
	from := "logs"
	if p.Viz == "traces" {
		from = "spans"
	}
	if p.SQL != "" {
		problems.addHint(path+".sql", "row panels cannot use SQL", "use query {from: "+from+"}")
	}
	if p.Query == nil {
		if p.SQL == "" {
			problems.addHint(path+".query", "row panels require a structured query", "use query {from: "+from+"}")
		}
		return
	}
	q := p.Query
	if q.Limit < 0 || q.Limit > 1000 {
		problems.addHint(path+".query.limit", "limit must be 0 to 1000", "use a limit from 1 to 1000 or omit it for the default")
	}
	if q.Histogram != nil {
		problems.addHint(path+".query.histogram", "histogram applies only to heatmap and histogram", "remove histogram or use a heatmap or histogram panel")
	}
	if p.Viz == "traces" {
		if q.From != "spans" {
			problems.addHint(path+".query.from", "traces reads spans", "use query {from: spans}")
		}
		if q.Sort != "" && q.Sort != "duration_ms" && q.Sort != "errors" && q.Sort != "+start" {
			problems.addHint(path+".query.sort", "traces sort is duration_ms, errors or +start", "use sort duration_ms, errors, +start or omit sort")
		}
	} else {
		if q.From != "logs" {
			problems.addHint(path+".query.from", "logs and log_patterns read logs", "use query {from: logs}")
		}
	}
	if p.Viz == "logs" && q.Sort != "" && q.Sort != "time" && q.Sort != "+time" {
		problems.addHint(path+".query.sort", "logs sort is time or +time", "use sort time, +time or omit sort")
	}
	if p.Viz == "log_patterns" {
		if len(q.By) != 1 || q.By[0] != "body_template" {
			problems.addHint(path+".query.by", "log_patterns groups by body_template", "use by [body_template]")
		}
		if len(q.Measures) != 1 || q.Measures[0] != "count()" {
			problems.addHint(path+".query.measures", "log_patterns requires count()", "use measures [count()]")
		}
		if q.Sort != "" && q.Sort != "count" {
			problems.addHint(path+".query.sort", "log_patterns sort is count", "use sort count or omit sort")
		}
		if q.Bucket != "" && q.Bucket != "auto" {
			if _, ok := buckets[q.Bucket]; !ok {
				problems.addHint(path+".query.bucket", "bucket must be auto or a supported interval", "use auto, 10s, 30s, 1m, 5m, 10m, 15m, 30m, 1h, 3h, 6h, 12h or 1d")
			}
		}
	}
	if p.Viz == "logs" || p.Viz == "traces" {
		if len(q.Measures) > 0 {
			problems.addHint(path+".query.measures", "fixed row projections do not take aggregate measures", "remove measures; use log_patterns for counts or a table for aggregates")
		}
		if len(q.By) > 0 {
			problems.addHint(path+".query.by", "logs and traces panels do not group; use log_patterns or a table", "remove by or use log_patterns or a table")
		}
		if q.Bucket != "" {
			problems.addHint(path+".query.bucket", "logs and traces panels do not take a bucket", "remove bucket")
		}
	}
}
