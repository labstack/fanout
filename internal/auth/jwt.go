package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// AccessTTL is the lifetime of an access token.
	AccessTTL = 15 * time.Minute
	// RefreshTTL is the lifetime of a refresh token.
	RefreshTTL = 7 * 24 * time.Hour
	// TokenTimePrecision keeps JWT iat/exp stable enough for refresh revocation checks.
	TokenTimePrecision = time.Millisecond
)

const (
	accessTokenType  = "access"
	refreshTokenType = "refresh"
)

func init() {
	jwt.TimePrecision = TokenTimePrecision
}

// Claims is the JWT payload for access and refresh tokens.
type Claims struct {
	jwt.RegisteredClaims
	TokenType string `json:"typ"`
}

// SignAccess creates a short-lived access token.
func SignAccess(secret, userID string) (string, error) {
	now := time.Now().UTC()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
		},
		TokenType: accessTokenType,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// SignRefresh creates a long-lived refresh token.
func SignRefresh(secret, userID string, issuedAt time.Time) (string, error) {
	now := issuedAt.UTC()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTTL)),
		},
		TokenType: refreshTokenType,
	})
	return token.SignedString([]byte(secret))
}

// VerifyAccess validates an access token and returns the claims.
func VerifyAccess(secret, tokenStr string) (*Claims, error) {
	claims, err := parseClaims(secret, tokenStr)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != accessTokenType {
		return nil, fmt.Errorf("wrong token type: expected access, got %s", claims.TokenType)
	}
	return claims, nil
}

// VerifyRefresh validates a refresh token and returns the claims.
func VerifyRefresh(secret, tokenStr string) (*Claims, error) {
	claims, err := parseClaims(secret, tokenStr)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != refreshTokenType {
		return nil, fmt.Errorf("wrong token type: expected refresh, got %s", claims.TokenType)
	}
	return claims, nil
}

func parseClaims(secret, tokenStr string) (*Claims, error) {
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
	return claims, nil
}
