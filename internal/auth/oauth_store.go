package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	appid "github.com/labstack/fanout/internal/id"
)

const (
	OAuthAccessTTL  = 15 * time.Minute
	OAuthRefreshTTL = 30 * 24 * time.Hour
	OAuthCodeTTL    = 5 * time.Minute
)

var (
	ErrOAuthClientNotFound = errors.New("oauth client not found")
	ErrInvalidOAuthGrant   = errors.New("invalid oauth grant")
	ErrOAuthRefreshReuse   = errors.New("oauth refresh token reuse detected")
	ErrInvalidOAuthToken   = errors.New("invalid oauth token")
)

type OAuthClient struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	CreatedAt               int64    `json:"client_id_issued_at"`
}

type OAuthAuthorizationCode struct {
	ClientID      string
	UserID        string
	RedirectURI   string
	Scope         string
	Resource      string
	CodeChallenge string
	ExpiresAt     time.Time
}

type OAuthTokenRecord struct {
	Kind      string
	FamilyID  string
	ClientID  string
	UserID    string
	Scope     string
	Resource  string
	ExpiresAt time.Time
	RevokedAt sql.NullInt64
}

type OAuthTokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	Scope        string
}

type OAuthStore struct {
	db  *sql.DB
	now func() time.Time
}

func NewOAuthStore(db *sql.DB) *OAuthStore {
	return &OAuthStore{db: db, now: time.Now}
}

func (s *OAuthStore) RegisterClient(ctx context.Context, name, clientURI string, redirectURIs []string) (OAuthClient, error) {
	id, err := appid.New()
	if err != nil {
		return OAuthClient{}, fmt.Errorf("oauth: generate client id: %w", err)
	}
	client := OAuthClient{
		ClientID:                id,
		ClientName:              name,
		ClientURI:               clientURI,
		RedirectURIs:            append([]string(nil), redirectURIs...),
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		CreatedAt:               s.now().UTC().Unix(),
	}
	redirectJSON, _ := json.Marshal(client.RedirectURIs)
	grantJSON, _ := json.Marshal(client.GrantTypes)
	responseJSON, _ := json.Marshal(client.ResponseTypes)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_clients (
			client_id, client_name, client_uri, redirect_uris_json,
			grant_types_json, response_types_json, token_endpoint_auth_method, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		client.ClientID, client.ClientName, client.ClientURI, string(redirectJSON),
		string(grantJSON), string(responseJSON), client.TokenEndpointAuthMethod, client.CreatedAt,
	)
	if err != nil {
		return OAuthClient{}, fmt.Errorf("oauth: register client: %w", err)
	}
	return client, nil
}

func (s *OAuthStore) GetClient(ctx context.Context, clientID string) (OAuthClient, error) {
	var client OAuthClient
	var redirects, grants, responses string
	err := s.db.QueryRowContext(ctx, `
		SELECT client_id, client_name, client_uri, redirect_uris_json,
		       grant_types_json, response_types_json, token_endpoint_auth_method, created_at
		FROM oauth_clients WHERE client_id = ?`, clientID,
	).Scan(&client.ClientID, &client.ClientName, &client.ClientURI, &redirects,
		&grants, &responses, &client.TokenEndpointAuthMethod, &client.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthClient{}, ErrOAuthClientNotFound
	}
	if err != nil {
		return OAuthClient{}, fmt.Errorf("oauth: get client: %w", err)
	}
	if err := json.Unmarshal([]byte(redirects), &client.RedirectURIs); err != nil {
		return OAuthClient{}, fmt.Errorf("oauth: decode client redirects: %w", err)
	}
	if err := json.Unmarshal([]byte(grants), &client.GrantTypes); err != nil {
		return OAuthClient{}, fmt.Errorf("oauth: decode client grants: %w", err)
	}
	if err := json.Unmarshal([]byte(responses), &client.ResponseTypes); err != nil {
		return OAuthClient{}, fmt.Errorf("oauth: decode client responses: %w", err)
	}
	return client, nil
}

