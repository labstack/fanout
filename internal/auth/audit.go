package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	appid "github.com/labstack/fanout/internal/id"
	appmetrics "github.com/labstack/fanout/internal/metrics"
)

type AuditEvent struct {
	ActorUserID string
	EventType   string
	Outcome     string
	TargetType  string
	TargetID    string
	RemoteIP    string
	UserAgent   string
	Metadata    map[string]any
}

type AuditStore struct{ db *sql.DB }

func NewAuditStore(db *sql.DB) *AuditStore { return &AuditStore{db: db} }

func (s *AuditStore) Record(ctx context.Context, event AuditEvent) error {
	return recordAudit(ctx, s.db, event)
}

func (s *AuditStore) RecordTx(ctx context.Context, tx *sql.Tx, event AuditEvent) error {
	return recordAudit(ctx, tx, event)
}

func (s *AuditStore) Cleanup(ctx context.Context, retention time.Duration, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM auth_audit_events WHERE created_at < ?`, now.Add(-retention).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("auth: cleanup audit events: %w", err)
	}
	return result.RowsAffected()
}

type auditExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func recordAudit(ctx context.Context, executor auditExecutor, event AuditEvent) error {
	id, err := appid.New()
	if err != nil {
		appmetrics.AuthAuditWriteFailures.Inc()
		return fmt.Errorf("auth: audit id: %w", err)
	}
	metadata := event.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		appmetrics.AuthAuditWriteFailures.Inc()
		return fmt.Errorf("auth: audit metadata: %w", err)
	}
	var actor any
	if event.ActorUserID != "" {
		actor = event.ActorUserID
	}
	_, err = executor.ExecContext(ctx, `
		INSERT INTO auth_audit_events
			(id, actor_user_id, event_type, outcome, target_type, target_id, remote_ip, user_agent, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, actor, event.EventType, event.Outcome, nullableAudit(event.TargetType), nullableAudit(event.TargetID),
		nullableAudit(event.RemoteIP), nullableAudit(event.UserAgent), string(encoded), time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		appmetrics.AuthAuditWriteFailures.Inc()
		return fmt.Errorf("auth: record audit event: %w", err)
	}
	return nil
}

func nullableAudit(value string) any {
	if value == "" {
		return nil
	}
	return value
}
