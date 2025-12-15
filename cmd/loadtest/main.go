package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Alibaba MSCallGraph schema:
// traceid, timestamp, rpcid, um (upstream), rpctype, dm (downstream), interface, rt

var serviceNames = []string{
	"api-gateway", "order-service", "user-service", "product-service",
	"inventory-service", "payment-service", "auth-service", "notification-service",
	"search-service", "recommendation-service", "cart-service", "shipping-service",
	"analytics-service", "cache-service", "postgres", "redis", "elasticsearch",
	"kafka", "rabbitmq", "mongodb",
}

func hashToService(hash string) string {
	if hash == "" {
		return "unknown"
	}
	// Use first 8 chars of hash to pick a service name deterministically
	h := sha256.Sum256([]byte(hash))
	idx := int(h[0]) % len(serviceNames)
	return serviceNames[idx]
}

func rpcidToSpanID(rpcid string) []byte {
	h := sha256.Sum256([]byte(rpcid))
	return h[:8]
}

func rpcidToParentID(rpcid string) []byte {
	parts := strings.Split(rpcid, ".")
	if len(parts) <= 1 {
		return nil // root span
	}
	parentRpcid := strings.Join(parts[:len(parts)-1], ".")
	return rpcidToSpanID(parentRpcid)
}

func main() {
	csvFile := flag.String("file", "testdata/MSCallGraph_0.csv", "Alibaba trace CSV file")
	endpoint := flag.String("endpoint", "localhost:4317", "OTLP gRPC endpoint")
	limit := flag.Int("limit", 100000, "Max rows to load (0 = all)")
	batchSize := flag.Int("batch", 1000, "Batch size for sending")
	flag.Parse()

	ctx := context.Background()

	conn, err := grpc.NewClient(*endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	traceClient := coltracepb.NewTraceServiceClient(conn)
	logsClient := collogspb.NewLogsServiceClient(conn)

	f, err := os.Open(*csvFile)
	if err != nil {
		log.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()

	reader := csv.NewReader(bufio.NewReader(f))

	// Skip header
	header, _ := reader.Read()
	log.Printf("Columns: %v", header)

	baseTime := time.Now().Add(-5 * time.Minute) // Start traces from 5 minutes ago

	var spans []*tracepb.Span
	var logs []*logspb.LogRecord
	count := 0
	traceCount := 0
	lastTraceID := ""

	log.Printf("Loading from %s to %s (limit: %d)", *csvFile, *endpoint, *limit)

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if *limit > 0 && count >= *limit {
			break
		}

		// Parse: idx, traceid, timestamp, rpcid, um, rpctype, dm, interface, rt
		if len(record) < 9 {
			continue
		}

		traceID := record[1]
		timestampMs, _ := strconv.ParseInt(record[2], 10, 64)
		rpcid := record[3]
		um := record[4] // upstream (caller)
		rpctype := record[5]
		dm := record[6] // downstream (callee)
		iface := record[7]
		rtMs, _ := strconv.ParseFloat(record[8], 64)

		if traceID != lastTraceID {
			traceCount++
			lastTraceID = traceID
		}

		// Convert trace ID (hex string) to bytes
		traceIDBytes, _ := hex.DecodeString(traceID[:min(32, len(traceID))])
		if len(traceIDBytes) < 16 {
			padded := make([]byte, 16)
			copy(padded, traceIDBytes)
			traceIDBytes = padded
		}

		spanID := rpcidToSpanID(rpcid)
		parentID := rpcidToParentID(rpcid)

		// Calculate timestamps - Alibaba timestamp is when call completed
		// rt is response time in ms. Ensure minimum 1ms duration.
		if rtMs < 1 {
			rtMs = 1
		}
		endTime := baseTime.Add(time.Duration(timestampMs) * time.Millisecond)
		startTime := endTime.Add(-time.Duration(rtMs) * time.Millisecond)

		// Service name - use downstream (dm) as the service handling the call
		serviceName := hashToService(dm)
		if dm == "" {
			serviceName = hashToService(um)
		}

		// Operation name
		opName := rpctype
		if iface != "" {
			opName = iface
		}
		if opName == "" {
			opName = "unknown"
		}

		// Determine span kind and status
		kind := tracepb.Span_SPAN_KIND_SERVER
		if rpctype == "mc" || rpctype == "redis" {
			kind = tracepb.Span_SPAN_KIND_CLIENT
		}

		status := &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}
		// Simulate some errors based on response time (very slow = likely error)
		if rtMs > 5000 {
			status = &tracepb.Status{
				Code:    tracepb.Status_STATUS_CODE_ERROR,
				Message: "timeout exceeded",
			}
		}

		span := &tracepb.Span{
			TraceId:           traceIDBytes,
			SpanId:            spanID,
			ParentSpanId:      parentID,
			Name:              opName,
			Kind:              kind,
			StartTimeUnixNano: uint64(startTime.UnixNano()),
			EndTimeUnixNano:   uint64(endTime.UnixNano()),
			Status:            status,
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: serviceName}}},
				{Key: "rpc.type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: rpctype}}},
			},
		}
		spans = append(spans, span)

		// Generate a log for ~10% of spans
		if count%10 == 0 {
			severity := logspb.SeverityNumber_SEVERITY_NUMBER_INFO
			body := fmt.Sprintf("Processing %s", opName)
			if status.Code == tracepb.Status_STATUS_CODE_ERROR {
				severity = logspb.SeverityNumber_SEVERITY_NUMBER_ERROR
				body = fmt.Sprintf("Error: %s", status.Message)
			}

			lg := &logspb.LogRecord{
				TimeUnixNano:   uint64(startTime.UnixNano()),
				SeverityNumber: severity,
				SeverityText:   severity.String(),
				Body:           &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: body}},
				TraceId:        traceIDBytes,
				SpanId:         spanID,
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: serviceName}}},
				},
			}
			logs = append(logs, lg)
		}

		count++

		// Send batch
		if len(spans) >= *batchSize {
			sendSpans(ctx, traceClient, spans)
			sendLogs(ctx, logsClient, logs)
			spans = nil
			logs = nil

			if count%10000 == 0 {
				log.Printf("Loaded %d spans (%d traces)", count, traceCount)
			}
		}
	}

	// Send remaining
	if len(spans) > 0 {
		sendSpans(ctx, traceClient, spans)
		sendLogs(ctx, logsClient, logs)
	}

	log.Printf("Done. Loaded %d spans from %d traces", count, traceCount)
}

