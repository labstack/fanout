package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryTargetAtRotatesEveryOperationAcrossEveryWindow(t *testing.T) {
	windowsByOperation := make(map[string]map[string]bool)
	for i := 0; i < 25; i++ {
		target := queryTargetAt(i)
		windows := windowsByOperation[target.operation]
		if windows == nil {
			windows = make(map[string]bool)
			windowsByOperation[target.operation] = windows
		}
		for _, window := range queryWindows {
			if target.path == "/api/observability/"+target.operation+"?window="+window+"&limit=100" {
				windows[window] = true
			}
		}
	}

	for operation, windows := range windowsByOperation {
		if len(windows) != len(queryWindows) {
			t.Errorf("%s exercised %d windows, want %d: %#v", operation, len(windows), len(queryWindows), windows)
		}
	}
}

func TestScrapeMetricsSendsBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer metrics-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("fanout_rows_total 42\n"))
	}))
	defer server.Close()

	metrics, err := scrapeMetrics(server.URL, "metrics-secret")
	if err != nil {
		t.Fatal(err)
	}
	if metrics["fanout_rows_total"] != 42 {
		t.Fatalf("metrics = %#v", metrics)
	}
}
