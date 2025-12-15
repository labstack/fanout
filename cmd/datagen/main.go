package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	mrand "math/rand"
	"os"
	"os/signal"
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

// Service definitions
type Service struct {
	Name       string
	Operations []Op
	BaseLatMs  int
	VarLatMs   int
}

type Op struct {
	Name     string
	Weight   int
	Calls    []string
	ErrRate  float64
	SlowRate float64
}

var services = []Service{
	{
		Name:      "api-gateway",
		BaseLatMs: 5, VarLatMs: 15,
		Operations: []Op{
			{Name: "POST /api/orders", Weight: 30, Calls: []string{"order-service"}, ErrRate: 0.03},
			{Name: "GET /api/orders/{id}", Weight: 40, Calls: []string{"order-service"}, ErrRate: 0.01},
			{Name: "GET /api/products", Weight: 50, Calls: []string{"product-service"}, ErrRate: 0.01},
			{Name: "POST /api/auth/login", Weight: 20, Calls: []string{"auth-service"}, ErrRate: 0.05},
			{Name: "GET /api/users/me", Weight: 30, Calls: []string{"user-service"}, ErrRate: 0.02},
		},
	},
	{
		Name:      "order-service",
		BaseLatMs: 20, VarLatMs: 50,
		Operations: []Op{
			{Name: "createOrder", Weight: 30, Calls: []string{"inventory-service", "payment-service"}, ErrRate: 0.05},
			{Name: "getOrder", Weight: 40, Calls: []string{"postgres"}, ErrRate: 0.01},
			{Name: "listOrders", Weight: 20, Calls: []string{"postgres", "redis"}, ErrRate: 0.02},
		},
	},
	{
		Name:      "product-service",
		BaseLatMs: 10, VarLatMs: 30,
		Operations: []Op{
			{Name: "getProduct", Weight: 50, Calls: []string{"postgres", "redis"}, ErrRate: 0.01},
			{Name: "searchProducts", Weight: 30, Calls: []string{"elasticsearch"}, ErrRate: 0.02, SlowRate: 0.1},
			{Name: "updateInventory", Weight: 10, Calls: []string{"postgres"}, ErrRate: 0.03},
		},
	},
	{
		Name:      "auth-service",
		BaseLatMs: 15, VarLatMs: 25,
		Operations: []Op{
			{Name: "validateToken", Weight: 60, Calls: []string{"redis"}, ErrRate: 0.02},
			{Name: "login", Weight: 20, Calls: []string{"postgres"}, ErrRate: 0.08},
			{Name: "refreshToken", Weight: 15, Calls: []string{"redis"}, ErrRate: 0.03},
		},
	},
	{
		Name:      "user-service",
		BaseLatMs: 12, VarLatMs: 20,
		Operations: []Op{
			{Name: "getUser", Weight: 50, Calls: []string{"postgres", "redis"}, ErrRate: 0.01},
			{Name: "updateUser", Weight: 20, Calls: []string{"postgres"}, ErrRate: 0.03},
		},
	},
	{
		Name:      "inventory-service",
		BaseLatMs: 25, VarLatMs: 40,
		Operations: []Op{
			{Name: "checkStock", Weight: 40, Calls: []string{"postgres"}, ErrRate: 0.02},
			{Name: "reserveStock", Weight: 30, Calls: []string{"postgres"}, ErrRate: 0.04},
		},
	},
	{
		Name:      "payment-service",
		BaseLatMs: 100, VarLatMs: 200,
		Operations: []Op{
			{Name: "processPayment", Weight: 40, Calls: []string{"stripe-api"}, ErrRate: 0.08, SlowRate: 0.15},
			{Name: "refund", Weight: 10, Calls: []string{"stripe-api"}, ErrRate: 0.05},
		},
	},
	{
		Name: "postgres", BaseLatMs: 2, VarLatMs: 15,
		Operations: []Op{
			{Name: "SELECT", Weight: 60, ErrRate: 0.003},
			{Name: "INSERT", Weight: 20, ErrRate: 0.01},
			{Name: "UPDATE", Weight: 15, ErrRate: 0.008},
		},
	},
	{
		Name: "redis", BaseLatMs: 1, VarLatMs: 3,
		Operations: []Op{
			{Name: "GET", Weight: 70, ErrRate: 0.001},
			{Name: "SET", Weight: 25, ErrRate: 0.002},
		},
	},
	{
		Name: "elasticsearch", BaseLatMs: 30, VarLatMs: 100,
		Operations: []Op{
			{Name: "search", Weight: 80, ErrRate: 0.02, SlowRate: 0.1},
			{Name: "index", Weight: 15, ErrRate: 0.03},
		},
	},
	{
		Name: "stripe-api", BaseLatMs: 200, VarLatMs: 500,
		Operations: []Op{
			{Name: "charges.create", Weight: 60, ErrRate: 0.05, SlowRate: 0.2},
			{Name: "refunds.create", Weight: 20, ErrRate: 0.03},
		},
	},
}

