package query

import "strings"

// histogramQuantileSQL combines exported bucket weights on the union of their
// upper bounds. Coarse buckets keep their observations at the exported bound.
func histogramQuantileSQL(source string, groupColumns []string, q float64) string {
	keys := strings.Join(groupColumns, ",")
	prefix, group, partition, resultGroup := "", "", "", ""
	if keys != "" {
		prefix, group = keys+",", keys+","
		partition = "PARTITION BY " + keys + " "
		resultGroup = " GROUP BY " + keys
	}
	return `WITH weights AS (SELECT ` + prefix + `hi,sum(n)::DOUBLE AS n FROM ` + source + ` GROUP BY ` + group + `hi),
      ranks AS (SELECT *,sum(n) OVER (` + partition + `ORDER BY hi NULLS LAST ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS total,
        sum(n) OVER (` + partition + `ORDER BY hi NULLS LAST ROWS UNBOUNDED PRECEDING) AS cumulative,
        lag(hi) OVER (` + partition + `ORDER BY hi NULLS LAST) AS previous_bound,
        max(hi) OVER (` + partition + `) AS highest_finite FROM weights),
      answers AS (SELECT *,CASE
        WHEN total<=0 THEN NULL
        WHEN hi IS NULL THEN highest_finite
        WHEN previous_bound IS NULL AND hi<=0 THEN hi
        ELSE coalesce(previous_bound,0.0)+
          (` + sqlFloat(q) + `*total-(cumulative-n))/nullif(n,0)*(hi-coalesce(previous_bound,0.0))
        END::DOUBLE AS quantile
        FROM ranks WHERE cumulative>=` + sqlFloat(q) + `*total AND n>0)
      SELECT ` + prefix + `arg_min(quantile,cumulative)::DOUBLE AS quantile FROM answers` + resultGroup
}

func metricHistogramMeasure(m Measure) bool {
	return m.Func == "rate" || (metricValueMeasure(m) && (m.Func == "sum" || m.Func == "avg" || metricHistogramQuantile(m)))
}

func metricHistogramQuantile(m Measure) bool {
	return metricValueMeasure(m) && m.Q > 0
}
