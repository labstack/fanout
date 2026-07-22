package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	appid "github.com/labstack/fanout/internal/id"
)

type UserIdentity struct {
	ID          string
	UserID      string
	Issuer      string
	Subject     string
	EmailAtLink string
	CreatedAt   string
	LastLoginAt string
}

var ErrIdentityConflict = errors.New("identity is already linked")

type IdentityStore struct{ db *sql.DB }

func NewIdentityStore(db *sql.DB) *IdentityStore { return &IdentityStore{db: db} }

func (s *IdentityStore) Find(ctx context.Context, issuer, subject string) (UserIdentity, error) {
	var identity UserIdentity
	var lastLogin sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, issuer, subject, email_at_link, created_at, last_login_at
		FROM user_identities WHERE issuer = ? AND subject = ?`, issuer, subject,
	).Scan(&identity.ID, &identity.UserID, &identity.Issuer, &identity.Subject, &identity.EmailAtLink, &identity.CreatedAt, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return UserIdentity{}, ErrUserNotFound
	}
	if err != nil {
		return UserIdentity{}, fmt.Errorf("auth: find identity: %w", err)
	}
	identity.LastLoginAt = lastLogin.String
	return identity, nil
}

func (s *IdentityStore) LinkWithAudit(ctx context.Context, userID, issuer, subject, email string, audit *AuditStore, event AuditEvent) (UserIdentity, error) {
	if audit == nil {
		return UserIdentity{}, errors.New("auth: audit store is required for identity linking")
	}
	return s.link(ctx, userID, issuer, subject, email, audit, &event)
}

func (s *IdentityStore) CountForUser(ctx context.Context, userID string) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_identities WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("auth: count user identities: %w", err)
	}
	return count, nil
}

func (s *IdentityStore) link(ctx context.Context, userID, issuer, subject, email string, audit *AuditStore, event *AuditEvent) (UserIdentity, error) {
	id, err := appid.New()
	if err != nil {
		return UserIdentity{}, fmt.Errorf("auth: identity id: %w", err)
	}
	now := userTimestamp(time.Now())
	executor := auditExecutor(s.db)
	var tx *sql.Tx
	if event != nil && audit != nil {
		tx, err = s.db.BeginTx(ctx, nil)
		if err != nil {
			return UserIdentity{}, fmt.Errorf("auth: begin identity link: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		executor = tx
	}
	_, err = executor.ExecContext(ctx, `
		INSERT INTO user_identities (id, user_id, issuer, subject, email_at_link, created_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, userID, issuer, subject, email, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return UserIdentity{}, fmt.Errorf("%w: %v", ErrIdentityConflict, err)
		}
		return UserIdentity{}, fmt.Errorf("auth: link identity: %w", err)
	}
	if event != nil && audit != nil {
		event.TargetType = "identity"
		event.TargetID = id
		if err := audit.RecordTx(ctx, tx, *event); err != nil {
			return UserIdentity{}, err
		}
		if err := tx.Commit(); err != nil {
			return UserIdentity{}, fmt.Errorf("auth: commit identity link: %w", err)
		}
	}
	return UserIdentity{ID: id, UserID: userID, Issuer: issuer, Subject: subject, EmailAtLink: email, CreatedAt: now, LastLoginAt: now}, nil
}

func (s *IdentityStore) TouchLogin(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE user_identities SET last_login_at = ? WHERE id = ?`, userTimestamp(time.Now()), id)
	if err != nil {
		return fmt.Errorf("auth: touch identity login: %w", err)
	}
	return nil
}
