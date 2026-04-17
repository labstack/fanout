package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// AccessTTL is the lifetime of an access token.
	AccessTTL = 15 * time.Minute
	// RefreshTTL is the lifetime of a refresh token.
	RefreshTTL = 7 * 24 * time.Hour
)

// Claims is the JWT payload for access tokens.
type Claims struct {
	jwt.RegisteredClaims
	Email     string `json:"email"`
	Role      string `json:"role"`
	TokenType string `json:"typ"`
}

// SignAccess creates a short-lived access token.
func SignAccess(secret, userID, email, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
		},
		Email:     email,
		Role:      role,
		TokenType: "access",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// SignRefresh creates a long-lived refresh token.
func SignRefresh(secret, userID string) (string, error) {
	jti, err := generateJTI()
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTTL)),
		ID:        jti,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// VerifyAccess validates an access token and returns the claims.
func VerifyAccess(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	if claims.TokenType != "access" {
		return nil, fmt.Errorf("wrong token type: expected access, got %s", claims.TokenType)
	}
	return claims, nil
}

// VerifyRefresh validates a refresh token and returns the subject (user ID).
func VerifyRefresh(secret, tokenStr string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}
	return claims.Subject, nil
}

// GenerateSecret creates a random 32-byte hex secret for JWT signing.
func GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func generateJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate jti: %w", err)
	}
	return hex.EncodeToString(b), nil
}
