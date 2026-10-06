package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
)

type failedExemplars struct {
	fakePanels
	failure error
}

func (f *failedExemplars) Exemplars(context.Context, panel.ExemplarRequest) (panel.ExemplarResponse, error) {
	return panel.ExemplarResponse{}, f.failure
}

func TestM2FixExemplarHTTPErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"problem", panel.Problems{{Path: "panel_id", Message: "missing"}}, 400, "problems"},
		{"deadline", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), 504, "Exemplars took longer than 10 seconds"},
		{"cancel", context.Canceled, 200, ""},
		{"read_wait", fmt.Errorf("wrapped: %w", query.ErrParquetReadWait), 503, "exemplars unavailable"},
		{"internal", errors.New("failure"), 500, "exemplars unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := servePanels(t, &failedExemplars{failure: tc.err}, http.MethodPost, "/api/panels/exemplars", `{"dashboard":{"name":"x","panels":[]}}`)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.message) {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if tc.name == "cancel" && rec.Body.Len() != 0 {
				t.Fatal("cancel wrote body")
			}
		})
	}
}

func TestM2FixStrictJSONTrailingData(t *testing.T) {
	for _, suffix := range []string{` {}`, ` null`, ` garbage`, strings.Repeat(" ", 512<<10) + `{}`} {
		rec := servePanels(t, &fakePanels{}, http.MethodPost, "/api/panels/exemplars", `{"dashboard":{"name":"x","panels":[]}}`+suffix)
		if rec.Code != 400 {
			t.Fatalf("trailing data accepted: %d", rec.Code)
		}
	}
	rec := servePanels(t, &fakePanels{}, http.MethodPost, "/api/panels/exemplars", `{"dashboard":{"name":"x","panels":[]}}`+" \n\t")
	if rec.Code != 200 {
		t.Fatalf("whitespace rejected: %d %s", rec.Code, rec.Body)
	}
}
