package query

func receiverRelations() map[string]SQLRelationSchema {
	common := []SQLColumnSchema{
		{Field: str("namespace", "Service namespace"), SQLType: "VARCHAR"},
		{Field: ts("bucket", "UTC minute bucket; filter with $__window(bucket)"), SQLType: "TIMESTAMP_NS"},
		{Field: str("service", "Service name"), SQLType: "VARCHAR"},
	}
	metric := append(append([]SQLColumnSchema(nil), common...),
		SQLColumnSchema{Field: str("series_id", "Identity of the fixed receiver dimensions, independent of other OTLP labels"), SQLType: "VARCHAR"})
	for _, name := range []string{"name", "type", "temporality", "unit", "scope_name"} {
		metric = append(metric, SQLColumnSchema{Field: str(name, "Metric "+name), SQLType: "VARCHAR"})
	}
	metric = append(metric, SQLColumnSchema{Field: dict("resource", "host.name and container.id/name only"), SQLType: "VARIANT"}, SQLColumnSchema{Field: dict("attributes", "state, cpu, mountpoint, device, http.url/method/status_class/status_code only"), SQLType: "VARIANT"})
	for _, name := range []string{"host", "container_id", "container_name", "state", "cpu", "mountpoint", "device", "url", "method", "class", "code"} {
		metric = append(metric, SQLColumnSchema{Field: str(name, "Projected receiver dimension"), SQLType: "VARCHAR"})
	}
	points := append([]SQLColumnSchema(nil), metric...)
	points = append(points, SQLColumnSchema{Field: ts("time", "Actual witness event time"), SQLType: "TIMESTAMPTZ_NS"}, SQLColumnSchema{Field: num("value", "", "Metric value"), SQLType: "DOUBLE"}, SQLColumnSchema{Field: ts("start_time", "Counter interval start"), SQLType: "TIMESTAMPTZ_NS"}, SQLColumnSchema{Field: num("ingested_unix_nano", "", "Retry ordering"), SQLType: "BIGINT"})
	for _, name := range []string{"first_point", "last_point", "previous_point", "first_finite_point", "last_finite_point", "previous_finite_point"} {
		metric = append(metric, SQLColumnSchema{Field: dict(name, "Time, value, start_time and ingested_unix_nano of the retry-deduplicated witness"), SQLType: "STRUCT(time TIMESTAMPTZ_NS, value DOUBLE, start_time TIMESTAMPTZ_NS, ingested_unix_nano BIGINT)"})
	}
	metric = append(metric, SQLColumnSchema{Field: num("counter_increase", "", "Reset-aware increase within the minute; first point bridges the prior minute"), SQLType: "DOUBLE"}, SQLColumnSchema{Field: num("counter_seconds", "s", "Covered counter intervals within the minute"), SQLType: "DOUBLE"})
	resources := append(append([]SQLColumnSchema(nil), common...), SQLColumnSchema{Field: str("signal", "spans, logs or metrics"), SQLType: "VARCHAR"}, SQLColumnSchema{Field: dict("resource", "host.name, service.version, process.runtime.name/version, telemetry.sdk.language/version only"), SQLType: "VARIANT"}, SQLColumnSchema{Field: ts("first_seen", "First actual observation in the minute"), SQLType: "TIMESTAMPTZ_NS"}, SQLColumnSchema{Field: ts("last_seen", "Last actual observation in the minute"), SQLType: "TIMESTAMPTZ_NS"})
	entities := append([]SQLColumnSchema(nil), common...)
	for _, name := range []string{"receiver", "family", "entity", "host", "container_id", "container_name", "endpoint_from"} {
		entities = append(entities, SQLColumnSchema{Field: str(name, "Fixed receiver entity/family dimension"), SQLType: "VARCHAR"})
	}
	for _, name := range []string{"first_observation", "previous_observation", "last_observation"} {
		entities = append(entities, SQLColumnSchema{Field: dict(name, "First or last two distinct reporting times in this minute; check flag 2 is unsupported metadata, 1 success, 0 failure"), SQLType: "STRUCT(time TIMESTAMPTZ_NS, flag INTEGER)"})
	}

	description := "Analytical minute summaries share the service rollup affected-key, late-arrival and retention lifecycle. fresh_to_ms reports the ingest horizon. No raw boundary reads. Window boundaries have minute precision; counter overlap within a boundary minute is proportional to covered time."
	return map[string]SQLRelationSchema{
		"receiver_entity_rollup": {Description: "Per-minute reporting witnesses collapsed across CPUs, states, mounts and HTTP status classes. Readers bridge adjacent minutes for cadence; only the affected minute needs invalidation. " + description, Columns: entities},
		"receiver_rollup":        {Description: "Fixed-dimension summaries for httpcheck, hostmetrics and docker_stats. Retains first and last two witnesses (all and finite) per minute plus reset-aware counter totals. " + description, Columns: metric},
		"receiver_points":        {Description: "First and last two distinct finite witnesses per receiver series-minute; httpcheck.status preserves nonfinite observations for metadata diagnosis. Raw cadence witnesses remain in receiver_rollup. Intermediate scrapes are summarized; these are not raw metric points. " + description, Columns: points},
		"resource_rollup":        {Description: "Resource version/SDK/runtime and host tuples at minute precision. Filter bucket with $__window(bucket). Exact inventory remains available through resource_inventory. " + description, Columns: resources},
	}
}
