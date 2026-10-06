// Package panel defines Fanout's dashboard spec: the typed, validated contract
// between the agent, the API, storage and the browser renderer. The Go types
// are the single source of the JSON Schema the MCP tools publish.
package panel

import "time"

// SpecVersion is the only dashboard spec version Fanout reads.
const SpecVersion = 1

// AllValue selects All for a variable that offers it.
const AllValue = "$__all"

type Dashboard struct {
	Version     int          `json:"version,omitempty" jsonschema:"Spec version; omit or 1"`
	Name        string       `json:"name" jsonschema:"Short unique dashboard name, at most 80 characters"`
	Description string       `json:"description,omitempty" jsonschema:"What the dashboard is for, at most 280 characters"`
	Time        Time         `json:"time,omitzero" jsonschema:"Default time range, refresh and comparison"`
	Variables   []Variable   `json:"variables,omitempty" jsonschema:"Variables referenced as $name in filters, titles and SQL"`
	Annotations *Annotations `json:"annotations,omitempty" jsonschema:"Deploy and anomaly markers on time panels"`
	Panels      []Panel      `json:"panels" jsonschema:"Panels in display order"`
}

type Time struct {
	Range   string     `json:"range,omitempty" jsonschema:"Relative range: 5m, 15m, 1h, 3h, 6h, 12h, 24h, 2d, 7d or 30d"`
	From    *time.Time `json:"from,omitempty" jsonschema:"Absolute start (RFC 3339); use with to instead of range"`
	To      *time.Time `json:"to,omitempty" jsonschema:"Absolute end (RFC 3339)"`
	Refresh string     `json:"refresh,omitempty" jsonschema:"off, 10s, 30s, 1m or 5m; default 30s"`
	Compare string     `json:"compare,omitempty" jsonschema:"previous_period to compare with the preceding range"`
}

type Annotations struct {
	Deploys   *bool `json:"deploys,omitempty" jsonschema:"Draw deploys; default true"`
	Anomalies *bool `json:"anomalies,omitempty" jsonschema:"Draw detector anomalies; default true"`
}

type Variable struct {
	Name       string   `json:"name" jsonschema:"Identifier referenced as $name: lowercase letters, digits, underscores"`
	Kind       string   `json:"kind" jsonschema:"query, custom, constant or text"`
	From       string   `json:"from,omitempty" jsonschema:"query variables: spans, logs or metrics"`
	Field      string   `json:"field,omitempty" jsonschema:"query variables: a column or attributes['key'] whose distinct values are the options"`
	Where      []string `json:"where,omitempty" jsonschema:"query variables: filter expressions; may reference earlier variables"`
	Options    []string `json:"options,omitempty" jsonschema:"custom variables: the fixed options"`
	Value      string   `json:"value,omitempty" jsonschema:"constant variables: the value"`
	Default    string   `json:"default,omitempty" jsonschema:"Initial value; $__all selects All when include_all is true"`
	Multi      bool     `json:"multi,omitempty" jsonschema:"Allow several values; use as IN $name"`
	IncludeAll bool     `json:"include_all,omitempty" jsonschema:"Offer All, which removes filters that reference this variable"`
}

type Panel struct {
	ID          string      `json:"id" jsonschema:"Stable identifier: lowercase letters, digits and underscores; edits address panels by id"`
	Title       string      `json:"title" jsonschema:"Panel title, at most 80 characters; may reference $variables"`
	Description string      `json:"description,omitempty" jsonschema:"Help text, at most 280 characters"`
	Viz         string      `json:"viz" jsonschema:"stat, gauge, timeseries, bar, table, text, heatmap, histogram, scatter, state_timeline, logs, log_patterns or traces"`
	Width       int         `json:"width,omitempty" jsonschema:"Grid columns from 1 to 12; default depends on viz"`
	Height      string      `json:"height,omitempty" jsonschema:"s, m or l; default depends on viz"`
	Query       *Query      `json:"query,omitempty" jsonschema:"Structured query; exactly one of query or sql, except text panels"`
	SQL         string      `json:"sql,omitempty" jsonschema:"One read-only SELECT over spans, logs, metrics, service_rollup or edge_rollup; must use $__window(time_column)"`
	Unit        string      `json:"unit,omitempty" jsonschema:"ms, s, ns, percent, ratio, count, per_second, per_minute, bytes or none; inferred when omitted"`
	XUnit       string      `json:"x_unit,omitempty" jsonschema:"scatter x axis unit; unit describes y"`
	Reduce      string      `json:"reduce,omitempty" jsonschema:"stat and gauge: window (default), last, mean, min, max or sum"`
	Thresholds  []Threshold `json:"thresholds,omitempty" jsonschema:"Up to 4 status boundaries"`
	Better      string      `json:"better,omitempty" jsonschema:"lower or higher; inferred for known measures"`
	Min         *float64    `json:"min,omitempty" jsonschema:"gauge: scale minimum"`
	Max         *float64    `json:"max,omitempty" jsonschema:"gauge: scale maximum"`
	Options     *Options    `json:"options,omitempty"`
	Click       *Click      `json:"click,omitempty" jsonschema:"Clicking a bar, row or series sets a variable"`
	Drill       string      `json:"drill,omitempty" jsonschema:"traces or logs: what a click on a point opens"`
	Time        *PanelTime  `json:"time,omitempty" jsonschema:"Override the dashboard time for this panel"`
	Content     string      `json:"content,omitempty" jsonschema:"text panels: Markdown, at most 4000 characters"`
	Grid        *Grid       `json:"grid,omitempty" jsonschema:"Position set by the server or by dragging; omit when authoring"`
}

