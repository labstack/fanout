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
	engine "github.com/labstack/pitot/internal/duckdb"
	"github.com/labstack/pitot/internal/telemetry/semconv"
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
		if err := v.decode(ctx, path, signal, count); err != nil {
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

// decode forces native decoding of every page of every column that values
// does not read. COUNT alone can use footer metadata, so the queries hash
// every column, including shredded and residual VARIANT values; the XORs are
// discarded: this is readability validation, not a checksum. A query buffers
// the row-group chunks of every column it reads, and a variant column of wide
// metadata alone can approach the verifier's memory limit, so each variant
// column has its own query and the remaining columns share one.
func (v *BatchVerifier) decode(ctx context.Context, path string, s *Signal, expected int) error {
	var scalars []string
	var queries [][]string
	checked := keysByMap(s)
	for _, field := range s.schema.Fields() {
		name := sqlIdentifier(field.Name())
		if c, ok := s.Column(field.Name()); ok && c.Type == Variant {
			if len(checked[field.Name()]) == 0 {
				queries = append(queries, []string{name})
			}
		} else {
			scalars = append(scalars, name)
		}
	}
	if len(scalars) > 0 {
		queries = append(queries, scalars)
	}
	source := parquetSource(path)
	for _, columns := range queries {
		selects := []string{"COUNT(*)"}
		for _, c := range columns {
			selects = append(selects, "bit_xor(hash("+c+"))")
		}
		var count int64
		dest := make([]any, len(selects))
		dest[0] = &count
		for i := 1; i < len(dest); i++ {
			dest[i] = new(any)
		}
		if err := v.db.QueryRowContext(ctx, "SELECT "+strings.Join(selects, ", ")+" FROM "+source).Scan(dest...); err != nil {
			return nativeVerificationError(err)
		}
		if count != int64(expected) {
			return batchDataError(fmt.Errorf("decoded %d rows; metadata declares %d", count, expected))
		}
	}
	return nil
}

// values checks what the physical schema cannot state: every dedicated
// column holds its map value converted to the key's type (coerce), NULL
// when the value has no conversion; the footer counts each key's non-null
// values without one; the parallel lists of a row have equal lengths; and
// metric points carry exactly the columns of their kind.
//
// Every row is checked. Each map has one query that decodes it (the discarded
// hash, as in decode) and checks all of its keys. A compacted file gives every
// row the unified key dictionary of its inputs, and extracting a key walks
// that whole dictionary, so each key once cost a scan of the map;
// variant_normalize first rewrites each row with only the keys it uses. Each
// key is then read as its type name and its text, a few bytes a row: a
// VARIANT result keeps the row's dictionary, and the engine holds one result
// vector per expression outside its memory limit, so VARIANT extracts of
// every key grew the process by a gigabyte on files of wide metadata. The
// list and kind checks read no variant and share one query.
func (v *BatchVerifier) values(ctx context.Context, path string, s *Signal) error {
	source := parquetSource(path)
	counts, err := fileSpills(path, s)
	if err != nil {
		return batchDataError(err)
	}
	checked := keysByMap(s)
	for _, m := range mapColumns {
		keys := checked[m]
		if len(keys) == 0 {
			continue
		}
		columns := []string{m, "variant_normalize(" + m + ") AS " + normalizedMap}
		selects := []string{"bit_xor(hash(" + m + "))"}
		for _, key := range keys {
			// An absent key reads as no type and a null value as VARIANT_NULL:
			// neither is a value, so neither spills.
			columns = append(columns, key.Column)
			selects = append(selects, fmt.Sprintf("count(*) FILTER (WHERE NOT coalesce(%s, false))", coercedSQL(key)),
				fmt.Sprintf("count(*) FILTER (WHERE %s IS NULL AND %s<>'VARIANT_NULL')::UBIGINT", key.Column, keyTypeSQL(key)))
		}
		query := "SELECT " + strings.Join(selects, ", ") + " FROM (SELECT " + strings.Join(columns, ", ") + " FROM " + source + ")"
		results := make([]uint64, 2*len(keys))
		dest := []any{new(any)}
		for i := range results {
			dest = append(dest, &results[i])
		}
		if err := v.db.QueryRowContext(ctx, query).Scan(dest...); err != nil {
			return nativeVerificationError(err)
		}
		for i, key := range keys {
			if broken := results[2*i]; broken > 0 {
				return &CorruptBatchError{fmt.Errorf("%d rows break the %s check", broken, key.Column)}
			}
			if spilled := results[2*i+1]; spilled != counts[key.Bit] {
				return &CorruptBatchError{fmt.Errorf("%d rows spill %s; the footer counts %d", spilled, key.Name, counts[key.Bit])}
			}
		}
	}
	type check struct {
		name string
		sql  string
	}
	var checks []check
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
	results := make([]uint64, len(selects))
	dest := make([]any, len(results))
	for i := range results {
		dest[i] = &results[i]
	}
	if err := v.db.QueryRowContext(ctx, "SELECT "+strings.Join(selects, ", ")+" FROM "+source).Scan(dest...); err != nil {
		return nativeVerificationError(err)
	}
	for i, c := range checks {
		if results[i] > 0 {
			return &CorruptBatchError{fmt.Errorf("%d rows break the %s check", results[i], c.name)}
		}
	}
	return nil
}

func parquetSource(path string) string {
	return "read_parquet(" + sqlString(filepath.ToSlash(path)) + ", hive_partitioning=false)"
}

func sqlIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// mapColumns are the maps whose standard keys have dedicated columns, in
// column order; normalizedMap names the normalized map in a values query.
var mapColumns = [...]string{"attributes", "resource"}

const normalizedMap = "normalized"

// keysByMap groups the signal's dedicated keys by the map that holds them,
// in key-bit order.
func keysByMap(s *Signal) map[string][]semconv.Key {
	keys := map[string][]semconv.Key{}
	for _, key := range s.Keys() {
		m := mapColumns[0]
		if key.Scope == semconv.Resource {
			m = mapColumns[1]
		}
		keys[m] = append(keys[m], key)
	}
	return keys
}

// keyTypeSQL names the type of a key's value in the normalized map of a
// values query: the variant type names, VARIANT_NULL for a null value and
// NULL for an absent key.
func keyTypeSQL(key semconv.Key) string {
	return fmt.Sprintf("variant_type(%s, %s)", normalizedMap, sqlString(key.Name))
}

// keyStringSQL reads a key's value from the normalized map as text: a string
// as itself, an integer in decimal, a boolean as true or false, and a double
// in the shortest form that reads back as the same number.
func keyStringSQL(key semconv.Key) string {
	return fmt.Sprintf("variant_extract_string(%s, %s)", normalizedMap, sqlString(key.Name))
}

// coercedSQL holds when a key's column equals coerce of its map value. A
// float64 converted to text is compared by the number it reads back as:
// strconv's shortest form reads back exactly, and DuckDB does not print it.
func coercedSQL(key semconv.Key) string {
	t, v, c := keyTypeSQL(key), keyStringSQL(key), key.Column
	// DuckDB may evaluate a CASE branch over rows that do not take it, so
	// every branch casts with TRY_CAST. Its BIGINT cast accepts surrounding
	// spaces, which coerce does not, so digits are matched first.
	if key.Type == semconv.Int {
		return fmt.Sprintf(`%[3]s IS NOT DISTINCT FROM CASE %[1]s
WHEN 'INT64' THEN TRY_CAST(%[2]s AS BIGINT)
WHEN 'DOUBLE' THEN CASE WHEN TRY_CAST(%[2]s AS DOUBLE)>=-9223372036854775808::DOUBLE AND TRY_CAST(%[2]s AS DOUBLE)<9223372036854775808::DOUBLE
 AND TRY_CAST(%[2]s AS DOUBLE)=trunc(TRY_CAST(%[2]s AS DOUBLE)) THEN TRY_CAST(TRY_CAST(%[2]s AS DOUBLE) AS BIGINT) END
WHEN 'VARCHAR' THEN CASE WHEN regexp_full_match(%[2]s,'[+-]?[0-9]+') THEN TRY_CAST(%[2]s AS BIGINT) END END`, t, v, c)
	}
	return fmt.Sprintf(`CASE %[1]s
WHEN 'VARCHAR' THEN %[3]s IS NOT DISTINCT FROM %[2]s
WHEN 'INT64' THEN %[3]s IS NOT DISTINCT FROM %[2]s
WHEN 'BOOL_TRUE' THEN %[3]s IS NOT DISTINCT FROM 'true'
WHEN 'BOOL_FALSE' THEN %[3]s IS NOT DISTINCT FROM 'false'
WHEN 'DOUBLE' THEN TRY_CAST(%[3]s AS DOUBLE) IS NOT DISTINCT FROM TRY_CAST(%[2]s AS DOUBLE)
ELSE %[3]s IS NULL END`, t, v, c)
}

// kindChecks hold for every metric point: the columns of its kind are set and
// the others NULL, and its lists have the lengths their kind requires.
var kindChecks = map[*Signal][]string{
	Metrics: {
		`(value IS NOT NULL) = (type NOT IN ('histogram','summary') AND (flags & 1) = 0)`,
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
