package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/labstack/fanout/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/query"
)

type annotationFake struct{}

func (annotationFake) Read(context.Context, annotations.Request) (annotations.Response, error) {
	return annotations.Response{Deploys: []annotations.Deploy{}, Anomalies: []annotations.Anomaly{}}, nil
}

func TestAnnotationErrorsWrapped(t *testing.T) {
	original := errors.New("storage failed")
	for _, cause := range []error{annotations.ErrRequest, context.DeadlineExceeded, query.ErrParquetReadWait, original} {
		wrapped := fmt.Errorf("annotation query: %w", cause)
		got := annotationError(wrapped)
		var httpErr *echo.HTTPError
		if !errors.As(got, &httpErr) || !errors.Is(got, cause) {
			t.Fatalf("cause lost: %v", got)
		}
	}
	if got := annotationError(fmt.Errorf("read: %w", context.Canceled)); got != nil {
		t.Fatalf("cancellation: %v", got)
	}
}

type annotationErrorFake struct{ err error }

func (f annotationErrorFake) Read(context.Context, annotations.Request) (annotations.Response, error) {
	return annotations.Response{}, f.err
}
func TestAnnotationErrorMappings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"request", annotations.ErrRequest, 400, annotations.ErrRequest.Error()},
		{"deadline", context.DeadlineExceeded, 504, "Annotations took longer than 10 seconds"},
		{"parquet", query.ErrParquetReadWait, 503, "annotations unavailable"},
		{"canceled", context.Canceled, 200, ""},
		{"unknown", errors.New("storage failed"), 500, "annotations unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestAuthServer(t)
			if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
				t.Fatal(err)
			}
			viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
			if err != nil {
				t.Fatal(err)
			}
			RegisterAnnotationRoutes(s.e, annotationErrorFake{tc.err})
			req := sessionRequest(http.MethodPost, "/api/annotations", strings.NewReader(`{"from":"2026-10-01T12:00:00Z","to":"2026-10-01T13:00:00Z"}`), s.login(t, viewer))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.message) {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if tc.name == "canceled" && rec.Body.Len() != 0 {
				t.Fatalf("canceled response body: %s", rec.Body)
			}
		})
	}
}
func TestAnnotationRoute(t *testing.T) {
	s := newTestAuthServer(t)
	_, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	RegisterAnnotationRoutes(s.e, annotationFake{})
	req := sessionRequest(http.MethodPost, "/api/annotations", strings.NewReader(`{"from":"2026-10-01T12:00:00Z","to":"2026-10-01T13:00:00Z","services":["checkout"]}`), s.login(t, viewer))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deploys":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	policy, ok := classifyRoute(http.MethodPost, "/api/annotations")
	if !ok || policy.capability != ReadTelemetry {
		t.Fatalf("policy: %+v %v", policy, ok)
	}
	if _, ok := classifyRoute(http.MethodGet, "/api/annotations"); ok {
		t.Fatal("GET alias allowed")
	}
}

type invalidAnnotationFake struct{}

func (invalidAnnotationFake) Read(context.Context, annotations.Request) (annotations.Response, error) {
	return annotations.Response{}, annotations.ErrRequest
}
func TestAnnotationErrRequest400(t *testing.T) {
	s := newTestAuthServer(t)
	_, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	RegisterAnnotationRoutes(s.e, invalidAnnotationFake{})
	req := sessionRequest(http.MethodPost, "/api/annotations", strings.NewReader(`{"from":"2026-10-01T13:00:00Z","to":"2026-10-01T12:00:00Z"}`), s.login(t, viewer))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), annotations.ErrRequest.Error()) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
