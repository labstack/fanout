package ingest

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	httpserver "github.com/labstack/fanout/internal/server"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestSharedListenerProtocols(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "plaintext", true: "TLS"}[secure], func(t *testing.T) {
			f := newHTTPIngestFixture(t, true)
			g := grpc.NewServer(GRPCServerOptions(f.store)...)
			RegisterOTLP(g, NewServer(config.Config{}, newTestSubmitter(f.spans, f.logs, f.metrics)))
			defer g.Stop()
			app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "SPA") })
			ts := httptest.NewUnstartedServer(nil)
			ts.Config = httpserver.New("", app, f.handler, g)
			creds := insecure.NewCredentials()
			if secure {
				ts.EnableHTTP2 = true
				ts.StartTLS()
				roots := x509.NewCertPool()
				roots.AddCert(ts.Certificate())
				creds = credentials.NewTLS(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13})
			} else {
				ts.Start()
			}
			defer ts.Close()
			conn, err := grpc.NewClient(ts.Listener.Addr().String(), grpc.WithTransportCredentials(creds))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			tc := collectortrace.NewTraceServiceClient(conn)
			if _, err := tc.Export(ctx, testTraceRequest()); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("unauthenticated gRPC: %v", err)
			}
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+f.token)
			if _, err := tc.Export(ctx, testTraceRequest()); err != nil {
				t.Fatal(err)
			}
			if _, err := collectorlogs.NewLogsServiceClient(conn).Export(ctx, testLogsRequest()); err != nil {
				t.Fatal(err)
			}
			if _, err := collectormetrics.NewMetricsServiceClient(conn).Export(ctx, testMetricsRequest()); err != nil {
				t.Fatal(err)
			}
			<-f.spans
			<-f.logs
			<-f.metrics
			for _, tc := range []struct {
				path    string
				message proto.Message
			}{
				{"/v1/traces", testTraceRequest()}, {"/v1/logs", testLogsRequest()}, {"/v1/metrics", testMetricsRequest()},
			} {
				payload, err := proto.Marshal(tc.message)
				if err != nil {
					t.Fatal(err)
				}
				req, err := http.NewRequest(http.MethodPost, ts.URL+tc.path, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", otlpProtobufContentType)
				req.Header.Set("Authorization", "Bearer "+f.token)
				resp, err := ts.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s: %d", tc.path, resp.StatusCode)
				}
			}
			<-f.spans
			<-f.logs
			<-f.metrics
			for _, tc := range []struct {
				path string
				want int
			}{
				{"/", 200}, {"/v1/traces", 405}, {"/v1/unknown", 404}, {"/v1", 404},
			} {
				resp, err := ts.Client().Get(ts.URL + tc.path)
				if err != nil {
					t.Fatal(err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != tc.want {
					t.Fatalf("%s: %d, want %d", tc.path, resp.StatusCode, tc.want)
				}
			}
			// Even malformed gRPC must not fall through to a successful SPA GET.
			req := httptest.NewRequest(http.MethodGet, "/unknown.Service/Export", nil)
			req.Header.Set("Content-Type", "application/grpc")
			rec := httptest.NewRecorder()
			ts.Config.Handler.ServeHTTP(rec, req)
			if rec.Code == 200 || strings.Contains(rec.Body.String(), "SPA") {
				t.Fatalf("gRPC fell through: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

type blockedExport struct{ started, release chan struct{} }

func (b *blockedExport) Submit(ctx context.Context, _ telemetrystore.Batch) error {
	close(b.started)
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSharedListenerShutdownDrainsExports(t *testing.T) {
	for _, transport := range []string{"grpc", "http"} {
		t.Run(transport, func(t *testing.T) {
			f := newHTTPIngestFixture(t, true)
			blocked := &blockedExport{make(chan struct{}), make(chan struct{})}
			srv := NewServer(config.Config{}, blocked)
			g := grpc.NewServer(GRPCServerOptions(f.store)...)
			RegisterOTLP(g, srv)
			defer g.Stop()
			ts := httptest.NewUnstartedServer(nil)
			ts.Config = httpserver.New("", http.NotFoundHandler(), NewHTTPHandler(srv, f.store), g)
			ts.Start()
			defer ts.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := grpc.NewClient(ts.Listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			exported := make(chan error, 1)
			go func() {
				if transport == "grpc" {
					_, err := collectortrace.NewTraceServiceClient(conn).Export(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+f.token), testTraceRequest())
					exported <- err
					return
				}
				payload, _ := proto.Marshal(testTraceRequest())
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/traces", bytes.NewReader(payload))
				if err != nil {
					exported <- err
					return
				}
				req.Header.Set("Content-Type", otlpProtobufContentType)
				req.Header.Set("Authorization", "Bearer "+f.token)
				resp, err := ts.Client().Do(req)
				if err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						err = status.Errorf(codes.Internal, "HTTP %d", resp.StatusCode)
					}
				}
				exported <- err
			}()
			select {
			case <-blocked.started:
			case <-ctx.Done():
				t.Fatal("export did not start")
			}
			stopped := make(chan error, 1)
			go func() { stopped <- ts.Config.Shutdown(ctx) }()
			select {
			case err := <-stopped:
				t.Fatalf("shutdown returned before commit: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			close(blocked.release)
			if err := <-exported; err != nil {
				t.Fatal(err)
			}
			if err := <-stopped; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSharedListenerForceCloseCancelsHTTP2Stream(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("protocol = %s, want HTTP/2", r.Proto)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		close(started)
		<-r.Context().Done()
		close(canceled)
	})
	ts := httptest.NewUnstartedServer(nil)
	ts.Config = httpserver.New("", app, http.NotFoundHandler(), http.NotFoundHandler())
	ts.Start()
	defer ts.Close()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: protocols}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get(ts.URL + "/api/agent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := ts.Config.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown with active stream: %v", err)
	}
	if err := ts.Config.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP/2 stream context was not canceled")
	}
}
