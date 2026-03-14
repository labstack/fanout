package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAutoGranularity(t *testing.T) {
	cases := []struct {
		window int
		want   string
	}{
		{15, "1m"},
		{60, "1m"},
		{61, "5m"},
		{360, "5m"},
		{361, "15m"},
		{1440, "15m"},
		{1441, "1h"},
		{10080, "1h"},
	}
	for _, c := range cases {
		got := autoGranularity(c.window)
		if got != c.want {
			t.Errorf("autoGranularity(%d) = %q, want %q", c.window, got, c.want)
		}
	}
}

func TestGranularityMinutes(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"1m", 1},
		{"5m", 5},
		{"15m", 15},
		{"1h", 60},
		{"auto", 5}, // unknown falls back to 5
	}
	for _, c := range cases {
		got := granularityMinutes(c.input)
		if got != c.want {
			t.Errorf("granularityMinutes(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestMetrics_UnknownAction(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	_, err := svc.MetricsTool(context.Background(), MetricParams{Action: "bad"})
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestMetricsList_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"name", "type", "unit", "description", "services"}))

	result, err := svc.MetricsList(context.Background(), MetricParams{Window: 15})
	if err != nil {
		t.Fatalf("MetricsList() error = %v", err)
	}
	if len(result.Metrics) != 0 {
		t.Errorf("Metrics len = %d, want 0", len(result.Metrics))
	}
}

func TestMetricsList_WithRows(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"name", "type", "unit", "description", "services"}).
			AddRow("http.server.duration", "histogram", "ms", "HTTP server request duration", []byte(`["frontend","checkout"]`)).
			AddRow("db.query.duration", "gauge", "ms", "DB query latency", []byte(`["db-service"]`)))

	result, err := svc.MetricsList(context.Background(), MetricParams{Window: 60})
	if err != nil {
		t.Fatalf("MetricsList() error = %v", err)
	}
	if len(result.Metrics) != 2 {
		t.Fatalf("Metrics len = %d, want 2", len(result.Metrics))
	}
	if result.Metrics[0].Name != "http.server.duration" {
		t.Errorf("Metrics[0].Name = %q", result.Metrics[0].Name)
	}
	if result.Metrics[0].Type != "histogram" {
		t.Errorf("Metrics[0].Type = %q, want 'histogram'", result.Metrics[0].Type)
	}
	if len(result.Metrics[0].Services) != 2 {
		t.Errorf("Metrics[0].Services len = %d, want 2", len(result.Metrics[0].Services))
	}
}

func TestMetricsList_QueryError(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	result, err := svc.MetricsList(context.Background(), MetricParams{Window: 15})
	if err != nil {
		t.Fatalf("MetricsList() should not return error, got %v", err)
	}
	if len(result.Metrics) != 0 {
		t.Errorf("Metrics len = %d, want 0 on error", len(result.Metrics))
	}
}

func TestMetricsQuery_NoName(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	_, err := svc.MetricsQuery(context.Background(), MetricParams{
		Window:      15,
		Aggregation: "avg",
	})
	if err == nil {
		t.Error("expected error when no name provided")
	}
}

func TestMetricsQuery_InvalidAggregation(t *testing.T) {
	svc, _ := newMockService(t)
	defer svc.duck.DB.Close()

	_, err := svc.MetricsQuery(context.Background(), MetricParams{
		Name:        "http.server.duration",
		Aggregation: "median",
		Window:      15,
	})
	if err == nil {
		t.Error("expected error for invalid aggregation")
	}
}

func TestMetricsQuery_Empty(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "service", "value", "unit"}))

	result, err := svc.MetricsQuery(context.Background(), MetricParams{
		Name:        "http.server.duration",
		Aggregation: "avg",
		Window:      15,
	})
	if err != nil {
		t.Fatalf("MetricsQuery() error = %v", err)
	}
	if len(result.Series) != 0 {
		t.Errorf("Series len = %d, want 0", len(result.Series))
	}
	if len(result.Anomalies) != 0 {
		t.Errorf("Anomalies len = %d, want 0", len(result.Anomalies))
	}
}

func TestMetricsQuery_WithData(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	now := time.Now()
	b1 := now.Add(-10 * time.Minute)
	b2 := now.Add(-5 * time.Minute)

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "service", "value", "unit"}).
			AddRow(b1, "", 14.2, "ms").
			AddRow(b2, "", 15.8, "ms"))

	result, err := svc.MetricsQuery(context.Background(), MetricParams{
		Name:        "http.server.duration",
		Aggregation: "avg",
		Window:      15,
	})
	if err != nil {
		t.Fatalf("MetricsQuery() error = %v", err)
	}
	if len(result.Series) != 1 {
		t.Fatalf("Series len = %d, want 1", len(result.Series))
	}
	if result.Series[0].Metric != "http.server.duration" {
		t.Errorf("Series[0].Metric = %q", result.Series[0].Metric)
	}
	if result.Series[0].Aggregation != "avg" {
		t.Errorf("Series[0].Aggregation = %q", result.Series[0].Aggregation)
	}
	if result.Series[0].Unit != "ms" {
		t.Errorf("Series[0].Unit = %q, want 'ms'", result.Series[0].Unit)
	}
	if len(result.Series[0].Datapoints) != 2 {
		t.Errorf("Datapoints len = %d, want 2", len(result.Series[0].Datapoints))
	}
}