func sendSpans(ctx context.Context, client coltracepb.TraceServiceClient, spans []*tracepb.Span) {
	// Group by service
	byService := make(map[string][]*tracepb.Span)
	for _, sp := range spans {
		svc := getAttr(sp.Attributes, "service.name")
		byService[svc] = append(byService[svc], sp)
	}

	var resourceSpans []*tracepb.ResourceSpans
	for svc, svcSpans := range byService {
		rs := &tracepb.ResourceSpans{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: svc}}},
				},
			},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: svcSpans}},
		}
		resourceSpans = append(resourceSpans, rs)
	}

	_, err := client.Export(ctx, &coltracepb.ExportTraceServiceRequest{ResourceSpans: resourceSpans})
	if err != nil {
		log.Printf("Failed to send spans: %v", err)
	}
}

func sendLogs(ctx context.Context, client collogspb.LogsServiceClient, logs []*logspb.LogRecord) {
	if len(logs) == 0 {
		return
	}

	byService := make(map[string][]*logspb.LogRecord)
	for _, lg := range logs {
		svc := getAttr(lg.Attributes, "service.name")
		byService[svc] = append(byService[svc], lg)
	}

	var resourceLogs []*logspb.ResourceLogs
	for svc, svcLogs := range byService {
		rl := &logspb.ResourceLogs{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: svc}}},
				},
			},
			ScopeLogs: []*logspb.ScopeLogs{{LogRecords: svcLogs}},
		}
		resourceLogs = append(resourceLogs, rl)
	}

	_, err := client.Export(ctx, &collogspb.ExportLogsServiceRequest{ResourceLogs: resourceLogs})
	if err != nil {
		log.Printf("Failed to send logs: %v", err)
	}
}

func getAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			if sv, ok := a.Value.Value.(*commonpb.AnyValue_StringValue); ok {
				return sv.StringValue
			}
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
