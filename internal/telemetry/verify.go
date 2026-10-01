package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	driver "github.com/duckdb/duckdb-go/v2"
	engine "github.com/labstack/fanout/internal/duckdb"
)

var ErrUnsupportedBatchFormat = errors.New("unsupported batch metadata")

// CorruptBatchError denotes evidence of malformed authoritative data. A failed
// verifier (permissions, engine limits or cancellation) is not such evidence.
type CorruptBatchError struct{ Err error }

func (e *CorruptBatchError) Error() string { return e.Err.Error() }
func (e *CorruptBatchError) Unwrap() error { return e.Err }
func IsBatchCorrupt(err error) bool        { var e *CorruptBatchError; return errors.As(err, &e) }

func batchDataError(err error) error {
	if err == nil || errors.Is(err, ErrUnsupportedBatchFormat) {
		return err
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return &CorruptBatchError{err}
}

// BatchVerifier reuses one bounded native engine for an offline batch walk.
// It creates no catalog, spill files, or downloaded extensions.
type BatchVerifier struct{ db *sql.DB }

func NewBatchVerifier(ctx context.Context, directory string) (*BatchVerifier, error) {
	db, err := sql.Open("duckdb", "?threads=2&memory_limit=256MB")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"SET TimeZone='UTC'", "SET temp_directory=''"} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			break
		}
	}
	if err == nil {
		err = engine.ConfigurePolicy(ctx, db, directory)
	}
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize batch verifier: %w", err)
	}
	return &BatchVerifier{db: db}, nil
}
func (v *BatchVerifier) Close() error { return v.db.Close() }

// ValidatePublishedBatch deeply decodes a single batch and checks its schema,
// row counts, complete trace ordering and exact trace-index ranges.
func ValidatePublishedBatch(dir string) (err error) {
	v, err := NewBatchVerifier(context.Background(), dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, v.Close()) }()
	return v.Validate(context.Background(), dir)
}

func (v *BatchVerifier) Validate(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	batch, err := loadRegisteredBatch(dir)
	if err != nil {
		return batchDataError(err)
	}
	signals := []struct {
		name  string
		count int
	}{
		{"spans", batch.metadata.Spans},
		{"logs", batch.metadata.Logs},
		{"metrics", batch.metadata.Metrics},
	}
	for _, signal := range signals {
		if signal.count == 0 {
			continue
		}
		path := filepath.Join(dir, signal.name+".parquet")
		if err := v.decode(ctx, path, signal.count); err != nil {
			return fmt.Errorf("verify %s Parquet: %w", signal.name, err)
		}
	}
	if batch.metadata.Spans > 0 {
		if err := verifySpanParquet(filepath.Join(dir, "spans.parquet"), batch.metadata.Spans, batch.traces); err != nil {
			return batchDataError(err)
		}
	}
	return nil
}

func (v *BatchVerifier) decode(ctx context.Context, path string, expected int) error {
	// COUNT alone can use footer metadata. Hash every column to force vector
	// decoding of all pages, including shredded and residual VARIANT values.
	// The XOR is discarded: this is readability validation, not a checksum.
	query := "SELECT COUNT(*), bit_xor(hash(COLUMNS(*))) FROM read_parquet('" + strings.ReplaceAll(filepath.ToSlash(path), "'", "''") + "', hive_partitioning=false)"
	rows, err := v.db.QueryContext(ctx, query)
	if err != nil {
		return nativeVerificationError(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	var count int64
	dest[0] = &count
	for i := 1; i < len(dest); i++ {
		dest[i] = &values[i]
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nativeVerificationError(err)
		}
		return errors.New("batch verifier returned no row")
	}
	if err := rows.Scan(dest...); err != nil {
		return nativeVerificationError(err)
	}
	if rows.Next() {
		return errors.New("batch verifier returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return nativeVerificationError(err)
	}
	if count != int64(expected) {
		return batchDataError(fmt.Errorf("decoded %d rows; metadata declares %d", count, expected))
	}
	return nil
}

func nativeVerificationError(err error) error {
	var e *driver.Error
	if errors.As(err, &e) {
		if e.Type == driver.ErrorTypeInvalid && strings.Contains(e.Error(), "TProtocolException:") {
			return &CorruptBatchError{err}
		}
		switch e.Type {
		case driver.ErrorTypeInvalidInput, driver.ErrorTypeConversion, driver.ErrorTypeSerialization, driver.ErrorTypeOutOfRange:
			return &CorruptBatchError{err}
		}
	}
	return err
}

type verificationSpanRow struct {
	TraceHash     uint64 `parquet:"_trace_hash"`
	TraceID       string `parquet:"trace_id"`
	StartUnixNano int64  `parquet:"start_unix_nano"`
	SpanID        string `parquet:"span_id"`
}
