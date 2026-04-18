package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAttributesFromJSON_Logs(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(
		sqlmock.NewRows([]string{"count"}).AddRow(int64(5000)))
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality"}).
			AddRow("http.method", int64(800), int64(4)).
			AddRow("http.url", int64(750), int64(200)))
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality"}).
			AddRow("service.version", int64(900), int64(3)))

	result, err := svc.Attributes(context.Background(), AttributeParams{
		Signal: "logs",
		Window: 60,
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("Attributes() error = %v", err)
	}

	if result.Signal != "logs" {
		t.Errorf("Signal = %q, want %q", result.Signal, "logs")
	}
	if result.TotalRows != 5000 {
		t.Errorf("TotalRows = %d, want 5000", result.TotalRows)
	}
	if len(result.Attributes) != 2 {
		t.Fatalf("Attributes count = %d, want 2", len(result.Attributes))
	}
	if result.Attributes[0].Key != "http.method" {
		t.Errorf("Attributes[0].Key = %q, want %q", result.Attributes[0].Key, "http.method")
	}
	if result.Attributes[0].DiscoveryMethod != "sample" {
		t.Errorf("DiscoveryMethod = %q, want %q", result.Attributes[0].DiscoveryMethod, "sample")
	}
	if result.Attributes[0].Cardinality != 4 {
		t.Errorf("Cardinality = %d, want 4", result.Attributes[0].Cardinality)
	}
	if len(result.ResourceAttributes) != 1 {
		t.Fatalf("ResourceAttributes count = %d, want 1", len(result.ResourceAttributes))
	}
	if len(result.Warnings) == 0 {
		t.Error("Warnings should include approximate counts note")
	}
}

func TestAttributesFromJSON_Metrics(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(
		sqlmock.NewRows([]string{"count"}).AddRow(int64(1000)))
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality"}))
	mock.ExpectQuery("SELECT key").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality"}))

	result, err := svc.Attributes(context.Background(), AttributeParams{
		Signal: "metrics",
		Window: 15,
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("Attributes() error = %v", err)
	}

	if result.Signal != "metrics" {
		t.Errorf("Signal = %q, want %q", result.Signal, "metrics")
	}
	for _, w := range result.Warnings {
		if w == "Attribute discovery for metrics is not yet supported. Use the query tool with json_keys() on a small sample." {
			t.Error("Should no longer return 'not yet supported' warning")
		}
	}
}

func TestAttributes_SpansDiscoveryMethod(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("WITH data").WillReturnRows(
		sqlmock.NewRows([]string{"key", "count", "cardinality", "samples", "total_rows"}).
			AddRow("http_method", int64(90), int64(3), `["GET","POST","PUT"]`, int64(100)).
			AddRow("service_version", int64(80), int64(2), `["1.0","1.1"]`, int64(100)).
			AddRow("__total_rows__", int64(0), int64(0), `[]`, int64(100)))

	result, err := svc.Attributes(context.Background(), AttributeParams{
		Signal: "spans",
		Window: 15,
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("Attributes() error = %v", err)
	}

	if len(result.Attributes) != 1 {
		t.Fatalf("Attributes count = %d, want 1", len(result.Attributes))
	}
	if result.Attributes[0].DiscoveryMethod != "column" {
		t.Errorf("DiscoveryMethod = %q, want %q", result.Attributes[0].DiscoveryMethod, "column")
	}
	if result.TotalRows != 100 {
		t.Errorf("TotalRows = %d, want 100", result.TotalRows)
	}
	if len(result.ResourceAttributes) != 1 {
		t.Fatalf("ResourceAttributes count = %d, want 1", len(result.ResourceAttributes))
	}
	if result.ResourceAttributes[0].Key != "service.version" {
		t.Errorf("ResourceAttributes[0].Key = %q, want service.version", result.ResourceAttributes[0].Key)
	}
}