var svcMap = make(map[string]*Service)

func init() {
	for i := range services {
		svcMap[services[i].Name] = &services[i]
	}
}

func main() {
	endpoint := flag.String("endpoint", "localhost:4317", "OTLP gRPC endpoint")
	rps := flag.Int("rps", 10, "requests per second")
	duration := flag.Duration("duration", 5*time.Minute, "how long to generate data")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	conn, err := grpc.NewClient(*endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	traceClient := coltracepb.NewTraceServiceClient(conn)
	logsClient := collogspb.NewLogsServiceClient(conn)

	log.Printf("Generating data: endpoint=%s rps=%d duration=%v", *endpoint, *rps, *duration)
	log.Printf("Services: %d", len(services))

	ticker := time.NewTicker(time.Second / time.Duration(*rps))
	defer ticker.Stop()

	timeout := time.After(*duration)
	count := 0

	for {
		select {
		case <-ticker.C:
			go generateAndSend(ctx, traceClient, logsClient)
			count++
			if count%100 == 0 {
				log.Printf("Generated %d traces", count)
			}
		case <-timeout:
			log.Printf("Done. Generated %d traces", count)
			return
		case <-ctx.Done():
			log.Printf("Interrupted. Generated %d traces", count)
			return
		}
	}
}

func generateAndSend(ctx context.Context, traceClient coltracepb.TraceServiceClient, logsClient collogspb.LogsServiceClient) {
	gateway := svcMap["api-gateway"]
	op := pickOp(gateway)

	traceID := genBytes(16)
	rootSpanID := genBytes(8)

	var allSpans []*tracepb.Span
	var allLogs []*logspb.LogRecord

	baseTime := time.Now().Add(-time.Duration(mrand.Intn(1000)) * time.Millisecond)
	spans, logs := genSpan(gateway, op, traceID, rootSpanID, nil, baseTime, 0)
	allSpans = append(allSpans, spans...)
	allLogs = append(allLogs, logs...)

	// Group spans by service for ResourceSpans
	byService := make(map[string][]*tracepb.Span)
	for _, sp := range allSpans {
		svc := getAttr(sp.Attributes, "service.name")
		byService[svc] = append(byService[svc], sp)
	}

	var resourceSpans []*tracepb.ResourceSpans
	for svc, spans := range byService {
		rs := &tracepb.ResourceSpans{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: svc}}},
				},
			},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
		}
		resourceSpans = append(resourceSpans, rs)
	}

	// Send traces
	if len(resourceSpans) > 0 {
		traceClient.Export(ctx, &coltracepb.ExportTraceServiceRequest{ResourceSpans: resourceSpans})
	}

	// Group logs by service
	if len(allLogs) > 0 {
		byServiceLogs := make(map[string][]*logspb.LogRecord)
		for _, lg := range allLogs {
			svc := getAttr(lg.Attributes, "service.name")
			byServiceLogs[svc] = append(byServiceLogs[svc], lg)
		}

		var resourceLogs []*logspb.ResourceLogs
		for svc, logs := range byServiceLogs {
			rl := &logspb.ResourceLogs{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: svc}}},
					},
				},
				ScopeLogs: []*logspb.ScopeLogs{{LogRecords: logs}},
			}
			resourceLogs = append(resourceLogs, rl)
		}
		logsClient.Export(ctx, &collogspb.ExportLogsServiceRequest{ResourceLogs: resourceLogs})
	}
}

