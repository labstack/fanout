package query

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

// ServiceGuidance returns an empty message for an exact observed service name.
// Retained names remain valid even when the requested window has no traffic.
// Rollups are the common, cheap path; inventory covers publication/rollup lag
// and services that emit only logs or metrics. Neither uses a capped name list.
func (e *Executor) ServiceGuidance(ctx context.Context, start, end time.Time, namespace, service string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	if !start.Before(end) || end.Sub(start) > e.maxWindow {
		return "", Problems{{Path: "time", Message: "window must be positive and within retention"}}
	}
	known, err := e.observedService(ctx, `SELECT EXISTS (
 SELECT 1 FROM service_rollup WHERE service=? AND (?='' OR namespace=?)
)`, service, namespace, namespace)
	if err != nil || known {
		return "", err
	}

	// Retention follows ingestion time. Membership includes all event times in
	// the pinned retained batches, including late and future-dated events.
	history := queryrows.Window{Start: start, End: end, Namespace: namespace, Service: service, RetainedInventory: true}
	historyCtx := queryrows.WithInventoryWindow(queryrows.WithWindow(ctx, history), history)
	known, err = e.observedService(historyCtx, `SELECT EXISTS (
 SELECT 1 FROM resource_inventory WHERE service=? AND (?='' OR namespace=?)
)`, service, namespace, namespace)
	if err != nil || known {
		return "", err
	}

	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end, Namespace: namespace})
	rows, err := e.engine.QueryContext(ctx, serviceGuidanceSQL, start, end, namespace, service)
	if err != nil {
		return "", fmt.Errorf("service guidance: %w", err)
	}
	defer rows.Close()
	var knownNames []string
	users := map[string][]string{}
	for rows.Next() {
		var kind, ns, name string
		var traffic int64
		if err := rows.Scan(&kind, &ns, &name, &traffic); err != nil {
			return "", err
		}
		label := name
		if namespace == "" && ns != "" {
			label += " (namespace " + ns + ")"
		}
		if kind == "service" {
			knownNames = append(knownNames, label)
		} else {
			users[kind] = append(users[kind], label)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	message := fmt.Sprintf("Unknown service %q", service)
	if namespace != "" {
		message += fmt.Sprintf(" in namespace %q", namespace)
	}
	message += "."
	for _, kind := range []string{"messaging system", "database system", "peer/callee"} {
		if names := users[kind]; len(names) > 0 {
			message += fmt.Sprintf(" %s is a %s used by %s.", service, kind, strings.Join(names, ", "))
		}
	}
	if len(knownNames) == 0 {
		message += " No known services have traffic in the requested window. Try a wider window or check the namespace."
	} else {
		message += " Known services by traffic in the requested window: " + strings.Join(knownNames, ", ") + ". Use an exact service name."
	}
	return message, nil
}

func (e *Executor) observedService(ctx context.Context, sql string, args ...any) (bool, error) {
	rows, err := e.engine.QueryContext(ctx, sql, args...)
	if err != nil {
		return false, fmt.Errorf("observe service: %w", err)
	}
	defer rows.Close()
	var known bool
	if rows.Next() {
		if err := rows.Scan(&known); err != nil {
			return false, err
		}
	}
	return known, rows.Err()
}

// Guidance is only queried after rejecting a name. Count actual event traffic
// rather than resource identities, which are not proportional to traffic.
// The read window pins/prunes Parquet, while predicates keep nanosecond bounds.
const serviceGuidanceSQL = `
WITH span_rows AS MATERIALIZED (
 SELECT namespace,service,messaging_system,db_system,peer_service FROM spans
 WHERE start_time >= $1::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time < $2::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND ($3='' OR namespace=$3)
), traffic AS (
 SELECT namespace,service,count(*) AS n FROM span_rows GROUP BY namespace,service
 UNION ALL
 SELECT namespace,service,count(*) FROM logs
 WHERE time >= $1::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time < $2::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND ($3='' OR namespace=$3) GROUP BY namespace,service
 UNION ALL
 SELECT namespace,service,count(*) FROM metrics
 WHERE time >= $1::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time < $2::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND ($3='' OR namespace=$3) GROUP BY namespace,service
), known AS (
 SELECT 'service' AS kind,namespace,service,sum(n)::BIGINT AS n FROM traffic
 WHERE service IS NOT NULL AND service<>'' GROUP BY namespace,service
 ORDER BY n DESC,namespace,service LIMIT 10
), aliases AS (
 SELECT 'messaging system' AS kind,namespace,service,count(*) AS n FROM span_rows
 WHERE messaging_system=$4 GROUP BY namespace,service
 UNION ALL
 SELECT 'database system',namespace,service,count(*) FROM span_rows
 WHERE db_system=$4 GROUP BY namespace,service
 UNION ALL
 SELECT 'peer/callee',namespace,service,count(*) FROM span_rows
 WHERE peer_service=$4 GROUP BY namespace,service
 UNION ALL
 SELECT 'peer/callee',namespace,caller,sum(calls)::BIGINT FROM edge_rollup
 WHERE bucket >= $1 AND bucket < $2 AND ($3='' OR namespace=$3) AND callee=$4 AND calls>0
 GROUP BY namespace,caller
), alias_users AS (
 SELECT kind,namespace,service,sum(n)::BIGINT AS n,
 row_number() OVER (PARTITION BY kind ORDER BY sum(n) DESC,namespace,service) AS rank
 FROM aliases GROUP BY kind,namespace,service
)
SELECT * FROM known
UNION ALL
SELECT kind,namespace,service,n FROM alias_users WHERE rank<=10
ORDER BY kind,n DESC,namespace,service`

// EmptyGuidance is used only after a complete fragment returned no matching
// data. Reuse checked filters and resolved variables to find exact service scopes.
func (e *Executor) EmptyGuidance(ctx context.Context, req RunRequest) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	d := req.Dashboard
	Normalize(&d)
	checked, err := e.check(ctx, &d)
	if err != nil {
		return "", "", err
	}
	t := d.Time
	if req.Time != nil {
		t = *req.Time
	}
	start, end, err := resolveWindow(t, nil, e.now(), e.maxWindow)
	if err != nil {
		return "", "", err
	}
	vars, err := e.values(ctx, &d, checked, start, end, req.Vars)
	if err != nil {
		return "", "", err
	}
	scope := Scope{Start: start, End: end, Vars: vars}
	seen := map[[2]string]bool{}
	var notes []string
	for _, p := range d.Panels {
		var namespace, service string
		unsafeScope := false
		for _, f := range checked.Filters[p.ID] {
			if scope.dropped(f) {
				continue
			}
			tree, err := e.engine.ParseSQL(ctx, "SELECT 1 FROM spans WHERE ("+f.Source+")")
			if err != nil {
				return "", "", err
			}
			var visit func(*SQLExpr)
			visit = func(node *SQLExpr) {
				if node != nil && node.Class == "CONJUNCTION" && node.Type == "CONJUNCTION_AND" {
					children := node.Children
					for _, child := range children {
						visit(child)
					}
					return
				}
				field, value, err := rollupNodeValue(node, scope)
				if err != nil {
					// A string scope cannot represent every checked predicate.
					// Never drop a namespace restriction to obtain guidance.
					unsafeScope = unsafeScope || guidanceReferencesNamespace(node)
					return
				}
				if field == "service" {
					unsafeScope = unsafeScope || (service != "" && service != value)
					service = value
				}
				if field == "namespace" {
					unsafeScope = unsafeScope || (namespace != "" && namespace != value)
					namespace = value
				}
			}
			visit(SQLExpression(tree["where_clause"]))
		}
		key := [2]string{namespace, service}
		if unsafeScope || service == "" || seen[key] {
			continue
		}
		seen[key] = true
		message, err := e.ServiceGuidance(ctx, start, end, namespace, service)
		if err != nil {
			return "", "", err
		}
		if message != "" {
			return "unknown_service", message, nil
		}
		ctx := queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end, Namespace: namespace, Service: service})
		rows, err := e.engine.QueryContext(ctx, `
SELECT
 (SELECT count(*) FROM spans WHERE start_time >= $1::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time < $2::TIMESTAMP_NS::TIMESTAMPTZ_NS AND ($3='' OR namespace=$3) AND service=$4),
 (SELECT count(*) FROM logs WHERE time >= $1::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time < $2::TIMESTAMP_NS::TIMESTAMPTZ_NS AND ($3='' OR namespace=$3) AND service=$4),
 (SELECT count(*) FROM metrics WHERE time >= $1::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time < $2::TIMESTAMP_NS::TIMESTAMPTZ_NS AND ($3='' OR namespace=$3) AND service=$4)`, start, end, namespace, service)
		if err != nil {
			return "", "", err
		}
		var spans, logs, metrics int64
		if rows.Next() {
			err = rows.Scan(&spans, &logs, &metrics)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return "", "", err
		}
		notes = append(notes, fmt.Sprintf("Service %q in the requested window: spans=%d, logs=%d, metrics=%d.", service, spans, logs, metrics))
		if len(seen) == 10 {
			break
		}
	}
	return "no_data", strings.Join(notes, " "), nil
}

func guidanceReferencesNamespace(node *SQLExpr) bool {
	return annotationNamespace(node)
}
