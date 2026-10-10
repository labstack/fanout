package panel

import (
	"context"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

const systemSetupURL = "https://fanout.run/guides/watch-your-system"

type SystemRequest struct {
	Time      Time
	Namespace string
}

// System builds a fresh, ordinary dashboard. Nothing is persisted.
func (e *Executor) System(ctx context.Context, req SystemRequest) (*Dashboard, error) {
	t := req.Time
	t.Refresh = "30s"
	if t.Range == "" && t.From == nil && t.To == nil {
		t.Range = "1h"
	}
	var problems Problems
	validateTime(t, &problems)
	if len(problems) > 0 {
		return nil, problems
	}
	start, end, err := resolveWindow(t, nil, e.now(), e.maxWindow)
	if err != nil {
		return nil, Problems{{Path: "time", Message: err.Error()}}
	}
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end, Namespace: req.Namespace})
	if _, err := e.discoverSchema(ctx, start, end, req.Namespace); err != nil {
		return nil, err
	}
	// Exact family discovery is not subject to Schema's 300-name/sample caps.
	where, args := windowWhere(signals["metrics"], start, end, req.Namespace)
	rows, err := e.engine.QueryContext(ctx, `SELECT name,coalesce(unit,''),type,coalesce(temporality,''),count(*) FROM metrics WHERE `+where+` AND name IN ('httpcheck.status','httpcheck.duration','system.cpu.time','system.memory.usage','system.filesystem.usage','container.cpu.utilization','container.memory.usage.total','container.memory.usage.limit') GROUP BY name,unit,type,temporality`, args...)
	if err != nil {
		return nil, err
	}
	var metrics []MetricSchema
	for rows.Next() {
		var m MetricSchema
		if err = rows.Scan(&m.Name, &m.Unit, &m.Type, &m.Temporality, &m.Count); err != nil {
			break
		}
		metrics = append(metrics, m)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	reachable, hosts, containers := false, false, false
	hasStatus := false
	for _, m := range metrics {
		hasStatus = hasStatus || m.Name == "httpcheck.status"
		reachable = reachable || strings.HasPrefix(m.Name, "httpcheck.")
		hosts = hosts || strings.HasPrefix(m.Name, "system.")
		containers = containers || strings.HasPrefix(m.Name, "container.")
	}
	// Presence and vantages use exact scoped observations from every signal.
	text := systemScopedSQL(`WITH checks AS (SELECT DISTINCT namespace,resource['host.name']::VARCHAR AS host FROM metrics WHERE $__window(time) /* system namespace */ AND name IN ('httpcheck.status','httpcheck.duration')),
 service_hosts AS (SELECT DISTINCT namespace,resource['host.name']::VARCHAR AS host FROM spans WHERE $__window(start_time) /* system namespace */ AND service IS NOT NULL AND service <> '')
 SELECT (SELECT count(*) FROM spans WHERE $__window(start_time) /* system namespace */),
 (SELECT count(*) FROM (`+systemVersionObservations+`) v WHERE version IS NOT NULL AND version<>''),
 (SELECT count(*) FROM checks WHERE host IS NULL OR host=''),
 (SELECT count(*) FROM checks WHERE host IS NOT NULL AND host<>'' AND NOT EXISTS (SELECT 1 FROM service_hosts s WHERE s.namespace=checks.namespace AND s.host=checks.host)),
 (SELECT count(*) FROM metrics WHERE $__window(time) /* system namespace */ AND name IN ('system.cpu.time','system.memory.usage','system.filesystem.usage') AND coalesce(resource['host.name']::VARCHAR,'')=''),
 (SELECT count(*) FROM metrics WHERE $__window(time) /* system namespace */ AND name IN ('container.cpu.utilization','container.memory.usage.total','container.memory.usage.limit') AND (coalesce(resource['host.name']::VARCHAR,'')='' OR coalesce(resource['container.id']::VARCHAR,'')=''))`, req.Namespace)
	expanded, err := expandMacros(text, time.Minute)
	if err != nil {
		return nil, err
	}
	bound, _, values, err := bindParams(expanded, func(name string) ([]any, bool, error) {
		if name == "__from" {
			return []any{start}, false, nil
		}
		return []any{end}, false, nil
	})
	if err != nil {
		return nil, err
	}
	rows, err = e.engine.QueryContext(ctx, bound, values...)
	if err != nil {
		return nil, err
	}
	var spans, versions, unknown, outside, anonymousHosts, anonymousContainers int64
	if rows.Next() {
		err = rows.Scan(&spans, &versions, &unknown, &outside, &anonymousHosts, &anonymousContainers)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	yes, no := true, false
	d := &Dashboard{Name: "System", Time: Time{From: &start, To: &end, Refresh: "30s"}, Annotations: &Annotations{Deploys: &yes, Anomalies: &no}}
	if reachable {
		d.Panels = append(d.Panels, systemReachabilityPanels(req.Namespace)...)
		if !hasStatus {
			d.Panels[0] = systemNote("check_states", "Is it reachable?", "httpcheck.status is missing. Add the Collector `httpcheck` receiver to report check results. [Watch your system]("+systemSetupURL+").")
		}
		// Duration measures and units are discovered, never inferred from a name.
		for i := range d.Panels {
			if d.Panels[i].ID == "check_latency" {
				d.Panels[i] = systemLatencyPanel(req.Namespace, metrics)
			}
		}
		if unknown > 0 {
			d.Panels = append(d.Panels, systemNote("check_vantage", "Check vantage", "Some checks have no reporting host.name; their vantage is unknown."))
		} else if outside == 0 {
			d.Panels = append(d.Panels, systemNote("check_vantage", "Check vantage", "Every check runs on a host that also runs observed services. Local checks cannot see every DNS, CDN or proxy failure that outside visitors encounter."))
		}
	} else {
		d.Panels = append(d.Panels, systemMissing("reachable_missing", "Is it reachable?", "httpcheck"))
	}
	if spans > 0 {
		d.Panels = append(d.Panels, systemServicePanels(req.Namespace)...)
	} else {
		d.Panels = append(d.Panels, systemMissing("services_missing", "Is anything failing or slow?", "otlp"))
	}
	d.Panels = append(d.Panels, systemCapacityPanels(req.Namespace, hosts, containers)...)
	if anonymousHosts > 0 || anonymousContainers > 0 {
		d.Panels = append(d.Panels, systemNote("capacity_identity", "Resource identity missing", "Some infrastructure observations have no stable resource identity and are omitted from the tables. Hosts need host.name; containers need host.name and container.id. [Watch your system]("+systemSetupURL+")."))
	}
	if versions > 0 {
		d.Panels = append(d.Panels, systemVersionPanel(req.Namespace))
	} else {
		d.Panels = append(d.Panels, systemMissing("versions_missing", "What changed?", "otlp"))
	}
	systemPack(d)
	if err := e.Validate(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

func systemMissing(id, title, receiver string) Panel {
	return systemNote(id, title, "No telemetry for this section in the selected range and namespace. Add the Collector `"+receiver+"` receiver. [Watch your system]("+systemSetupURL+").")
}
func systemNote(id, title, content string) Panel {
	return Panel{ID: id, Title: title, Viz: "text", Width: 12, Height: "s", Content: content}
}
func systemScopedSQL(text, namespace string) string {
	predicate := ""
	if namespace != "" {
		predicate = " AND namespace = " + sqlString(namespace)
	}
	return strings.ReplaceAll(text, "/* system namespace */", predicate)
}
func systemPack(d *Dashboard) {
	y := 0
	for i := range d.Panels {
		p := &d.Panels[i]
		h := 6
		if p.Height == "s" {
			h = 3
		}
		if p.Height == "l" {
			h = 9
		}
		p.Grid = &Grid{X: 0, Y: y, W: 12, H: h}
		y += h
	}
}
