package intelligence

import (
	"testing"
	"time"
)

func TestDefaultDetectorConfig(t *testing.T) {
	cfg := DefaultDetectorConfig()

	if !cfg.Enabled {
		t.Error("DefaultDetectorConfig() should be enabled by default")
	}
	if cfg.CheckInterval != 60*time.Second {
		t.Errorf("CheckInterval = %v, want 60s", cfg.CheckInterval)
	}
	if cfg.LookbackWindow != 15*time.Minute {
		t.Errorf("LookbackWindow = %v, want 15m", cfg.LookbackWindow)
	}
	if cfg.ErrorRateThreshold != 2.0 {
		t.Errorf("ErrorRateThreshold = %f, want 2.0", cfg.ErrorRateThreshold)
	}
	if cfg.LatencyThreshold != 2.0 {
		t.Errorf("LatencyThreshold = %f, want 2.0", cfg.LatencyThreshold)
	}
	if cfg.VolumeThreshold != 2.5 {
		t.Errorf("VolumeThreshold = %f, want 2.5", cfg.VolumeThreshold)
	}
}

func TestAnomalyTypes(t *testing.T) {
	tests := []struct {
		anomalyType AnomalyType
		expected    string
	}{
		{AnomalyErrorSpike, "error_spike"},
		{AnomalyLatencyDegradation, "latency_degradation"},
		{AnomalyVolumeChange, "volume_change"},
		{AnomalyErrorRateChange, "error_rate_change"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			if string(tc.anomalyType) != tc.expected {
				t.Errorf("AnomalyType = %q, want %q", tc.anomalyType, tc.expected)
			}
		})
	}
}

func TestAnomaly(t *testing.T) {
	now := time.Now()
	a := Anomaly{
		Type:        AnomalyErrorSpike,
		ServiceName: "api-gateway",
		Metric:      "error_rate",
		Current:     5.5,
		Baseline:    1.0,
		ZScore:      3.2,
		DetectedAt:  now,
		Description: "Error rate spike detected",
	}

	if a.Type != AnomalyErrorSpike {
		t.Errorf("Type = %q", a.Type)
	}
	if a.ServiceName != "api-gateway" {
		t.Errorf("ServiceName = %q", a.ServiceName)
	}
	if a.Metric != "error_rate" {
		t.Errorf("Metric = %q", a.Metric)
	}
	if a.Current != 5.5 {
		t.Errorf("Current = %f", a.Current)
	}
	if a.Baseline != 1.0 {
		t.Errorf("Baseline = %f", a.Baseline)
	}
	if a.ZScore != 3.2 {
		t.Errorf("ZScore = %f", a.ZScore)
	}
}

func TestPattern(t *testing.T) {
	now := time.Now()
	p := Pattern{
		Template:    "Connection refused to database",
		Count:       25,
		FirstSeen:   now.Add(-10 * time.Minute),
		LastSeen:    now,
		Severity:    "ERROR",
		ServiceName: "user-service",
		Summary:     "Database connectivity issues",
		Samples: []LogSample{
			{Timestamp: now, Message: "Connection refused to database at 10.0.0.5", TraceID: "trace-123"},
		},
	}

	if p.Template != "Connection refused to database" {
		t.Errorf("Template = %q", p.Template)
	}
	if p.Count != 25 {
		t.Errorf("Count = %d", p.Count)
	}
	if p.Severity != "ERROR" {
		t.Errorf("Severity = %q", p.Severity)
	}
	if len(p.Samples) != 1 {
		t.Errorf("Samples count = %d", len(p.Samples))
	}
}

func TestLogSample(t *testing.T) {
	now := time.Now()
	s := LogSample{
		Timestamp: now,
		Message:   "Failed to connect to redis",
		TraceID:   "abc123def456",
	}

	if s.Message != "Failed to connect to redis" {
		t.Errorf("Message = %q", s.Message)
	}
	if s.TraceID != "abc123def456" {
		t.Errorf("TraceID = %q", s.TraceID)
	}
}

func TestInsight(t *testing.T) {
	i := Insight{
		Type:     "anomaly",
		Severity: "critical",
		Message:  "Error rate increased by 500%",
	}

	if i.Type != "anomaly" {
		t.Errorf("Type = %q", i.Type)
	}
	if i.Severity != "critical" {
		t.Errorf("Severity = %q", i.Severity)
	}
	if i.Message == "" {
		t.Error("Message should not be empty")
	}
}

func TestIntelligenceSnapshot(t *testing.T) {
	now := time.Now()
	snap := IntelligenceSnapshot{
		GeneratedAt: now,
		Timeframe:   "last_15m",
		Insights: []Insight{
			{Type: "anomaly", Severity: "warning", Message: "test insight"},
		},
		Patterns: []Pattern{
			{Template: "test pattern", Count: 10},
		},
		Anomalies: []Anomaly{
			{Type: AnomalyErrorSpike, ZScore: 2.5},
		},
		Summary:     "System healthy",
		HealthScore: 95.0,
	}

	if snap.Timeframe != "last_15m" {
		t.Errorf("Timeframe = %q", snap.Timeframe)
	}
	if len(snap.Insights) != 1 {
		t.Errorf("Insights count = %d", len(snap.Insights))
	}
	if len(snap.Patterns) != 1 {
		t.Errorf("Patterns count = %d", len(snap.Patterns))
	}
	if len(snap.Anomalies) != 1 {
		t.Errorf("Anomalies count = %d", len(snap.Anomalies))
	}
	if snap.HealthScore != 95.0 {
		t.Errorf("HealthScore = %f", snap.HealthScore)
	}
}

func TestDetectorConfig(t *testing.T) {
	cfg := DetectorConfig{
		Enabled:            true,
		CheckInterval:      30 * time.Second,
		LookbackWindow:     10 * time.Minute,
		ErrorRateThreshold: 1.5,
		LatencyThreshold:   2.5,
		VolumeThreshold:    3.0,
	}

	if !cfg.Enabled {
		t.Error("Enabled should be true")
	}
	if cfg.CheckInterval != 30*time.Second {
		t.Errorf("CheckInterval = %v", cfg.CheckInterval)
	}
	if cfg.LookbackWindow != 10*time.Minute {
		t.Errorf("LookbackWindow = %v", cfg.LookbackWindow)
	}
	if cfg.ErrorRateThreshold != 1.5 {
		t.Errorf("ErrorRateThreshold = %f", cfg.ErrorRateThreshold)
	}
}
