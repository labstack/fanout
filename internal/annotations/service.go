package annotations

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

var ErrRequest = errors.New("annotations need a positive range of at most 430 days and at most 100 services")

type Request struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Services []string  `json:"services,omitempty"`
	// An empty Namespace uses the query engine's DefaultNamespace.
	Namespace string `json:"namespace,omitempty"`
}
type Deploy struct {
	Namespace string    `json:"namespace"`
	Service   string    `json:"service"`
	Version   string    `json:"version"`
	At        time.Time `json:"at"`
}
type Anomaly struct {
	Namespace string    `json:"namespace"`
	Service   string    `json:"service"`
	Kind      string    `json:"kind"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Title     string    `json:"title"`
	Severity  string    `json:"severity"`
}
type Response struct {
	Deploys   []Deploy  `json:"deploys"`
	Anomalies []Anomaly `json:"anomalies"`
	Truncated bool      `json:"truncated,omitempty"`
}
type Service struct{ db queryrows.Queryer }

func New(db queryrows.Queryer) *Service { return &Service{db: db} }
func (s *Service) Read(ctx context.Context, req Request) (Response, error) {
	out := Response{Deploys: []Deploy{}, Anomalies: []Anomaly{}}
	if req.From.IsZero() || req.To.IsZero() || !req.From.Before(req.To) || req.To.Sub(req.From) > 430*24*time.Hour || len(req.Services) > 100 || len(req.Namespace) > 200 {
		return out, ErrRequest
	}
	if req.Namespace == "" {
		if db, ok := s.db.(interface{ DefaultNamespace() string }); ok {
			req.Namespace = db.DefaultNamespace()
		}
	}
	suffix := ""
	args := []any{req.From.UTC(), req.To.UTC(), req.Namespace, req.Namespace}
	if len(req.Services) > 0 {
		slots := make([]string, len(req.Services))
		for i, v := range req.Services {
			if v == "" || len(v) > 200 {
				return out, ErrRequest
			}
			slots[i] = "?"
			args = append(args, v)
		}
		suffix = " AND service IN (" + strings.Join(slots, ",") + ")"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `WITH versions AS (SELECT *,row_number() OVER(PARTITION BY namespace,service ORDER BY first_seen,service_version) AS n FROM version_rollup)
SELECT namespace,service,service_version,first_seen::TIMESTAMP_NS FROM versions
WHERE first_seen>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND first_seen<?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND (?='' OR namespace=?) AND n>1`+suffix+` ORDER BY first_seen DESC,namespace,service,service_version LIMIT 1001`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a Deploy
		if err := rows.Scan(&a.Namespace, &a.Service, &a.Version, &a.At); err != nil {
			rows.Close()
			return out, err
		}
		a.At = a.At.UTC()
		out.Deploys = append(out.Deploys, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Deploys) > 1000 {
		out.Deploys = out.Deploys[:1000]
		out.Truncated = true
	}
	rows, err = s.db.QueryContext(ctx, `SELECT namespace,service,kind,start_time::TIMESTAMP_NS,end_time::TIMESTAMP_NS,title,severity FROM anomaly_log
WHERE end_time>?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND (?='' OR namespace=?)`+suffix+` ORDER BY end_time DESC,namespace,service,kind LIMIT 1001`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a Anomaly
		if err := rows.Scan(&a.Namespace, &a.Service, &a.Kind, &a.From, &a.To, &a.Title, &a.Severity); err != nil {
			rows.Close()
			return out, err
		}
		a.From = a.From.UTC()
		a.To = a.To.UTC()
		out.Anomalies = append(out.Anomalies, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Anomalies) > 1000 {
		out.Anomalies = out.Anomalies[:1000]
		out.Truncated = true
	}
	rows, err = s.db.QueryContext(ctx, `SELECT coalesce(max(last_ingested_unix_nano),0) FROM rollup_state WHERE cache_key IN ('version_rollup_v1_limited','version_rollup_v1_history_limited')`)
	if err != nil {
		return out, err
	}
	if rows.Next() {
		var limited int64
		if err := rows.Scan(&limited); err != nil {
			rows.Close()
			return out, err
		}
		out.Truncated = out.Truncated || limited > 0
	}
	err = rows.Err()
	rows.Close()
	return out, err
}
