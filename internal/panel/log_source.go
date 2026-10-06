package panel

import "github.com/labstack/fanout/internal/observability"

func redactedLogSource() string {
	// Qualifying the physical relation also prevents an author's logs CTE from
	// capturing this trusted source when embedded in a SQL-panel subquery.
	return `SELECT * REPLACE (` + observability.RedactLogBodySQL("body") + ` AS body,` + observability.RedactLogBodySQL("body_template") + ` AS body_template) FROM main.logs`
}

func structuredSource(signal string) string {
	if signal == "logs" {
		return "(" + redactedLogSource() + ")"
	}
	return signal
}