type Query struct {
	From      string     `json:"from" jsonschema:"spans, logs or metrics"`
	Where     []string   `json:"where,omitempty" jsonschema:"Filter expressions joined by AND, e.g. service = $service or attributes['http.route'] = '/cart'"`
	Measures  []string   `json:"measures,omitempty" jsonschema:"Fixed logs and traces take no measures; other queries take 1 to 6 of fn(field) [as alias]: count(), rate(), error_rate(), share(), avg, min, max, sum, last, p50, p75, p90, p95, p99, quantile(field, q), count_distinct(field)"`
	By        []string   `json:"by,omitempty" jsonschema:"Up to 3 grouping fields: columns or attributes['key']"`
	Bucket    string     `json:"bucket,omitempty" jsonschema:"auto or 10s, 30s, 1m, 5m, 10m, 15m, 30m, 1h, 3h, 6h, 12h, 1d; required for timeseries"`
	Histogram *Histogram `json:"histogram,omitempty" jsonschema:"heatmap and histogram panels (milestone 2)"`
	Sort      string     `json:"sort,omitempty" jsonschema:"Measure alias descending (+ ascending); logs: time or +time; traces: duration_ms, errors or +start; log_patterns: count"`
	Limit     int        `json:"limit,omitempty" jsonschema:"Row limit, at most 1000; logs and traces default 1000; log_patterns default 20, capped at 50"`
}

type Histogram struct {
	Temporality string `json:"temporality,omitempty" jsonschema:"metric histograms: cumulative (also unknown/default) or delta; cumulative uses last minus first per bound, clamped at zero; a series whose counter resets mid-window contributes 0 for that window"`
	Field       string `json:"field" jsonschema:"spans duration_ms with log2 buckets or metrics value with explicit buckets; negative span durations clamp to [0,1), +Inf goes to overflow, NaN is excluded because it is not a measurement"`
	Buckets     string `json:"buckets,omitempty"`
}

type Threshold struct {
	Value  float64 `json:"value"`
	Status string  `json:"status" jsonschema:"ok, warn or bad"`
	Label  string  `json:"label,omitempty"`
}

type Options struct {
	Highlight string `json:"highlight,omitempty" jsonschema:"logs and log_patterns: literal redacted-body search, at most 200 characters"`
	Style     string `json:"style,omitempty" jsonschema:"timeseries: line, area, bars or stacked (additive measures only)"`
	Scale     string `json:"scale,omitempty" jsonschema:"linear or log"`
	Top       int    `json:"top,omitempty" jsonschema:"Series limit; the rest become Other; default 8"`
	Legend    string `json:"legend,omitempty" jsonschema:"auto or hidden"`
	XScale    string `json:"x_scale,omitempty" jsonschema:"scatter x axis: linear or log"`
	YScale    string `json:"y_scale,omitempty" jsonschema:"scatter y axis: linear or log"`
}

type Click struct {
	SetVariable string `json:"set_variable" jsonschema:"Variable set to the clicked value"`
}

type PanelTime struct {
	Range string `json:"range,omitempty" jsonschema:"Replace the dashboard range, e.g. 30d"`
	Shift string `json:"shift,omitempty" jsonschema:"Move the dashboard range back, e.g. 1d"`
}

type Grid struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Top is the series limit for a grouped time series.
func (p *Panel) Top() int {
	if p.Options != nil && p.Options.Top > 0 {
		return min(p.Options.Top, 20)
	}
	return 8
}