func (s *OAuthStore) CreateAuthorizationCode(ctx context.Context, code OAuthAuthorizationCode) (string, error) {
	raw, err := randomOAuthValue("foc_")
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oauth_authorization_codes (
			code_hash, client_id, user_id, redirect_uri, scope, resource,
			code_challenge, expires_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		oauthHash(raw), code.ClientID, code.UserID, code.RedirectURI, code.Scope,
		code.Resource, code.CodeChallenge, now.Add(OAuthCodeTTL).Unix(), now.Unix(),
	)
	if err != nil {
		return "", fmt.Errorf("oauth: store authorization code: %w", err)
	}
	return raw, nil
}

func (s *OAuthStore) ConsumeAuthorizationCode(ctx context.Context, raw string) (OAuthAuthorizationCode, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return OAuthAuthorizationCode{}, fmt.Errorf("oauth: open code transaction: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return OAuthAuthorizationCode{}, fmt.Errorf("oauth: begin code transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var code OAuthAuthorizationCode
	var expires int64
	err = conn.QueryRowContext(ctx, `
		SELECT client_id, user_id, redirect_uri, scope, resource, code_challenge, expires_at
		FROM oauth_authorization_codes WHERE code_hash = ?`, oauthHash(raw),
	).Scan(&code.ClientID, &code.UserID, &code.RedirectURI, &code.Scope,
		&code.Resource, &code.CodeChallenge, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthAuthorizationCode{}, ErrInvalidOAuthGrant
	}
	if err != nil {
		return OAuthAuthorizationCode{}, fmt.Errorf("oauth: read authorization code: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM oauth_authorization_codes WHERE code_hash = ?`, oauthHash(raw)); err != nil {
		return OAuthAuthorizationCode{}, fmt.Errorf("oauth: consume authorization code: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return OAuthAuthorizationCode{}, fmt.Errorf("oauth: commit code transaction: %w", err)
	}
	committed = true
	code.ExpiresAt = time.Unix(expires, 0).UTC()
	if !code.ExpiresAt.After(s.now()) {
		return OAuthAuthorizationCode{}, ErrInvalidOAuthGrant
	}
	return code, nil
}

func (s *OAuthStore) IssueTokenPair(ctx context.Context, clientID, userID, scope, resource string) (OAuthTokenPair, error) {
	family, err := appid.New()
	if err != nil {
		return OAuthTokenPair{}, fmt.Errorf("oauth: generate token family: %w", err)
	}
	return s.insertTokenPair(ctx, s.db, family, clientID, userID, scope, resource)
}

func (s *OAuthStore) RotateRefreshToken(ctx context.Context, clientID, raw, resource string) (OAuthTokenPair, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return OAuthTokenPair{}, fmt.Errorf("oauth: open refresh transaction: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("oauth: begin refresh transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	record, err := readOAuthToken(ctx, conn, raw)
	if err != nil {
		return OAuthTokenPair{}, err
	}
	now := s.now().UTC().Unix()
	if record.Kind != "refresh" || record.ClientID != clientID || record.Resource != resource || record.ExpiresAt.Unix() <= now {
		return OAuthTokenPair{}, ErrInvalidOAuthGrant
	}
	if record.RevokedAt.Valid {
		if _, revokeErr := conn.ExecContext(ctx, `UPDATE oauth_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE family_id = ?`, now, record.FamilyID); revokeErr != nil {
			return OAuthTokenPair{}, fmt.Errorf("oauth: revoke reused token family: %w", revokeErr)
		}
		if _, commitErr := conn.ExecContext(ctx, "COMMIT"); commitErr != nil {
			return OAuthTokenPair{}, fmt.Errorf("oauth: commit reuse revocation: %w", commitErr)
		}
		committed = true
		return OAuthTokenPair{}, ErrOAuthRefreshReuse
	}
	var active int64
	if err := conn.QueryRowContext(ctx, `SELECT active FROM users WHERE id = ?`, record.UserID).Scan(&active); err != nil || active != 1 {
		if _, revokeErr := conn.ExecContext(ctx, `UPDATE oauth_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE family_id = ?`, now, record.FamilyID); revokeErr != nil {
			return OAuthTokenPair{}, fmt.Errorf("oauth: revoke inactive user token family: %w", revokeErr)
		}
		if _, commitErr := conn.ExecContext(ctx, "COMMIT"); commitErr != nil {
			return OAuthTokenPair{}, fmt.Errorf("oauth: commit inactive user revocation: %w", commitErr)
		}
		committed = true
		return OAuthTokenPair{}, ErrInvalidOAuthGrant
	}
	if _, err := conn.ExecContext(ctx, `UPDATE oauth_tokens SET revoked_at = ? WHERE token_hash = ?`, now, oauthHash(raw)); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("oauth: rotate refresh token: %w", err)
	}
	pair, err := s.insertTokenPair(ctx, conn, record.FamilyID, record.ClientID, record.UserID, record.Scope, record.Resource)
	if err != nil {
		return OAuthTokenPair{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("oauth: commit refresh transaction: %w", err)
	}
	committed = true
	return pair, nil
}

func (s *OAuthStore) VerifyAccessToken(ctx context.Context, raw, resource string) (OAuthTokenRecord, error) {
	record, err := readOAuthToken(ctx, s.db, raw)
	if err != nil {
		return OAuthTokenRecord{}, ErrInvalidOAuthToken
	}
	if record.Kind != "access" || record.Resource != resource || record.RevokedAt.Valid || !record.ExpiresAt.After(s.now()) {
		return OAuthTokenRecord{}, ErrInvalidOAuthToken
	}
	return record, nil
}

type oauthDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *OAuthStore) insertTokenPair(ctx context.Context, db oauthDB, familyID, clientID, userID, scope, resource string) (OAuthTokenPair, error) {
	access, err := randomOAuthValue("foa_")
	if err != nil {
		return OAuthTokenPair{}, err
	}
	refresh, err := randomOAuthValue("for_")
	if err != nil {
		return OAuthTokenPair{}, err
	}
	now := s.now().UTC()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO oauth_tokens (
			token_hash, kind, family_id, client_id, user_id, scope, resource, expires_at, created_at
		) VALUES (?, 'access', ?, ?, ?, ?, ?, ?, ?), (?, 'refresh', ?, ?, ?, ?, ?, ?, ?)`,
		oauthHash(access), familyID, clientID, userID, scope, resource, now.Add(OAuthAccessTTL).Unix(), now.Unix(),
		oauthHash(refresh), familyID, clientID, userID, scope, resource, now.Add(OAuthRefreshTTL).Unix(), now.Unix(),
	); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("oauth: store token pair: %w", err)
	}
	return OAuthTokenPair{AccessToken: access, RefreshToken: refresh, ExpiresIn: int64(OAuthAccessTTL.Seconds()), Scope: scope}, nil
}

func readOAuthToken(ctx context.Context, db oauthDB, raw string) (OAuthTokenRecord, error) {
	var record OAuthTokenRecord
	var expires int64
	err := db.QueryRowContext(ctx, `
		SELECT kind, family_id, client_id, user_id, scope, resource, expires_at, revoked_at
		FROM oauth_tokens WHERE token_hash = ?`, oauthHash(raw),
	).Scan(&record.Kind, &record.FamilyID, &record.ClientID, &record.UserID,
		&record.Scope, &record.Resource, &expires, &record.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthTokenRecord{}, ErrInvalidOAuthGrant
	}
	if err != nil {
		return OAuthTokenRecord{}, fmt.Errorf("oauth: read token: %w", err)
	}
	record.ExpiresAt = time.Unix(expires, 0).UTC()
	return record, nil
}

func randomOAuthValue(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth: generate random value: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func oauthHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
