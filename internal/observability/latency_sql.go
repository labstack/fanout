package observability

// A service's latency is read from the buckets in which it actually served
// something. service_rollup stores percentiles that already exclude the calls a
// service was waiting on, but a bucket holding nothing except such calls has no
// served latency to report and falls back to them — a subscription held open
// for ten minutes would otherwise decide the service's whole window.
//
// Preferring served buckets keeps that bucket out of the answer while leaving a
// service that served nothing all window — a load generator, a cron worker —
// measured on what it does have instead of reporting a flat zero.
const (
	// windowP95SQL aggregates p95 across buckets. MAX, not an average: the
	// worst bucket is the one worth knowing about.
	windowP95SQL = `COALESCE(MAX(p95_ms) FILTER (WHERE served_spans > 0), MAX(p95_ms), 0)`

	// windowP50SQL weights each bucket by the spans it holds.
	windowP50SQL = `COALESCE(
    SUM(p50_ms * spans) FILTER (WHERE served_spans > 0) / NULLIF(SUM(spans) FILTER (WHERE served_spans > 0), 0),
    SUM(p50_ms * spans) / NULLIF(SUM(spans), 0),
    0)`
)
