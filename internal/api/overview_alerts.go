package api

import (
	"context"
	"errors"
	"log/slog"

	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/service"
)

// alertLister is the narrow interface computeAlertsState needs from the
// alert store. *alert.Store satisfies it; tests use a stub.
type alertLister interface {
	ListAlerts(state, service, ruleID string) ([]alert.Alert, error)
}

// computeAlertsState determines the wire-level alerts wrapper from the
// alert store. See the state matrix in the spec for the full table.
//
// Context cancellation (Canceled / DeadlineExceeded) is propagated as the
// returned error WITHOUT logging or flipping to "unavailable" — a hung-up
// client is not a subsystem failure and the response is being abandoned.
// Other errors are logged with namespace + a stable code= field and the
// wrapper returns status=unavailable so the UI can show a banner.
func computeAlertsState(ctx context.Context, store alertLister, namespace string, windowMin int) (AlertsOut, error) {
	if store == nil {
		return AlertsOut{Status: AlertsStatusDisabled, Items: []service.OverviewAlert{}}, nil
	}

	// Bail early if the caller already hung up — avoids a store hit and
	// surfaces the cancellation without misclassifying it as a failure.
	if err := ctx.Err(); err != nil {
		return AlertsOut{}, err
	}

	rows, err := store.ListAlerts("firing", "", "")
	if err != nil {
		// Forward-compat: if ListAlerts is ever plumbed for context (it
		// currently uses context.Background internally), context errors
		// should propagate rather than be treated as subsystem failure.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return AlertsOut{}, err
		}
		slog.Error("list firing alerts for overview failed",
			"namespace", namespace,
			"window_min", windowMin,
			"code", "overview.alerts.list_failed",
			"err", err)
		return AlertsOut{Status: AlertsStatusUnavailable, Items: []service.OverviewAlert{}}, nil
	}

	items := make([]service.OverviewAlert, 0, len(rows))
	for _, a := range rows {
		items = append(items, service.OverviewAlert{
			Rule:    a.RuleID,
			Service: a.Service,
			State:   a.State,
			Value:   a.Value,
			FiredAt: a.FiredAt,
		})
	}
	return AlertsOut{Status: AlertsStatusOK, Items: items}, nil
}
