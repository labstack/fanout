package main

import (
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
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
	if metrics.total("fanout_rows_total") != 42 {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestHistogramHasExactReleaseSLOBoundary(t *testing.T) {
	h := newHistogram()
	for range 100 {
		h.record(1499 * time.Millisecond)
	}

	if got := h.snapshot().P95Ms; got != mixedQueryP95SLOMs {
		t.Fatalf("p95 = %vms, want exact release SLO boundary %vms", got, mixedQueryP95SLOMs)
	}
}

func TestHistogramBoundsCanEnforceFivePercentGuardrail(t *testing.T) {
	for index := 1; index < len(latBoundsMs); index++ {
		previous := latBoundsMs[index-1]
		current := latBoundsMs[index]
		if ratio := current / previous; ratio > 1.0050001 {
			t.Fatalf("bounds %vms -> %vms have ratio %.4f, want <= 1.005", previous, current, ratio)
		}
	}
}

func TestHistogramRecordsWithBinarySearchAtExactBoundary(t *testing.T) {
	h := newHistogram()
	h.record(500 * time.Microsecond)
	h.record(501 * time.Microsecond)
	if got := h.n.Load(); got != 2 {
		t.Fatalf("samples = %d, want 2", got)
	}
	if got := h.counts[0].Load(); got != 1 {
		t.Fatalf("first bucket = %d, want 1", got)
	}
}

func TestSyntheticSequenceIsDeterministicPerWorkerSeed(t *testing.T) {
	type sequence struct {
		TraceID   []byte
		Namespace string
		Route     string
		Attrs     any
		Status    any
	}
	generate := func() sequence {
		rng := rand.New(rand.NewPCG(17, 3))
		generator := generator{cfg: config{namespaces: 4, cardinality: 100, errorRate: 0.2}}
		return sequence{
			TraceID:   randBytes(rng, 16),
			Namespace: generator.namespace(rng),
			Route:     generator.route(rng),
			Attrs:     generator.spanAttrs(rng),
			Status:    generator.status(rng),
		}
	}

	if first, second := generate(), generate(); !reflect.DeepEqual(first, second) {
		t.Fatalf("same worker seed produced different sequences:\nfirst:  %#v\nsecond: %#v", first, second)
	}
}
