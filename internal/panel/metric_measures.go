package panel

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

func metricMeasurePlan(m Measure) (source, perSeries, acrossSeries string) {
	if m.Field != nil && (m.Field.Column != "value" || m.Field.Key != "") && m.Func != "count_distinct" {
		// _value is the finite FieldRef.numberSQL() projection over raw visible points.
		switch m.Func {
		case "sum", "avg", "min", "max":
			return "metric_raw_window", "list(_value) FILTER (WHERE _value IS NOT NULL)",
				"CASE WHEN len(flatten(list(_v)))>0 THEN list_aggregate(flatten(list(_v)), '" + m.Func + "')::DOUBLE END"
		case "last":
			return "metric_raw_window", "arg_max(_value,(time,ingested_unix_nano))", "arg_max(_v,_last_order)::DOUBLE"
		default:
			return "metric_raw_window", "list(_value) FILTER (WHERE _value IS NOT NULL)",
				"CASE WHEN len(flatten(list(_v)))>0 THEN list_aggregate(flatten(list(_v)), 'quantile_cont', " + sqlFloat(m.Q) + ")::DOUBLE END"
		}
	}
	scalar := "CASE WHEN type IN ('gauge','updown','counter') THEN _value END"
	gauge := "CASE WHEN type IN ('gauge','updown') THEN _value END"
	switch m.Func {
	case "count":
		return "metric_raw_window", "count(*)::DOUBLE", "sum(_v)::DOUBLE"
	case "count_distinct":
		return "metric_raw_window", "list(DISTINCT _distinct) FILTER (WHERE _distinct IS NOT NULL)", "list_unique(flatten(list(_v)))::DOUBLE"
	case "share":
		return "metric_raw_window", "count(*)::DOUBLE", "100.0*sum(_v)/nullif(sum(sum(_v)) OVER (_partition),0)"
	case "rate":
		return "metric_increases", "sum(_rate_share ORDER BY time,ingested_unix_nano) FILTER (WHERE _rate_covered>0)/nullif(sum(_rate_covered ORDER BY time,ingested_unix_nano) FILTER (WHERE _rate_covered>0),0)", "sum(_v ORDER BY " + metricSeriesKey + ")::DOUBLE"
	case "sum":
		return "metric_value_rows", "CASE WHEN type IN ('gauge','updown') THEN avg(_value) WHEN type='counter' THEN sum(increase) WHEN type='histogram' THEN sum(sum_increase) END", "sum(_v)::DOUBLE"
	case "avg":
		return "metric_gauge_rows", "CASE WHEN type='histogram' THEN sum(sum_increase) ELSE avg(" + gauge + ") END", "sum(_v)/nullif(sum(_weight),0)::DOUBLE"
	case "last":
		return "metric_last_points", "arg_max(" + scalar + ",(time,ingested_unix_nano))", "sum(_v)::DOUBLE"
	case "min", "max":
		return "metric_window_points", m.Func + "(" + scalar + ")", m.Func + "(_v)::DOUBLE"
	default:
		return "metric_window_points", "list(" + gauge + ") FILTER (WHERE " + gauge + " IS NOT NULL)",
			"CASE WHEN len(flatten(list(_v)))>0 THEN list_aggregate(flatten(list(_v)), 'quantile_cont', " + sqlFloat(m.Q) + ")::DOUBLE END"
	}
}

// Only value measures use series semantics. Every other numeric field and
// point operation reads the original window, including retries and summaries.
func metricValueMeasure(m Measure) bool {
	return m.Field != nil && m.Field.Column == "value" && m.Field.Key == "" && m.Func != "count_distinct"
}

func metricMeasureKinds(m Measure) []string {
	var kinds []string
	switch m.Func {
	case "rate":
		kinds = []string{"counter"}
	case "sum", "last", "min", "max":
		kinds = []string{"gauge", "updown", "counter"}
	default:
		kinds = []string{"gauge", "updown"}
	}
	if metricHistogramMeasure(m) {
		kinds = append(kinds, "histogram")
	}
	return kinds
}

