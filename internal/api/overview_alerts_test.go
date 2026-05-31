package api

import (
	"context"
	"errors"
	"testing"

	"github.com/labstack/fanout/internal/alert"
)

// stubAlertLister is a test seam matching the alertLister interface.
type stubAlertLister struct {
	listFn func(state, svc, ruleID string) ([]alert.Alert, error)
}

func (s *stubAlertLister) ListAlerts(state, svc, ruleID string) ([]alert.Alert, error) {
	return s.listFn(state, svc, ruleID)
}

func TestComputeAlertsState_NilStore(t *testing.T) {
	out, err := computeAlertsState(context.Background(), nil, "ns", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != AlertsStatusDisabled {
		t.Errorf("Status = %q, want %q", out.Status, AlertsStatusDisabled)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Errorf("Items should be non-nil empty, got %v", out.Items)
	}
}

func TestComputeAlertsState_ContextCanceled(t *testing.T) {
	store := &stubAlertLister{listFn: func(_, _, _ string) ([]alert.Alert, error) {
		return nil, context.Canceled
	}}
	_, err := computeAlertsState(context.Background(), store, "ns", 60)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestComputeAlertsState_ContextDeadlineExceeded(t *testing.T) {
	store := &stubAlertLister{listFn: func(_, _, _ string) ([]alert.Alert, error) {
		return nil, context.DeadlineExceeded
	}}
	_, err := computeAlertsState(context.Background(), store, "ns", 60)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestComputeAlertsState_GenericError(t *testing.T) {
	bang := errors.New("database is locked")
	store := &stubAlertLister{listFn: func(_, _, _ string) ([]alert.Alert, error) {
		return nil, bang
	}}
	out, err := computeAlertsState(context.Background(), store, "ns", 60)
	if err != nil {
		t.Fatalf("unexpected err propagation: %v", err)
	}
	if out.Status != AlertsStatusUnavailable {
		t.Errorf("Status = %q, want %q", out.Status, AlertsStatusUnavailable)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Errorf("Items should be non-nil empty, got %v", out.Items)
	}
}

func TestComputeAlertsState_SuccessEmpty(t *testing.T) {
	store := &stubAlertLister{listFn: func(_, _, _ string) ([]alert.Alert, error) {
		return nil, nil
	}}
	out, err := computeAlertsState(context.Background(), store, "ns", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != AlertsStatusOK {
		t.Errorf("Status = %q, want %q", out.Status, AlertsStatusOK)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Errorf("Items should be non-nil empty, got %v", out.Items)
	}
}

func TestComputeAlertsState_SuccessWithItems(t *testing.T) {
	rows := []alert.Alert{
		{RuleID: "r1", Service: "cart", State: "firing", Value: 0.42, FiredAt: "2026-05-31T12:00:00Z"},
		{RuleID: "r2", Service: "shipping", State: "firing", Value: 1.1, FiredAt: "2026-05-31T12:01:00Z"},
	}
	store := &stubAlertLister{listFn: func(state, svc, ruleID string) ([]alert.Alert, error) {
		if state != "firing" {
			t.Errorf("state filter = %q, want firing", state)
		}
		return rows, nil
	}}
	out, err := computeAlertsState(context.Background(), store, "ns", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != AlertsStatusOK {
		t.Errorf("Status = %q, want %q", out.Status, AlertsStatusOK)
	}
	if len(out.Items) != 2 {
		t.Fatalf("Items len = %d, want 2", len(out.Items))
	}
	if out.Items[0].Rule != "r1" || out.Items[0].Service != "cart" {
		t.Errorf("Items[0] = %+v, want rule=r1 service=cart", out.Items[0])
	}
}
