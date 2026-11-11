package ingest

import (
	"context"
	"net"
	"syscall"
	"testing"

	"github.com/labstack/pitot/internal/config"
	"github.com/labstack/pitot/internal/telemetry"
	telemetrystore "github.com/labstack/pitot/internal/telemetry/store"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Compare the admission handler with a full protobuf round trip. The latter
// includes client CPU: decoded protobufs are unavoidable at this admission seam.
func BenchmarkRefusedExport(b *testing.B) {
	for _, wire := range []bool{false, true} {
		name := "handler"
		if wire {
			name = "grpc"
		}
		b.Run(name, func(b *testing.B) {
			writer := telemetrystore.NewWriter(nil, 50000)
			writer.SetAdmission(func() int { return telemetry.CompactionDebtLimit }, nil)
			srv := NewServer(config.Config{}, writer)
			req := testTraceRequest()
			spans := req.ResourceSpans[0].ScopeSpans[0].Spans
			for len(req.ResourceSpans[0].ScopeSpans[0].Spans) < 1000 {
				req.ResourceSpans[0].ScopeSpans[0].Spans = append(req.ResourceSpans[0].ScopeSpans[0].Spans, spans[0])
			}
			export := srv.exportTraces
			if wire {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					b.Fatal(err)
				}
				server := grpc.NewServer()
				RegisterOTLP(server, srv)
				go server.Serve(listener)
				b.Cleanup(server.Stop)
				conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { conn.Close() })
				client := collectortrace.NewTraceServiceClient(conn)
				export = func(ctx context.Context, r *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
					return client.Export(ctx, r)
				}
			}
			// Establish the connection before sampling CPU.
			if _, err := export(context.Background(), req); status.Code(err) != codes.ResourceExhausted {
				b.Fatal(err)
			}
			cpu := refusalCPU()
			b.ReportAllocs()
			for b.Loop() {
				if _, err := export(context.Background(), req); status.Code(err) != codes.ResourceExhausted {
					b.Fatal(err)
				}
			}
			b.ReportMetric((refusalCPU()-cpu)*1e6/float64(b.N), "cpu-us/request")
		})
	}
}
func refusalCPU() float64 {
	var u syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &u); err != nil {
		panic(err)
	}
	return float64(u.Utime.Sec+u.Stime.Sec) + float64(u.Utime.Usec+u.Stime.Usec)/1e6
}
