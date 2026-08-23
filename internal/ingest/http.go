package ingest

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/labstack/fanout/internal/settings"
)

const (
	otlpProtobufContentType = "application/x-protobuf"
	maxOTLPHTTPBodyBytes    = int64(64 << 20)
)

var (
	errOTLPHTTPBodyTooLarge           = errors.New("OTLP request exceeds decompressed size limit")
	errUnsupportedOTLPContentEncoding = errors.New("unsupported OTLP content encoding")
)

// NewHTTPHandler exposes only the three stable OTLP/HTTP signal endpoints.
// Browser/API routes live on a different listener and are deliberately absent.
func NewHTTPHandler(srv *Server, settingsStore *settings.Store) http.Handler {
	return &httpIngestHandler{
		srv:          srv,
		authorizer:   newIngestAuthorizer(settingsStore),
		maxBodyBytes: maxOTLPHTTPBodyBytes,
	}
}

type httpIngestHandler struct {
	srv          *Server
	authorizer   *ingestAuthorizer
	maxBodyBytes int64
}

func (h *httpIngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/traces":
		req := &collectortrace.ExportTraceServiceRequest{}
		h.handleExport(w, r, req, func(ctx context.Context) (proto.Message, error) {
			return h.srv.exportTraces(ctx, req)
		})
	case "/v1/metrics":
		req := &collectormetrics.ExportMetricsServiceRequest{}
		h.handleExport(w, r, req, func(ctx context.Context) (proto.Message, error) {
			return h.srv.exportMetrics(ctx, req)
		})
	case "/v1/logs":
		req := &collectorlogs.ExportLogsServiceRequest{}
		h.handleExport(w, r, req, func(ctx context.Context) (proto.Message, error) {
			return h.srv.exportLogs(ctx, req)
		})
	default:
		http.NotFound(w, r)
	}
}

func (h *httpIngestHandler) handleExport(
	w http.ResponseWriter,
	r *http.Request,
	req proto.Message,
	export func(context.Context) (proto.Message, error),
) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method must be POST", http.StatusMethodNotAllowed)
		return
	}
	if err := h.authorizer.authorizeToken(r.Context(), ingestTokenFromHTTP(r.Header)); err != nil {
		switch {
		case errors.Is(err, errInvalidIngestToken), errors.Is(err, errIngestNotInitialized):
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			http.Error(w, "ingest authentication unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != otlpProtobufContentType {
		http.Error(w, "Content-Type must be application/x-protobuf", http.StatusUnsupportedMediaType)
		return
	}
	body, err := readOTLPHTTPBody(r, h.maxBodyBytes)
	if errors.Is(err, errOTLPHTTPBodyTooLarge) {
		http.Error(w, "OTLP request exceeds 64 MiB decompressed limit", http.StatusRequestEntityTooLarge)
		return
	}
	if errors.Is(err, errUnsupportedOTLPContentEncoding) {
		http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
		return
	}
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := proto.Unmarshal(body, req); err != nil {
		http.Error(w, "invalid protobuf payload", http.StatusBadRequest)
		return
	}
	resp, err := export(r.Context())
	if err != nil {
		// The only current processing failure is cancellation/backpressure. Do not
		// acknowledge a request whose rows were not accepted by the shared pipeline.
		http.Error(w, "telemetry was not accepted", http.StatusServiceUnavailable)
		return
	}
	encoded, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "encode OTLP response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", otlpProtobufContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func readOTLPHTTPBody(r *http.Request, limit int64) ([]byte, error) {
	var reader io.Reader = r.Body
	switch encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); encoding {
	case "", "identity":
	case "gzip":
		compressed, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("open gzip body: %w", err)
		}
		defer compressed.Close()
		reader = compressed
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedOTLPContentEncoding, encoding)
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errOTLPHTTPBodyTooLarge
	}
	return body, nil
}

func ingestTokenFromHTTP(header http.Header) string {
	return ingestTokenFromAuthorization(header.Get("Authorization"))
}
