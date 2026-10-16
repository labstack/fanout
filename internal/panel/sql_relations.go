package panel

// SQLRelationSchema describes a panel-SQL-only relation. Its rows are not raw
// events, so it deliberately has neither a signal time column nor sample counts.
type SQLRelationSchema struct {
	Description string            `json:"description"`
	Columns     []SQLColumnSchema `json:"columns"`
}

type SQLColumnSchema struct {
	Field
	SQLType string `json:"sql_type"`
}

// SQLRelations is the shared catalog for discovery and generated documentation.
func SQLRelations() map[string]SQLRelationSchema {
	return map[string]SQLRelationSchema{
		"resource_inventory": {
			Description: "Distinct full resource identities per signal, namespace and service observed inside the logical panel window [start,end). Panel SQL only; unavailable to structured query.from, variables and unscoped SQL. count(*) counts resource identities per signal, not events or instances. Resource extrema describe actual observations inside the selected window, not a historical registry.",
			Columns: []SQLColumnSchema{
				{Field: str("signal", "spans, logs or metrics; select the required signal"), SQLType: "VARCHAR"},
				{Field: str("namespace", "Original resource namespace, preserving NULL and empty values"), SQLType: "VARCHAR"},
				{Field: str("service", "Original service name, preserving NULL and empty values"), SQLType: "VARCHAR"},
				{Field: dict("resource", "Complete typed resource; use resource['key'], with dotted keys written literally"), SQLType: "VARIANT"},
				{Field: ts("first_seen", "Earliest actual event observation inside the selected panel window"), SQLType: "TIMESTAMPTZ_NS"},
				{Field: ts("last_seen", "Latest actual event observation inside the selected panel window"), SQLType: "TIMESTAMPTZ_NS"},
			},
		},
	}
}
