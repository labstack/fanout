package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestNamespaces_UsesRecentWindow(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("(?s)SELECT DISTINCT namespace.*start_time >= now\\(\\) - INTERVAL 7 DAY").
		WillReturnRows(sqlmock.NewRows([]string{"namespace"}).
			AddRow("default").
			AddRow("payments"))

	namespaces := svc.Namespaces(context.Background())
	if len(namespaces) != 2 {
		t.Fatalf("Namespaces() len = %d, want 2", len(namespaces))
	}
	if namespaces[0] != "default" || namespaces[1] != "payments" {
		t.Fatalf("Namespaces() = %v, want [default payments]", namespaces)
	}
}

func TestMetrics_RowIterationErrorReturnsPartialResults(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	rows := sqlmock.NewRows([]string{"metric_name", "mtype", "cnt", "avg_val", "min_val", "max_val", "services"}).
		AddRow("http.server.duration", "histogram", int64(3), 12.5, 10.0, 20.0, []byte(`["frontend"]`)).
		AddRow("db.query.duration", "gauge", int64(2), 4.0, 2.0, 6.0, []byte(`["db"]`)).
		RowError(1, errors.New("iteration failed"))
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	result, err := svc.Metrics(context.Background(), MetricsParams{Window: 15})
	if err != nil {
		t.Fatalf("Metrics() error = %v", err)
	}
	if len(result.Metrics) != 1 {
		t.Fatalf("Metrics() len = %d, want 1 partial row", len(result.Metrics))
	}
	if result.Metrics[0].Name != "http.server.duration" {
		t.Fatalf("Metrics()[0].Name = %q", result.Metrics[0].Name)
	}
}
