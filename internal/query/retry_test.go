package query

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// transient is the canonical transient DuckLake IO error string that
// isTransientLakeIOError must classify as retryable.
const transientMsg = "IO Error: Cannot open file: No such file or directory"

// ---- isTransientLakeIOError ----

func TestIsTransientLakeIOError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "context.Canceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "generic error",
			err:  errors.New("syntax error"),
			want: false,
		},
		{
			name: "IO Error only (missing No such file)",
			err:  errors.New("IO Error: something else"),
			want: false,
		},
		{
			name: "No such file only (missing IO Error)",
			err:  errors.New("something: No such file or directory"),
			want: false,
		},
		{
			name: "both substrings present",
			err:  errors.New(transientMsg),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isTransientLakeIOError(tc.err)
			if got != tc.want {
				t.Errorf("isTransientLakeIOError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ---- QueryContext retry tests ----

func TestQueryContextRetriesThenSucceeds(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db}
	ctx := context.Background()

	// First call: transient error → should retry.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).
		WillReturnError(errors.New(transientMsg))
	// Second call: success.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).
		WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))

	rows, err := d.QueryContext(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("QueryContext() error = %v, want nil", err)
	}
	if rows == nil {
		t.Fatal("QueryContext() rows = nil, want non-nil")
	}
	rows.Close()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (retry did not happen): %v", err)
	}
}

func TestQueryContextNonTransientReturnsImmediately(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db}
	ctx := context.Background()

	// Non-transient error — exactly one call, no retry.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).
		WillReturnError(errors.New("syntax error"))

	_, queryErr := d.QueryContext(ctx, "SELECT 1")
	if queryErr == nil {
		t.Fatal("QueryContext() error = nil, want syntax error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected extra call (retry happened on non-transient error): %v", err)
	}
}

func TestQueryContextExhaustsRetries(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db}
	ctx := context.Background()

	// All 3 attempts fail with transient error.
	for i := 0; i < 3; i++ {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).
			WillReturnError(errors.New(transientMsg))
	}

	_, queryErr := d.QueryContext(ctx, "SELECT 1")
	if queryErr == nil {
		t.Fatal("QueryContext() error = nil after exhausted retries, want transient error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected call count (expected exactly 3 attempts): %v", err)
	}
}

// ---- QueryRowScan retry test ----

func TestQueryRowScanRetriesThenSucceeds(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Duck{DB: db}
	ctx := context.Background()

	// First call: transient error (sqlmock surfaces it at Query time, which the
	// wrapper classifies and retries — same observable outcome as an error at Scan).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).
		WillReturnError(errors.New(transientMsg))
	// Second call: success with one row.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(42))

	var dst int
	if err := d.QueryRowScan(ctx, []any{&dst}, "SELECT 1"); err != nil {
		t.Fatalf("QueryRowScan() error = %v, want nil", err)
	}
	if dst != 42 {
		t.Errorf("QueryRowScan() scanned %d, want 42", dst)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations (retry did not happen): %v", err)
	}
}

// ---- applyMemoryHeadroom tests ----

func TestApplyMemoryHeadroomCapsSmallBox(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 6.4 GiB ≈ 80% of 8 GiB → total≈8 GiB, capped = 8 GiB - 2 GiB = 6 GiB < 6.4 GiB → SET fires.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT current_setting('memory_limit')")).
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("6.4 GiB"))
	mock.ExpectExec("SET memory_limit=").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := applyMemoryHeadroom(ctx, db); err != nil {
		t.Fatalf("applyMemoryHeadroom() error = %v, want nil", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SET memory_limit was not issued for small box: %v", err)
	}
}

func TestApplyMemoryHeadroomSkipsBigBox(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 100 GiB → total≈125 GiB, capped=123 GiB >= 100 GiB limit → no-op (capped >= limit).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT current_setting('memory_limit')")).
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("100 GiB"))
	// No ExpectExec: if a SET fires, sqlmock reports it as an unexpected call
	// and ExpectationsWereMet fails.

	if err := applyMemoryHeadroom(ctx, db); err != nil {
		t.Fatalf("applyMemoryHeadroom() error = %v, want nil", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SET memory_limit was unexpectedly issued for big box: %v", err)
	}
}
