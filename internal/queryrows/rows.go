package queryrows

import (
	"context"
	"database/sql"
	"fmt"
)

// Rows is the database row-stream surface used by query consumers. Keeping it
// as an interface lets storage engines attach resource-lifetime behavior (for
// example, retaining a Parquet snapshot lock until iteration is complete).
type Rows interface {
	Close() error
	Columns() ([]string, error)
	Err() error
	Next() bool
	Scan(dest ...any) error
}

type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
}

// SQLQueryer is implemented by *sql.DB and sqlmock's database handle.
type SQLQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type SQLAdapter struct{ DB SQLQueryer }

func (a SQLAdapter) QueryContext(ctx context.Context, query string, args ...any) (Rows, error) {
	return a.DB.QueryContext(ctx, query, args...)
}

// ReadTransactioner runs related reads on one connection and database snapshot.
type ReadTransactioner interface {
	WithReadTransaction(context.Context, func(Queryer) error) error
}

func (a SQLAdapter) WithReadTransaction(ctx context.Context, read func(Queryer) error) error {
	db, ok := a.DB.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return fmt.Errorf("query database does not support read transactions")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	return read(SQLAdapter{DB: tx})
}
