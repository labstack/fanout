package annotations

import (
	"context"
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
