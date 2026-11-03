package query

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

// Hold the first panel inside its admitted read until its sibling is queued.
type diagnosisGateEngine struct {
	Engine
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *diagnosisGateEngine) QueryContext(ctx context.Context, text string, args ...any) (queryrows.Rows, error) {
	if strings.HasPrefix(text, "WITH base AS") {
		g.once.Do(func() {
			close(g.entered)
			select {
			case <-g.release:
			case <-ctx.Done():
			}
		})
	}
	return g.Engine.QueryContext(ctx, text, args...)
}

func diagnosisDashboard() Dashboard {
	return Dashboard{Name: "Diagnosis", Time: Time{Range: "1h"}, Panels: []Panel{
		{ID: "text", Title: "Spans", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service"}, Where: []string{"service = 'checkout'", "contains(lower(operation), lower('zzqxnomatch'))"}}},
	}}
}

func TestDiagnosisDoesNotTreatSiblingPanelsAsPressure(t *testing.T) {
	e := newFixtureExecutor(t)
	defer e.Close()
	e.admission = newReadAdmission(1, 0, 8)
	gate := &diagnosisGateEngine{Engine: e.engine, entered: make(chan struct{}), release: make(chan struct{})}
	e.engine = gate
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	d := diagnosisDashboard()
	sibling := d.Panels[0]
	sibling.ID = "sibling"
	d.Panels = append(d.Panels, sibling)
	done := make(chan []Result, 1)
	go func() {
		results, err := e.Run(ctx, RunRequest{Dashboard: d})
		if err != nil {
			t.Error(err)
		}
		done <- results
	}()
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waitForReadWaiters(t, e.admission, 1)
	close(gate.release)
	for _, r := range <-done {
		if r.partial || r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, `Without operation containing "zzqxnomatch", 120 match the remaining filters.`) {
			t.Errorf("sibling suppressed diagnosis: %+v", r)
		}
	}
}

func TestDiagnosisPressureDecisionSurvivesQueueDraining(t *testing.T) {
	e := newFixtureExecutor(t)
	defer e.Close()
	e.SetCacheTTL(time.Minute)
	e.admission = newReadAdmission(1, 0, 8)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := e.admission.acquire(ctx, panelClass); err != nil {
		t.Fatal(err)
	}
	// Work from another request is already queued when this batch arrives.
	other := make(chan error, 1)
	go func() { other <- e.admission.acquire(ctx, panelClass) }()
	waitForReadWaiters(t, e.admission, 1)
	done := make(chan []Result, 1)
	go func() {
		results, err := e.Run(ctx, RunRequest{Dashboard: diagnosisDashboard()})
		if err != nil {
			t.Error(err)
		}
		done <- results
	}()
	waitForReadWaiters(t, e.admission, 2)
	e.admission.release(panelClass)
	if err := <-other; err != nil {
		t.Fatal(err)
	}
	e.admission.release(panelClass)
	results := <-done
	if len(results) != 1 {
		t.Fatalf("results=%+v", results)
	}
	r := results[0]
	if !r.partial || r.Status != StatusEmpty || !strings.Contains(r.Diagnosis, "diagnosis was skipped because the server is busy") {
		t.Fatalf("pressure not reported: %+v", r)
	}
	if len(e.cache.entries) != 0 {
		t.Fatal("deferred diagnosis was cached")
	}
	// A fresh request after pressure clears gets a complete diagnosis.
	results, err := e.Run(ctx, RunRequest{Dashboard: diagnosisDashboard()})
	if err != nil {
		t.Fatal(err)
	}
	r = results[0]
	if r.Cached || r.partial || !strings.Contains(r.Diagnosis, `Without operation containing "zzqxnomatch", 120 match the remaining filters.`) {
		t.Fatalf("diagnosis did not recover: %+v", r)
	}
	if len(e.cache.entries) != 1 {
		t.Fatal("complete diagnosis was not cached")
	}
}