func TestMetricsQuery_GroupByService(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	now := time.Now()
	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "service", "value", "unit"}).
			AddRow(now.Add(-10*time.Minute), "frontend", 10.0, "ms").
			AddRow(now.Add(-10*time.Minute), "checkout", 20.0, "ms"))

	result, err := svc.MetricsQuery(context.Background(), MetricParams{
		Name:        "http.server.duration",
		Aggregation: "avg",
		GroupBy:     []string{"service"},
		Window:      15,
	})
	if err != nil {
		t.Fatalf("MetricsQuery() error = %v", err)
	}
	if len(result.Series) != 2 {
		t.Fatalf("Series len = %d, want 2 (one per service)", len(result.Series))
	}
	// Verify labels contain service
	for _, s := range result.Series {
		if s.Labels["service"] == "" {
			t.Errorf("expected service label, got empty")
		}
	}
}

func TestDetectMetricAnomalies_NoAnomaly(t *testing.T) {
	// Uniform values — no anomaly expected
	dps := []MetricDatapoint{
		{Time: "t1", Value: 10.0},
		{Time: "t2", Value: 10.0},
		{Time: "t3", Value: 10.0},
		{Time: "t4", Value: 10.0},
	}
	anomalies := detectMetricAnomalies("test", dps)
	if len(anomalies) != 0 {
		t.Errorf("expected 0 anomalies for uniform data, got %d", len(anomalies))
	}
}

func TestDetectMetricAnomalies_Spike(t *testing.T) {
	// One extreme spike: with 8 stable values and one huge outlier the deviation is well over 2σ
	dps := []MetricDatapoint{
		{Time: "t1", Value: 10.0},
		{Time: "t2", Value: 10.0},
		{Time: "t3", Value: 10.0},
		{Time: "t4", Value: 10.0},
		{Time: "t5", Value: 10.0},
		{Time: "t6", Value: 10.0},
		{Time: "t7", Value: 10.0},
		{Time: "t8", Value: 10.0},
		{Time: "t9", Value: 10000.0}, // extreme spike
	}
	anomalies := detectMetricAnomalies("test", dps)
	if len(anomalies) == 0 {
		t.Error("expected spike anomaly not detected")
	}
	if anomalies[0].Type != "spike" {
		t.Errorf("anomaly type = %q, want 'spike'", anomalies[0].Type)
	}
}

func TestDetectMetricAnomalies_Drop(t *testing.T) {
	// One extreme drop: 8 stable values at 100 and one near-zero
	dps := []MetricDatapoint{
		{Time: "t1", Value: 100.0},
		{Time: "t2", Value: 100.0},
		{Time: "t3", Value: 100.0},
		{Time: "t4", Value: 100.0},
		{Time: "t5", Value: 100.0},
		{Time: "t6", Value: 100.0},
		{Time: "t7", Value: 100.0},
		{Time: "t8", Value: 100.0},
		{Time: "t9", Value: 0.01}, // extreme drop
	}
	anomalies := detectMetricAnomalies("test", dps)
	if len(anomalies) == 0 {
		t.Error("expected drop anomaly not detected")
	}
	if anomalies[0].Type != "drop" {
		t.Errorf("anomaly type = %q, want 'drop'", anomalies[0].Type)
	}
}

func TestDetectMetricAnomalies_TooFewPoints(t *testing.T) {
	// Less than 3 datapoints — skip anomaly detection
	dps := []MetricDatapoint{
		{Time: "t1", Value: 100.0},
		{Time: "t2", Value: 1.0},
	}
	anomalies := detectMetricAnomalies("test", dps)
	if len(anomalies) != 0 {
		t.Errorf("expected 0 anomalies for < 3 points, got %d", len(anomalies))
	}
}

func TestMetrics_DispatchList(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"name", "type", "unit", "description", "services"}))

	result, err := svc.MetricsTool(context.Background(), MetricParams{Action: "list", Window: 15})
	if err != nil {
		t.Fatalf("Metrics(list) error = %v", err)
	}
	if _, ok := result.(*MetricsListResult); !ok {
		t.Errorf("Metrics(list) returned %T, want *MetricsListResult", result)
	}
}

func TestMetrics_DispatchQuery(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "service", "value", "unit"}))

	result, err := svc.MetricsTool(context.Background(), MetricParams{
		Action:      "query",
		Name:        "http.server.duration",
		Aggregation: "avg",
		Window:      15,
	})
	if err != nil {
		t.Fatalf("Metrics(query) error = %v", err)
	}
	if _, ok := result.(*MetricsQueryResult); !ok {
		t.Errorf("Metrics(query) returned %T, want *MetricsQueryResult", result)
	}
}

func TestMetrics_DefaultActionIsQuery(t *testing.T) {
	svc, mock := newMockService(t)
	defer svc.duck.DB.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"bucket", "service", "value", "unit"}))

	// Action is empty string — should default to query
	result, err := svc.MetricsTool(context.Background(), MetricParams{
		Action:      "",
		Name:        "some.metric",
		Aggregation: "avg",
		Window:      15,
	})
	if err != nil {
		t.Fatalf("Metrics() with empty action error = %v", err)
	}
	if _, ok := result.(*MetricsQueryResult); !ok {
		t.Errorf("empty action returned %T, want *MetricsQueryResult", result)
	}
}
