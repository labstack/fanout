package annotations

import (
	"context"
	"errors"
	"github.com/labstack/fanout/internal/queryrows"
	"testing"
	"time"
)

type defaultAnnotationDB struct{ namespaces []string }

func (*defaultAnnotationDB) DefaultNamespace() string { return "default" }
func (d *defaultAnnotationDB) QueryContext(_ context.Context, _ string, args ...any) (queryrows.Rows, error) {
	if len(args) > 0 {
		d.namespaces = append(d.namespaces, args[2].(string))
	}
	return emptyAnnotationRows{}, nil
}

type emptyAnnotationRows struct{}

func (emptyAnnotationRows) Close() error               { return nil }
func (emptyAnnotationRows) Columns() ([]string, error) { return nil, nil }
func (emptyAnnotationRows) Err() error                 { return nil }
func (emptyAnnotationRows) Next() bool                 { return false }
func (emptyAnnotationRows) Scan(...any) error          { return nil }
func TestM2FixEmptyNamespaceUsesDefault(t *testing.T) {
	for _, namespace := range []string{"", "explicit"} {
		t.Run(namespace, func(t *testing.T) {
			db := &defaultAnnotationDB{}
			now := time.Now().UTC()
			if _, err := New(db).Read(t.Context(), Request{From: now.Add(-time.Hour), To: now, Namespace: namespace}); err != nil {
				t.Fatal(err)
			}
			want := namespace
			if want == "" {
				want = "default"
			}
			if len(db.namespaces) != 2 {
				t.Fatalf("reads: %v", db.namespaces)
			}
			for _, got := range db.namespaces {
				if got != want {
					t.Fatalf("namespace=%q want %q", got, want)
				}
			}
		})
	}
}

type recordingAnnotationDB struct {
	defaultAnnotationDB
	inTransaction         bool
	begins, ends, queries int
	failAt                int
}

func (db *recordingAnnotationDB) QueryContext(ctx context.Context, q string, args ...any) (queryrows.Rows, error) {
	if !db.inTransaction {
		return nil, errors.New("annotation query outside transaction")
	}
	db.queries++
	if db.queries == db.failAt {
		return nil, errors.New("read failed")
	}
	return db.defaultAnnotationDB.QueryContext(ctx, q, args...)
}
func (db *recordingAnnotationDB) WithReadTransaction(ctx context.Context, read func(queryrows.Queryer) error) error {
	db.begins++
	db.inTransaction = true
	defer func() { db.inTransaction = false; db.ends++ }()
	return read(db)
}
func TestFinalFixAnnotationsReadOneTransaction(t *testing.T) {
	now := time.Now().UTC()
	for _, failAt := range []int{0, 1, 2, 3} {
		db := &recordingAnnotationDB{failAt: failAt}
		_, err := New(db).Read(t.Context(), Request{From: now.Add(-time.Hour), To: now})
		if failAt == 0 && err != nil {
			t.Fatal(err)
		}
		if failAt > 0 && err == nil {
			t.Fatal("missing read error")
		}
		want := 3
		if failAt > 0 {
			want = failAt
		}
		if db.begins != 1 || db.ends != 1 || db.queries != want || db.inTransaction {
			t.Fatalf("transaction lifecycle: %+v", db)
		}
	}
}

func (db *defaultAnnotationDB) WithReadTransaction(_ context.Context, read func(queryrows.Queryer) error) error {
	return read(db)
}
