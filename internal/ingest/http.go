package ingest

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/labstack/pitot/internal/settings"
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
// Browser/API routes use a separate handler and are deliberately absent.
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
		writeOTLPStatus(w, r, http.StatusMethodNotAllowed, codes.Unimplemented, "method must be POST")
		return
	}
	if err := h.authorizer.authorizeToken(r.Context(), ingestTokenFromHTTP(r.Header)); err != nil {
		switch {
		case errors.Is(err, errInvalidIngestToken):
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeOTLPStatus(w, r, http.StatusUnauthorized, codes.Unauthenticated, "unauthorized")
		default:
			writeOTLPStatus(w, r, http.StatusServiceUnavailable, codes.Unavailable, "ingest authentication unavailable")
		}
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != otlpProtobufContentType {
		writeOTLPStatus(w, r, http.StatusUnsupportedMediaType, codes.InvalidArgument, "Content-Type must be application/x-protobuf")
		return
	}
	body, err := readOTLPHTTPBody(r, h.maxBodyBytes)
	if errors.Is(err, errOTLPHTTPBodyTooLarge) {
		writeOTLPStatus(w, r, http.StatusRequestEntityTooLarge, codes.InvalidArgument, "OTLP request exceeds 64 MiB decompressed limit")
		return
	}
	if errors.Is(err, errUnsupportedOTLPContentEncoding) {
		writeOTLPStatus(w, r, http.StatusUnsupportedMediaType, codes.InvalidArgument, err.Error())
		return
	}
	if err != nil {
		writeOTLPStatus(w, r, http.StatusBadRequest, codes.InvalidArgument, "invalid request body")
		return
	}
	if err := proto.Unmarshal(body, req); err != nil {
		writeOTLPStatus(w, r, http.StatusBadRequest, codes.InvalidArgument, "invalid protobuf payload")
		return
	}
	resp, err := export(r.Context())
	if err != nil {
		// Invalid payloads are permanent failures; exporters must not retry them.
		if status.Code(err) == codes.InvalidArgument {
			writeOTLPStatus(w, r, http.StatusBadRequest, codes.InvalidArgument, status.Convert(err).Message())
			return
		}
		// Shedding is throttling: OTLP/HTTP senders retry 429 after Retry-After.
		if status.Code(err) == codes.ResourceExhausted {
			delay := shedRetryDelay
			for _, detail := range status.Convert(err).Details() {
				if retry, ok := detail.(*errdetails.RetryInfo); ok {
					delay = retry.RetryDelay.AsDuration()
				}
			}
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(delay.Seconds())))))
			writeOTLPStatus(w, r, http.StatusTooManyRequests, codes.ResourceExhausted, status.Convert(err).Message())
			return
		}
		// Cancellation/backpressure remains retryable. Never acknowledge rows
		// that were not accepted by the shared pipeline.
		writeOTLPStatus(w, r, http.StatusServiceUnavailable, codes.Unavailable, "telemetry was not accepted")
		return
	}
	encoded, err := proto.Marshal(resp)
	if err != nil {
		writeOTLPStatus(w, r, http.StatusInternalServerError, codes.Internal, "encode OTLP response")
		return
	}
	w.Header().Set("Content-Type", otlpProtobufContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// writeOTLPStatus answers a failed export as OTLP/HTTP requires: the HTTP
// status with a google.rpc.Status body, in JSON for a JSON request and in
// protobuf otherwise.
func writeOTLPStatus(w http.ResponseWriter, r *http.Request, httpCode int, code codes.Code, message string) {
	body := status.New(code, message).Proto()
	contentType, encode := otlpProtobufContentType, proto.Marshal
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil && mediaType == "application/json" {
		contentType, encode = "application/json", protojson.Marshal
	}
	encoded, err := encode(body)
	if err != nil {
		http.Error(w, message, httpCode)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(httpCode)
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
