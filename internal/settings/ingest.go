package settings

import (
	"context"
	"fmt"
)

const ingestKey = "ingest"

// Ingest holds the (optional) ingest-auth configuration.
// When TokenHash is empty, OTLP ingest is unauthenticated. When set, every
// request must present the token via `x-fanout-ingest-token` or Bearer auth.
type Ingest struct {
	TokenHash string `json:"token_hash"`
}

func DefaultIngest() Ingest {
	return Ingest{}
}

func (s *Store) GetIngest(ctx context.Context) (Ingest, error) {
	ingest := DefaultIngest()
	if err := s.Get(ctx, ingestKey, &ingest); err != nil {
		return Ingest{}, err
	}
	return ingest, nil
}

func (s *Store) SetIngest(ctx context.Context, ingest Ingest) error {
	return s.Upsert(ctx, ingestKey, ingest)
}

func GenerateIngestToken() (string, string, error) {
	raw, err := randomHexToken(24)
	if err != nil {
		return "", "", fmt.Errorf("generate ingest token: %w", err)
	}
	token := "fo_" + raw
	return token, HashIngestToken(token), nil
}

func HashIngestToken(token string) string {
	return hashToken(token)
}

func CheckIngestToken(token, hash string) bool {
	return checkToken(token, hash)
}
