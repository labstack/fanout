package ingest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/fanout/internal/config"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type shedSubmitter struct{}

func (shedSubmitter) Submit(context.Context, telemetrystore.Batch) error {
	return telemetrystore.ErrIngestOverBudget
}

// OTLP exporters retry RESOURCE_EXHAUSTED only when it carries RetryInfo;
// without it a shed request is dropped by the sender.
func TestShedExportCarriesRetryInfo(t *testing.T) {
	srv := NewServer(config.Config{DefaultNamespace: "default"}, shedSubmitter{})
	_, err := srv.exportTraces(context.Background(), testTraceRequest())
	st := status.Convert(err)
	if st.Code() != codes.ResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", st.Code())
	}
	var delay *errdetails.RetryInfo
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.RetryInfo); ok {
			delay = info
		}
	}
	if delay == nil || delay.GetRetryDelay().AsDuration() <= 0 {
		t.Fatalf("details = %v, want a positive RetryInfo delay", st.Details())
	}
}

// OTLP/HTTP treats 429 as retryable and honors Retry-After.
func TestHTTPShedExportIsThrottled(t *testing.T) {
	f := newHTTPIngestFixture(t, true)
	f.handler = NewHTTPHandler(NewServer(config.Config{DefaultNamespace: "default"}, shedSubmitter{}), f.store)
	payload, err := proto.Marshal(testTraceRequest())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(payload))
	req.Header.Set("Content-Type", otlpProtobufContentType)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status = %d Retry-After = %q, want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
}
