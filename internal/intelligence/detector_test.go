package intelligence

import (
	"testing"
	"time"
)

func TestToInt64(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected int64
	}{
		{"int64 value", int64(42), 42},
		{"float64 value", float64(42.5), 42},
		{"int value", int(42), 42},
		{"string value", "42", 0},
		{"nil value", nil, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := toInt64(tc.input)
			if result != tc.expected {
				t.Errorf("toInt64(%v) = %d, want %d", tc.input, result, tc.expected)
			}
		})
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected int
	}{
		{"int64 value", int64(42), 42},
		{"float64 value", float64(42.9), 42},
		{"int value", int(100), 100},
		{"negative", float64(-10.5), -10},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := toInt(tc.input)
			if result != tc.expected {
				t.Errorf("toInt(%v) = %d, want %d", tc.input, result, tc.expected)
			}
		})
	}
}

func TestCalculateHealthScore(t *testing.T) {
	d := &Detector{config: DetectorConfig{}}

	tests := []struct {
		name      string
		anomalies []Anomaly
		patterns  []Pattern
		minScore  float64
		maxScore  float64
	}{
		{
			name:      "no anomalies or patterns",
			anomalies: nil,
			patterns:  nil,
			minScore:  100,
			maxScore:  100,
		},
		{
			name: "critical anomaly (z >= 3.0)",
			anomalies: []Anomaly{
				{ZScore: 3.5},
			},
			patterns: nil,
			minScore: 85,
			maxScore: 85,
		},
		{
			name: "serious anomaly (z >= 2.5)",
			anomalies: []Anomaly{
				{ZScore: 2.7},
			},
			patterns: nil,
			minScore: 90,
			maxScore: 90,
		},
		{
			name: "moderate anomaly (z >= 2.0)",
			anomalies: []Anomaly{
				{ZScore: 2.2},
			},
			patterns: nil,
			minScore: 95,
			maxScore: 95,
		},
		{
			name: "negative z-score",
			anomalies: []Anomaly{
				{ZScore: -3.0},
			},
			patterns: nil,
			minScore: 85,
			maxScore: 85,
		},
		{
			name:      "error patterns",
			anomalies: nil,
			patterns: []Pattern{
				{Severity: "ERROR", Count: 15},
			},
			minScore: 95,
			maxScore: 95,
		},
		{
			name:      "warn patterns",
			anomalies: nil,
			patterns: []Pattern{
				{Severity: "WARN", Count: 25},
			},
			minScore: 98,
			maxScore: 98,
		},
		{
			name: "multiple anomalies floor at zero",
			anomalies: []Anomaly{
				{ZScore: 3.5}, {ZScore: 3.5}, {ZScore: 3.5},
				{ZScore: 3.5}, {ZScore: 3.5}, {ZScore: 3.5},
				{ZScore: 3.5}, {ZScore: 3.5},
			},
			patterns: nil,
			minScore: 0,
			maxScore: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			score := d.calculateHealthScore(tc.anomalies, tc.patterns)
			if score < tc.minScore || score > tc.maxScore {
				t.Errorf("calculateHealthScore() = %f, want between %f and %f",
					score, tc.minScore, tc.maxScore)
			}
		})
	}
}

func TestGenerateSummary(t *testing.T) {
	d := &Detector{config: DetectorConfig{}}

	tests := []struct {
		name        string
		healthScore float64
		anomalies   []Anomaly
		patterns    []Pattern
		contains    string
	}{
		{"healthy", 95, nil, nil, "healthy"},
		{"minor issues", 75, []Anomaly{{ZScore: 2.0}}, nil, "Minor issues"},
		{"degraded", 55, []Anomaly{{ZScore: 3.0}}, nil, "degraded"},
		{"critical", 30, []Anomaly{{ZScore: 3.5}}, nil, "Critical"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			summary := d.generateSummary(tc.healthScore, tc.anomalies, tc.patterns)
			if !contains(summary, tc.contains) {
				t.Errorf("generateSummary(%f, ...) = %q, want to contain %q",
					tc.healthScore, summary, tc.contains)
			}
		})
	}
}

func TestGenerateInsights(t *testing.T) {
	d := &Detector{config: DetectorConfig{}}

	tests := []struct {
		name          string
		anomalies     []Anomaly
		patterns      []Pattern
		expectedCount int
	}{
		{"no anomalies", nil, nil, 0},
		{
			"critical anomaly",
			[]Anomaly{{ZScore: 3.5, Description: "test"}},
			nil,
			1,
		},
		{
			"severe pattern",
			nil,
			[]Pattern{{Severity: "ERROR", Count: 25, ServiceName: "svc", Template: "err"}},
			1,
		},
		{
			"combined",
			[]Anomaly{{ZScore: 2.0, Description: "test"}},
			[]Pattern{{Severity: "ERROR", Count: 25, ServiceName: "svc", Template: "err"}},
			2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			insights := d.generateInsights(tc.anomalies, tc.patterns)
			if len(insights) != tc.expectedCount {
				t.Errorf("generateInsights() returned %d insights, want %d",
					len(insights), tc.expectedCount)
			}
		})
	}
}

func TestSafeRunCheckRecoversPanic(t *testing.T) {
	// A detector with nil duck will panic in runCheck when calling GenerateSnapshot
	d := &Detector{
		duck: nil,
		config: DetectorConfig{
			Enabled:        true,
			CheckInterval:  time.Minute,
			LookbackWindow: 5 * time.Minute,
		},
	}

	// safeRunCheck should recover from the panic without propagating it
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("safeRunCheck did not recover panic: %v", r)
		}
	}()

	d.safeRunCheck(t.Context())
}

func TestNewDetector(t *testing.T) {
	cfg := DetectorConfig{
		Enabled:        true,
		CheckInterval:  5 * time.Minute,
		LookbackWindow: 15 * time.Minute,
	}
	d := NewDetector(nil, cfg)

	if d == nil {
		t.Fatal("NewDetector returned nil")
		return
	}
	if d.config.Enabled != true {
		t.Error("config.Enabled not set correctly")
	}
	if d.config.CheckInterval != 5*time.Minute {
		t.Error("config.CheckInterval not set correctly")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && containsAt(s, substr)))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
