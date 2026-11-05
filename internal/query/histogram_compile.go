package query

import (
	"fmt"
	"strings"
)

func histogramMeasure(m Measure) bool {
	switch m.Func {
	case "p50", "p75", "p90", "p95", "p99", "quantile":
		return true
	}
	return false
}

// Expand bins before aggregation. Correlating an aggregate list of maps through
// UNNEST makes DuckDB repeatedly process nested aggregate state. One grouped bin
// stream shares its cumulative counts across all requested quantiles instead.
func histogramAggregate(b *strings.Builder, prefix, from string, selects, aliases, groups []string, totals bool, quantiles map[int]Measure) string {
	keys := []string{"__hist_group"}
	hidden := []string{"0 AS __hist_group"}
	if totals {
		hidden[0] = "grouping(" + groups[0] + ") AS __hist_group"
	}
	for i, g := range groups {
		key := fmt.Sprintf("__hist_key_%d", i)
		keys = append(keys, key)
		hidden = append(hidden, g+" AS "+key)
	}
	grouping := func(bin bool) string {
		all := append([]string(nil), groups...)
		if bin {
			all = append(all, "e.key")
		}
		if totals {
			rest := append([]string(nil), groups[1:]...)
			if bin {
				rest = append(rest, "e.key")
			}
			return " GROUP BY GROUPING SETS ((" + strings.Join(all, ",") + "),(" + strings.Join(rest, ",") + "))"
		}
		if len(all) > 0 {
			return " GROUP BY " + strings.Join(all, ",")
		}
		return ""
	}
	aggregate := append(append([]string(nil), selects...), hidden...)
	aggregate = append(aggregate, "count(*) AS __hist_rows")
	fmt.Fprintf(b, ", %s_aggregate AS (SELECT %s FROM %s%s)", prefix, strings.Join(aggregate, ","), from, grouping(false))
	fmt.Fprintf(b, ", %s_bins AS (SELECT %s,e.key AS __hist_k,sum(e.value)::DOUBLE AS __hist_n FROM %s,unnest(map_entries(hist)) entries(e)%s)", prefix, strings.Join(hidden, ","), from, grouping(true))
	partition := strings.Join(keys, ",")
	fmt.Fprintf(b, ", %s_cdf AS (SELECT *,sum(__hist_n) OVER (PARTITION BY %s ORDER BY __hist_k) AS __hist_cum,sum(__hist_n) OVER (PARTITION BY %s) AS __hist_total FROM %s_bins)", prefix, partition, partition, prefix)
	values := append([]string(nil), keys...)
	for i := range aliases {
		if m, ok := quantiles[i]; ok {
			q := sqlFloat(m.Q)
			value := `CASE WHEN __hist_k=-2147483648 THEN 0.0 ELSE pow(2.0,(__hist_k-1)/8.0)+(pow(2.0,__hist_k/8.0)-pow(2.0,(__hist_k-1)/8.0))*greatest(0.0,least(1.0,(` + q + `*__hist_total-(__hist_cum-__hist_n))/__hist_n)) END`
			values = append(values, "min(CASE WHEN __hist_cum >= "+q+"*__hist_total THEN "+value+" END) AS "+quoteIdent(aliases[i]))
		}
	}
	fmt.Fprintf(b, ", %s_quantiles AS (SELECT %s FROM %s_cdf GROUP BY %s)", prefix, strings.Join(values, ","), prefix, partition)
	outputs := make([]string, len(aliases))
	for i, a := range aliases {
		side := "a"
		if _, ok := quantiles[i]; ok {
			side = "q"
		}
		outputs[i] = side + "." + quoteIdent(a) + " AS " + quoteIdent(a)
	}
	joins := make([]string, len(keys))
	for i, k := range keys {
		joins[i] = "a." + k + " IS NOT DISTINCT FROM q." + k
	}
	fmt.Fprintf(b, ", %s AS (SELECT %s FROM %s_aggregate a LEFT JOIN %s_quantiles q ON %s)", prefix, strings.Join(outputs, ","), prefix, prefix, strings.Join(joins, " AND "))
	return prefix
}