func compileMetricQuery(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error) {
	for _, m := range measures {
		if m.IsExpression() || m.Func == "count_if" {
			return Compiled{}, fmt.Errorf("measure arithmetic is not available for metrics")
		}
	}

	sig := signals["metrics"]
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	bucket := "0::BIGINT"
	bucketed := scope.Interval > 0
	if bucketed {
		bucket = metricBucketSQL(scope.Interval)
	}
	ctes := []string{"metric_raw_window AS (SELECT *," + bucket + " AS _bucket FROM metrics WHERE " + where + ")"}
	dims := make([]FieldRef, 0, len(p.Query.By))
	dimNames, dimExprs := []string{}, []string{}
	columns := []Column{}
	if bucketed {
		columns = append(columns, Column{Name: "time", Type: "time", Role: "time"})
	}
	for i, by := range p.Query.By {
		d, err := sig.field(by)
		if err != nil {
			return Compiled{}, err
		}
		dims = append(dims, d)
		dimNames = append(dimNames, fmt.Sprintf("_d%d", i))
		dimExprs = append(dimExprs, "coalesce("+d.stringSQL()+",'')")
		columns = append(columns, Column{Name: d.alias(), Type: "string", Role: "dimension"})
	}
	top, fold := 0, -1
	if bucketed && len(dims) == 1 && p.Viz != "state_timeline" && p.Viz != "table" {
		top, fold = p.Top(), 0
	} else if p.Viz == "bar" && len(dims) == 2 {
		top, fold = p.Top(), 1
	}

	// Name grouping is the metric-name safety boundary. Top-N may select
	// names, but an Other bucket must never combine different metrics.
	selectNames := top > 0 && dims[fold].Column == "name" && dims[fold].Key == ""

	// Share one builder for each source and kind set. A sum's carried
	// gauge/updown branch shares avg work; its counter branch
	// shares the increase builder with rate, without lagging across kinds.
	builders := map[string]string{}
	bucketSources := []string{}
	needRate := slices.ContainsFunc(measures, func(m Measure) bool { return m.Func == "rate" })
	builder := func(source string, kinds []string) (string, error) {
		key := source + ":" + strings.Join(kinds, ",")
		if name, ok := builders[key]; ok {
			return name, nil
		}
		name := fmt.Sprintf("metric_builder_%d", len(builders))
		var cte string
		var builderArgs []any
		var err error
		switch source {
		case "metric_increases":
			cte, builderArgs, err = metricSeriesCTE(filters, scope, kinds)
			spread, spreadArgs := metricSpreadCTE(scope)
			cte += spread
			builderArgs = append(builderArgs, spreadArgs...)
			if needRate {
				cte += metricRateCarryCTE(scope)
				source = "metric_rate_rows"
			} else {
				cte += ", metric_spread_rows AS (SELECT *,false AS _carry FROM metric_spread)"
				source = "metric_spread_rows"
			}
		case "metric_gauge_rows":
			cte, builderArgs, err = metricGaugeCTE(filters, scope, kinds)
		case "metric_last_points":
			cte, builderArgs, err = metricLastCTE(filters, scope, kinds)
		default:
			cte, builderArgs, err = metricPointsCTE(filters, scope, kinds, 0)
			source = "metric_points"
		}
		if err != nil {
			return "", err
		}
		ctes = append(ctes, name+" AS ("+cte+" SELECT * FROM "+source+")")
		args = append(args, builderArgs...)
		builders[key] = name
		if source == "metric_last_points" || source == "metric_gauge_rows" || source == "metric_rate_rows" || source == "metric_spread_rows" {
			bucketSources = append(bucketSources, name)
		}
		return name, nil
	}
	sources, per, across := make([]string, len(measures)), make([]string, len(measures)), make([]string, len(measures))
	histograms := make([]string, len(measures))
	for i, m := range measures {
		source, perSeries, acrossSeries := metricMeasurePlan(m)
		prefix := fmt.Sprintf("metric_m%d_", i)
		if metricHistogramMeasure(m) {
			histograms[i], err = builder("metric_increases", []string{"histogram"})
			if err != nil {
				return Compiled{}, err
			}
		}
		if source == "metric_value_rows" {
			gaugeRows, err := builder("metric_gauge_rows", []string{"gauge", "updown"})
			if err != nil {
				return Compiled{}, err
			}
			counterRows, err := builder("metric_increases", []string{"counter"})
			if err != nil {
				return Compiled{}, err
			}
			// Retain dimension fields; grouping still uses the author's by.
			fields := "* EXCLUDE (_bounds,_counts)"
			ctes = append(ctes, prefix+"rows AS (SELECT "+fields+",NULL::DOUBLE AS increase FROM "+gaugeRows+
				" UNION ALL BY NAME SELECT * FROM "+counterRows+")")
			source = prefix + "rows"
		} else if source != "metric_raw_window" {
			kinds := metricMeasureKinds(m)
			if metricHistogramMeasure(m) {
				kinds = slices.DeleteFunc(slices.Clone(kinds), func(kind string) bool { return kind == "histogram" })
			}
			source, err = builder(source, kinds)
			if err != nil {
				return Compiled{}, err
			}
		}
		if histograms[i] != "" && !metricHistogramQuantile(m) {
			combined := prefix + "combined"
			ctes = append(ctes, combined+" AS (SELECT * FROM "+source+" UNION ALL BY NAME SELECT * FROM "+histograms[i]+")")
			source = combined
		}

		projection := "NULL::DOUBLE"
		if m.Field != nil && m.Func != "count_distinct" {
			n := m.Field.numberSQL()
			projection = "CASE WHEN isfinite(" + n + ") THEN " + n + " END"
		}
		distinct := "NULL::VARCHAR"
		if m.Func == "count_distinct" {
			distinct = m.Field.stringSQL()
		}
		sources[i] = prefix + "source"
		visible := ""
		if m.Func != "rate" && metricHistogramMeasure(m) && !metricHistogramQuantile(m) {
			visible = " WHERE type IN ('gauge','updown') OR NOT coalesce(_carry,false)"
		}
		ctes = append(ctes, sources[i]+" AS (SELECT *,"+projection+" AS _value,"+distinct+" AS _distinct FROM "+source+visible+")")
		per[i], across[i] = perSeries, acrossSeries
		unit := m.Unit
		if unit == "none" {
			unit = ""
		}
		if p.Viz == "scatter" && i == 0 && p.XUnit != "" {
			unit = p.XUnit
		}
		if (p.Viz == "scatter" || p.Viz == "state_timeline") && p.Unit != "" && (p.Viz != "scatter" || i == 1) {
			unit = p.Unit
		}
		columns = append(columns, Column{Name: m.Alias, Type: "number", Role: "measure", Unit: unit})
	}
	// Both candidate ranking and displayed buckets use this same first stage.
	seriesStage := func(i int, name, bucketExpr string, names, exprs []string) {
		selects := []string{bucketExpr + " AS _bucket", metricSeriesKey, "type"}
		groups := []string{"1", metricSeriesKey, "type"}
		for j, d := range names {
			selects = append(selects, exprs[j]+" AS "+d)
			groups = append(groups, d)
		}
		selects = append(selects, per[i]+" AS _v")
		m := measures[i]
		sample := "_value"
		if metricValueMeasure(m) {
			kinds := []string{"gauge", "updown"}
			if m.Func == "min" || m.Func == "max" || m.Func == "last" || m.Func == "sum" {
				kinds = append(kinds, "counter")
			}
			qs := []string{}
			for _, k := range kinds {
				qs = append(qs, sqlString(k))
			}
			sample = "CASE WHEN type IN (" + strings.Join(qs, ",") + ") THEN _value END"
		}
		if metricHistogramMeasure(m) && !metricHistogramQuantile(m) {
			selects = append(selects, "CASE WHEN type='histogram' THEN sum(count_increase) ELSE count("+sample+") FILTER (WHERE NOT coalesce(_carry,false)) END AS _n")
		} else {
			selects = append(selects, "count("+sample+") AS _n")
		}
		if m.Func == "avg" && metricValueMeasure(m) {
			selects = append(selects, "CASE WHEN type='histogram' THEN sum(count_increase) ELSE 1 END AS _weight")
		}
		if m.Func == "last" && !metricValueMeasure(m) {
			selects = append(selects, "max((time,ingested_unix_nano)) FILTER (WHERE _value IS NOT NULL) AS _last_order")
		}
		where := ""
		if name == "metric_rank_series" && bucketed && m.Func == "last" && metricValueMeasure(m) {
			where = fmt.Sprintf(" WHERE _bucket=%d", alignedBucketMillis(scope.End.Add(-time.Nanosecond), scope.Interval))
		}
		if name == "metric_rank_series" && bucketed && (m.Func == "rate" || (metricValueMeasure(m) && (m.Func == "sum" || m.Func == "avg"))) {
			// Ranking is one whole-window bucket: carried bucket samples must
			// not reweight a series with real window coverage or gauge points.
			covered := "NOT coalesce(_carry,false)"
			if m.Func == "rate" {
				covered += " AND _rate_covered>0"
			}
			where = " WHERE NOT coalesce(_carry,false) OR series_id NOT IN (SELECT series_id FROM " + sources[i] + " WHERE " + covered + ")"
		}
		ctes = append(ctes, name+" AS (SELECT "+strings.Join(selects, ",")+" FROM "+sources[i]+where+" GROUP BY "+strings.Join(groups, ",")+")")
	}
	// Quantiles run from weights at both ranking and display scope. Display
	// dimensions are folded before interpolation so Other gets a new quantile.
	groupStage := func(i int, series, name, bucketExpr string, names, exprs []string, partition string) {
		keys := append([]string{"_bucket"}, names...)
		expression := strings.ReplaceAll(across[i], "_partition", partition)
		scalar := "SELECT " + strings.Join(keys, ",") + "," + expression + " AS _v,sum(_n) AS _n FROM " + series + " GROUP BY " + strings.Join(keys, ",")
		if !metricHistogramQuantile(measures[i]) {
			ctes = append(ctes, name+" AS ("+scalar+")")
			return
		}
		weights, answers, samples := name+"_weights", name+"_answers", name+"_samples"
		selects := []string{bucketExpr + " AS _bucket"}
		for j, d := range names {
			selects = append(selects, exprs[j]+" AS "+d)
		}
		// Zipped select-list unnests avoid the lateral join that WITH
		// ORDINALITY plans, and one pre-aggregation feeds both consumers.
		selects = append(selects, "unnest(list_append(_bounds,NULL::DOUBLE)) AS hi", "unnest(bound_increases) AS n")
		grouped := strings.Join(keys, ",") + ",hi"
		ctes = append(ctes,
			weights+" AS (SELECT "+grouped+",sum(n) AS n FROM (SELECT "+strings.Join(selects, ",")+" FROM "+histograms[i]+" WHERE NOT _carry) WHERE n>=0 GROUP BY "+grouped+")",
			answers+" AS ("+histogramQuantileSQL(weights, keys, measures[i].Q)+")",
			samples+" AS (SELECT "+strings.Join(keys, ",")+",sum(n)::DOUBLE AS _n FROM "+weights+" GROUP BY "+strings.Join(keys, ",")+")",
			name+"_scalar AS ("+scalar+")")
		selects, join := []string{}, []string{}
		for _, key := range keys {
			selects = append(selects, "coalesce(s."+key+",h."+key+") AS "+key)
			join = append(join, "s."+key+" IS NOT DISTINCT FROM h."+key)
		}
		selects = append(selects, "coalesce(h.quantile,s._v) AS _v", "coalesce(s._n,0)+coalesce(h._n,0) AS _n")
		ctes = append(ctes, name+" AS (SELECT "+strings.Join(selects, ",")+" FROM "+name+"_scalar s FULL OUTER JOIN (SELECT * FROM "+samples+" LEFT JOIN "+answers+" USING ("+strings.Join(keys, ",")+")) h ON "+strings.Join(join, " AND ")+")")
	}
	if top > 0 {
		seriesStage(0, "metric_rank_series", "0::BIGINT", []string{"d"}, []string{dimExprs[fold]})
		first := measures[0]
		direction := "DESC"
		rankPanel, rankQuery := *p, *p.Query
		rankQuery.Measures = []string{first.Text}
		rankPanel.Query = &rankQuery
		better := p.Better
		if better == "" {
			better = inferBetter(&rankPanel)
		}
		if !first.Additive && first.Func != "share" && better == "higher" {
			direction = "ASC"
		}
		samples := "coalesce(v.n,0)"
		order := "value " + direction + " NULLS LAST,n DESC,d"
		switch first.Func {
		case "p50", "p75", "p90", "p95", "p99", "quantile", "avg", "max", "min":
			order = "(n>=20) DESC,CASE WHEN n>=20 THEN value END " + direction + " NULLS LAST,n DESC,d"
		default:
			samples = "r.n"
		}
		groupStage(0, "metric_rank_series", "metric_rank_groups", "0::BIGINT", []string{"d"}, []string{dimExprs[fold]}, "")
		rankSource := "metric_raw_window"
		if first.Func == "last" && metricValueMeasure(first) {
			rankSource = sources[0]
		}
		if samples == "r.n" {
			samples = "coalesce(r.n,0)"
		}
		ctes = append(ctes,
			"metric_rank_values AS (SELECT d,_v AS value,_n AS n FROM metric_rank_groups)",
			"metric_rank_keys AS (SELECT "+dimExprs[fold]+" AS d,count(*) AS n FROM "+rankSource+" GROUP BY 1)",
			"candidates AS (SELECT coalesce(r.d,v.d) AS d,v.value,"+samples+" AS n FROM metric_rank_keys r FULL OUTER JOIN metric_rank_values v ON r.d IS NOT DISTINCT FROM v.d)",
			fmt.Sprintf("top AS (SELECT d FROM candidates ORDER BY %s LIMIT %d)", order, top))

	}
	foldExpr := func(expr string) string {
		return "CASE WHEN " + expr + " IN (SELECT d FROM top) THEN " + expr + " ELSE (SELECT 'Other ('||count(*)::VARCHAR||')' FROM candidates WHERE d NOT IN (SELECT d FROM top)) END"
	}
	keySelect := []string{"_bucket"}
	for i, d := range dimNames {
		expr := dimExprs[i]
		if i == fold && !selectNames {
			expr = foldExpr(expr)
		}
		keySelect = append(keySelect, expr+" AS "+d)
	}
	if !bucketed && len(dims) == 0 {
		ctes = append(ctes, "metric_groups AS (SELECT 0::BIGINT AS _bucket)")
	} else {
		groupSource := "SELECT " + strings.Join(keySelect, ",") + " FROM metric_raw_window"
		for _, source := range bucketSources {
			groupSource += " UNION ALL SELECT " + strings.Join(keySelect, ",") + " FROM " + source
		}
		ctes = append(ctes, "metric_groups AS (SELECT DISTINCT * FROM ("+groupSource+"))")
	}
	for i := range measures {
		name := fmt.Sprintf("metric_series_%d", i)
		seriesStage(i, name, "_bucket", dimNames, dimExprs)
		if top > 0 && !selectNames {
			selects := []string{"* EXCLUDE (" + dimNames[fold] + ")", foldExpr(dimNames[fold]) + " AS " + dimNames[fold]}
			folded := name + "_folded"
			ctes = append(ctes, folded+" AS (SELECT "+strings.Join(selects, ",")+" FROM "+name+")")
			name = folded
		}
		partition := ""
		if bucketed {
			partition = "PARTITION BY _bucket"
		}
		exprs := append([]string{}, dimExprs...)
		if top > 0 && !selectNames {
			exprs[fold] = foldExpr(exprs[fold])
		}
		groupStage(i, name, fmt.Sprintf("metric_group_%d", i), "_bucket", dimNames, exprs, partition)
	}
	selects, joins := []string{}, []string{}
	if bucketed {
		selects = append(selects, "g._bucket AS _t")
	}
	for i, d := range dims {
		selects = append(selects, "g."+dimNames[i]+" AS "+quoteIdent(d.alias()))
	}
	for i, m := range measures {
		alias := fmt.Sprintf("m%d", i)
		on := []string{alias + "._bucket IS NOT DISTINCT FROM g._bucket"}
		for _, d := range dimNames {
			on = append(on, alias+"."+d+" IS NOT DISTINCT FROM g."+d)
		}
		joins = append(joins, fmt.Sprintf(" LEFT JOIN metric_group_%d %s ON %s", i, alias, strings.Join(on, " AND ")))
		expr := alias + "._v"
		if m.Func == "count" || m.Func == "count_distinct" {
			expr = "coalesce(" + expr + ",0)::DOUBLE"
		}
		selects = append(selects, expr+" AS "+quoteIdent(m.Alias))
	}
	if p.Viz == "state_timeline" {
		ctes = append(ctes, fmt.Sprintf("metric_items AS (SELECT %s AS item FROM metric_raw_window GROUP BY 1 ORDER BY count(*) DESC,1 LIMIT %d)", dimExprs[0], p.Top()))
	}
	text := "WITH " + strings.Join(ctes, ",\n") + " SELECT " + strings.Join(selects, ",") + " FROM metric_groups g" + strings.Join(joins, "")
	if p.Viz == "state_timeline" {
		// Shares were computed across the whole scope before item selection.
		text += " WHERE g._d0 IN (SELECT item FROM metric_items)"
	} else if selectNames {
		text += " WHERE g." + dimNames[fold] + " IN (SELECT d FROM top)"
	}

	if p.Viz == "scatter" || p.Viz == "state_timeline" {
		n := distributionRows(p, len(columns))
		if p.Viz == "state_timeline" {
			text += fmt.Sprintf(" ORDER BY _t DESC,2 LIMIT %d", n+1)
		} else {
			n = min(n, 1000)
			alias, direction := measures[0].Alias, "DESC"
			if p.Query.Sort != "" {
				alias = strings.TrimPrefix(p.Query.Sort, "+")
				if strings.HasPrefix(p.Query.Sort, "+") {
					direction = "ASC"
				}
			}
			text += fmt.Sprintf(" ORDER BY %s %s NULLS LAST,1 LIMIT %d", quoteIdent(alias), direction, n+1)
		}
	} else {
		text += orderAndLimit(p, measures, bucketed, len(dims))
	}
	return Compiled{SQL: text, Args: args, Columns: columns}, nil
}
