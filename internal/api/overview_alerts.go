package api

import (
	"context"
	"errors"
	"log/slog"

	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/service"
)

// alertLister is the narrow interface computeAlertsState needs; tests stub it.
type alertLister interface {
	ListAlerts(state, service, ruleID string) ([]alert.Alert, error)
}

// asAlertLister returns the lister interface, preserving nil-ness so
// computeAlertsState's `store == nil` check works (the classic Go
// typed-nil-interface gotcha: a typed-nil pointer assigned to an
// interface produces a non-nil interface value).
func asAlertLister(s *alert.Store) alertLister {
	if s == nil {
		return nil
	}
	return s
}

// computeAlertsState builds the wire-level alerts wrapper. Context cancellation
// propagates as an error (the client hung up, response is being abandoned);
// other store errors log + return status=unavailable so the UI can show a banner.
func computeAlertsState(ctx context.Context, store alertLister, namespace string, windowMin int) (alertsOut, error) {
	if store == nil {
		return alertsOut{Status: alertsStatusDisabled, Items: []service.OverviewAlert{}}, nil
	}

	// Bail early if the caller already hung up — avoids a store hit and
	// surfaces the cancellation without misclassifying it as a failure.
	if err := ctx.Err(); err != nil {
		return alertsOut{}, err
	}

	rows, err := store.ListAlerts("firing", "", "")
	if err != nil {
		// Forward-compat: propagate context errors if ListAlerts gains ctx plumbing.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return alertsOut{}, err
		}
		slog.Error("list firing alerts for overview failed",
			"namespace", namespace,
			"window_min", windowMin,
			"err", err)
		return alertsOut{Status: alertsStatusUnavailable, Items: []service.OverviewAlert{}}, nil
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
	return alertsOut{Status: alertsStatusOK, Items: items}, nil
}
