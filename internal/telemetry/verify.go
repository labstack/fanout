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
	"github.com/labstack/fanout/internal/telemetry/semconv"
	"github.com/parquet-go/parquet-go"
)

var ErrUnsupportedBatchFormat = errors.New("unsupported telemetry batch format")

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
	// The shared loader checks the format-5 metadata and complete physical
	// schema before native decoding; unsupported formats are never corruption.
	batch, err := loadRegisteredBatch(dir)
	if err != nil {
		return batchDataError(err)
	}
	for _, signal := range Signals {
		_, count := batch.metadata.Signal(signal)
		if count == 0 {
			continue
		}
		path := filepath.Join(dir, signal.Name+".parquet")
		if err := v.decode(ctx, path, count); err != nil {
			return fmt.Errorf("verify %s Parquet: %w", signal.Name, err)
		}
		if err := v.values(ctx, path, signal); err != nil {
			return fmt.Errorf("verify %s Parquet: %w", signal.Name, err)
		}
		if err := verifyOrder(path, signal, count); err != nil {
			return batchDataError(err)
		}
		if signal.Traces {
			if err := verifyTraces(path, count, batch.traces); err != nil {
				return batchDataError(err)
			}
		}
	}
	return nil
}

func (v *BatchVerifier) decode(ctx context.Context, path string, expected int) error {
	// COUNT alone can use footer metadata. Hash every column to force vector
	// decoding of all pages, including shredded and residual VARIANT values.
	// The XOR is discarded: this is readability validation, not a checksum.
	query := "SELECT COUNT(*), bit_xor(hash(COLUMNS(*))) FROM read_parquet(" + sqlString(filepath.ToSlash(path)) + ", hive_partitioning=false)"
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

// values checks what the physical schema cannot state: every dedicated
// column equals its map value of the key's type, or is NULL with the row's
// spill bit set and a value of another type left in the map; spill bits
// name only the signal's own keys; the footer counts each key's
// spill bits; the parallel lists of a row have equal lengths; and metric
// points carry exactly the columns of their kind.
func (v *BatchVerifier) values(ctx context.Context, path string, s *Signal) error {
	type check struct {
		name string
		sql  string
	}
	var checks []check
	keys := s.Keys()
	var mask uint64
	for _, key := range keys {
		mask |= 1 << key.Bit
		column, _ := s.Column(key.Column)
		source, cast, kind := "attributes", "VARCHAR", "VARCHAR"
		if key.Scope == semconv.Resource {
			source = "resource"
		}
		if column.Type == Int64 {
			cast, kind = "BIGINT", "INT64"
		}
		value := fmt.Sprintf("%s[%s]", source, sqlString(key.Name))
		// An absent key and a null value both read as VARIANT_NULL: no
		// type, so no spill and a NULL column.
		checks = append(checks, check{key.Column, fmt.Sprintf(`CASE WHEN (_spill>>%d)&1=1 THEN %s IS NULL AND variant_typeof(%s) NOT IN ('VARIANT_NULL','%s')
WHEN variant_typeof(%s)='VARIANT_NULL' THEN %s IS NULL
ELSE variant_typeof(%s)='%s' AND %s IS NOT DISTINCT FROM TRY_CAST(%s AS %s) END`,
			key.Bit, key.Column, value, kind, value, key.Column, value, kind, key.Column, value, cast)})
	}
	if len(keys) > 0 {
		checks = append(checks, check{"_spill", fmt.Sprintf("_spill & %d = _spill", mask)})
	}
	groups := map[string][]string{}
	var names []string
	for _, c := range s.Columns {
		if c.Parallel != "" {
			if groups[c.Parallel] == nil {
				names = append(names, c.Parallel)
			}
			groups[c.Parallel] = append(groups[c.Parallel], c.Name)
		}
	}
	for _, name := range names {
		columns := groups[name]
		for _, c := range columns[1:] {
			checks = append(checks, check{name, fmt.Sprintf("len(%s) IS NOT DISTINCT FROM len(%s)", columns[0], c)})
		}
	}
	for _, c := range kindChecks[s] {
		checks = append(checks, check{"metric kind", c})
	}
	if len(checks) == 0 {
		return nil
	}
	var selects []string
	for _, c := range checks {
		selects = append(selects, "count(*) FILTER (WHERE NOT coalesce("+c.sql+", false))")
	}
	for _, key := range keys {
		selects = append(selects, fmt.Sprintf("coalesce(sum((_spill>>%d)&1), 0)::UBIGINT", key.Bit))
	}
	query := "SELECT " + strings.Join(selects, ", ") + " FROM read_parquet(" + sqlString(filepath.ToSlash(path)) + ", hive_partitioning=false)"
	results := make([]uint64, len(selects))
	dest := make([]any, len(results))
	for i := range results {
		dest[i] = &results[i]
	}
	if err := v.db.QueryRowContext(ctx, query).Scan(dest...); err != nil {
		return nativeVerificationError(err)
	}
	for i, c := range checks {
		if results[i] > 0 {
			return &CorruptBatchError{fmt.Errorf("%d rows break the %s check", results[i], c.name)}
		}
	}
	counts, err := fileSpills(path, s)
	if err != nil {
		return batchDataError(err)
	}
	for i, key := range keys {
		if got := results[len(checks)+i]; got != counts[key.Bit] {
			return &CorruptBatchError{fmt.Errorf("%d rows spill %s; the footer counts %d", got, key.Name, counts[key.Bit])}
		}
	}
	return nil
}

// kindChecks hold for every metric point: the columns of its kind are set and
// the others NULL, and its lists have the lengths their kind requires.
var kindChecks = map[*Signal][]string{
	Metrics: {
		`(value IS NOT NULL) = (type NOT IN ('histogram','summary'))`,
		`(hist_count IS NOT NULL) = (type IN ('histogram','summary'))`,
		`(temporality IS NOT NULL) = (type IN ('counter','updown','histogram'))`,
		`(hist_bounds IS NOT NULL) = (type='histogram' AND exp_scale IS NULL)`,
		`(hist_counts IS NOT NULL) = (hist_bounds IS NOT NULL)`,
		`hist_bounds IS NULL OR len(hist_counts)=len(hist_bounds)+1 OR len(hist_counts)=0 AND len(hist_bounds)=0`,
		`(exp_scale IS NOT NULL) <= (type='histogram')`,
		`(exp_zero_count IS NOT NULL) = (exp_scale IS NOT NULL)`,
		`(exp_zero_threshold IS NOT NULL) = (exp_scale IS NOT NULL)`,
		`(exp_positive_offset IS NOT NULL) = (exp_scale IS NOT NULL)`,
		`(exp_positive_counts IS NOT NULL) = (exp_scale IS NOT NULL)`,
		`(exp_negative_offset IS NOT NULL) = (exp_scale IS NOT NULL)`,
		`(exp_negative_counts IS NOT NULL) = (exp_scale IS NOT NULL)`,
		`(quantiles IS NOT NULL) = (type='summary')`,
		`(exemplar_times IS NOT NULL) = (type<>'summary')`,
	},
}

func fileSpills(path string, s *Signal) (spills, error) {
	f, err := os.Open(path)
	if err != nil {
		return spills{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return spills{}, err
	}
	file, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		return spills{}, err
	}
	return readSpills(file, s)
}

func sqlString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

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
	TraceHash uint64 `parquet:"_trace_hash"`
	TraceID   string `parquet:"trace_id"`
}