func genSpan(svc *Service, op Op, traceID, spanID, parentID []byte, start time.Time, depth int) ([]*tracepb.Span, []*logspb.LogRecord) {
	if depth > 5 {
		return nil, nil
	}

	latMs := svc.BaseLatMs + mrand.Intn(svc.VarLatMs)
	if op.SlowRate > 0 && mrand.Float64() < op.SlowRate {
		latMs *= 3 + mrand.Intn(5)
	}
	latency := time.Duration(latMs) * time.Millisecond

	isError := mrand.Float64() < op.ErrRate
	status := &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}
	if isError {
		status = &tracepb.Status{
			Code:    tracepb.Status_STATUS_CODE_ERROR,
			Message: pickError(svc.Name, op.Name),
		}
	}

	var allSpans []*tracepb.Span
	var allLogs []*logspb.LogRecord

	childStart := start.Add(time.Duration(2+mrand.Intn(5)) * time.Millisecond)
	for _, callSvc := range op.Calls {
		if target, ok := svcMap[callSvc]; ok {
			childOp := pickOp(target)
			childSpanID := genBytes(8)
			childSpans, childLogs := genSpan(target, childOp, traceID, childSpanID, spanID, childStart, depth+1)
			allSpans = append(allSpans, childSpans...)
			allLogs = append(allLogs, childLogs...)
			if len(childSpans) > 0 {
				childEnd := time.Unix(0, int64(childSpans[0].EndTimeUnixNano))
				childStart = childEnd.Add(time.Duration(1+mrand.Intn(3)) * time.Millisecond)
			}
		}
	}

	// Ensure parent ends after all children
	end := start.Add(latency)
	if len(allSpans) > 0 {
		lastChildEnd := time.Unix(0, int64(allSpans[len(allSpans)-1].EndTimeUnixNano))
		if lastChildEnd.After(end) {
			end = lastChildEnd.Add(time.Duration(2+mrand.Intn(5)) * time.Millisecond)
		}
	}

	span := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            spanID,
		ParentSpanId:      parentID,
		Name:              op.Name,
		Kind:              tracepb.Span_SPAN_KIND_SERVER,
		StartTimeUnixNano: uint64(start.UnixNano()),
		EndTimeUnixNano:   uint64(end.UnixNano()),
		Status:            status,
		Attributes: []*commonpb.KeyValue{
			{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: svc.Name}}},
		},
	}
	allSpans = append([]*tracepb.Span{span}, allSpans...)

	// Generate log
	if mrand.Float64() < 0.3 || isError {
		severity := logspb.SeverityNumber_SEVERITY_NUMBER_INFO
		body := "Processing " + op.Name
		if isError {
			severity = logspb.SeverityNumber_SEVERITY_NUMBER_ERROR
			body = "Error: " + status.Message
		} else if mrand.Float64() < 0.1 {
			severity = logspb.SeverityNumber_SEVERITY_NUMBER_WARN
			body = "Slow response in " + op.Name
		}

		lg := &logspb.LogRecord{
			TimeUnixNano:   uint64(start.Add(time.Duration(mrand.Intn(int(latency.Milliseconds()))) * time.Millisecond).UnixNano()),
			SeverityNumber: severity,
			SeverityText:   severity.String(),
			Body:           &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: body}},
			TraceId:        traceID,
			SpanId:         spanID,
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: svc.Name}}},
			},
		}
		allLogs = append(allLogs, lg)
	}

	return allSpans, allLogs
}

func pickOp(svc *Service) Op {
	total := 0
	for _, op := range svc.Operations {
		total += op.Weight
	}
	r := mrand.Intn(total)
	for _, op := range svc.Operations {
		r -= op.Weight
		if r < 0 {
			return op
		}
	}
	return svc.Operations[0]
}

func pickError(svc, op string) string {
	errors := []string{
		"connection timeout", "connection refused", "deadline exceeded",
		"internal server error", "resource exhausted", "permission denied",
	}
	specific := map[string][]string{
		"postgres":        {"connection pool exhausted", "query timeout", "deadlock detected"},
		"redis":           {"connection reset", "max clients reached"},
		"elasticsearch":   {"search timeout", "shard failure"},
		"stripe-api":      {"card_declined", "insufficient_funds", "expired_card"},
		"payment-service": {"payment failed", "fraud detected"},
	}
	if errs, ok := specific[svc]; ok && mrand.Float64() < 0.7 {
		return errs[mrand.Intn(len(errs))]
	}
	return errors[mrand.Intn(len(errors))]
}

func genBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
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

func hexStr(b []byte) string {
	return hex.EncodeToString(b)
}
