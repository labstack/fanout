package service

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestIncidentTracker_NoIncidentWhenHealthy(t *testing.T) {
	tracker := NewIncidentTracker()

	tracker.Tick("svc-a", "healthy", 0.001, 100, t0)
	tracker.Tick("svc-a", "healthy", 0.001, 100, t0.Add(time.Minute))

	incidents := tracker.Incidents()
	if len(incidents) != 0 {
		t.Errorf("expected 0 incidents, got %d", len(incidents))
	}
}

func TestIncidentTracker_OpensAfterTwoTicks(t *testing.T) {
	tracker := NewIncidentTracker()

	// First degraded tick — no incident yet
	tracker.Tick("svc-a", "degraded", 0.05, 2000, t0)
	incidents := tracker.Incidents()
	if len(incidents) != 0 {
		t.Errorf("after 1 degraded tick: expected 0 incidents, got %d", len(incidents))
	}

	// Second degraded tick — incident opens
	tracker.Tick("svc-a", "degraded", 0.05, 2000, t0.Add(time.Minute))
	incidents = tracker.Incidents()
	if len(incidents) != 1 {
		t.Fatalf("after 2 degraded ticks: expected 1 incident, got %d", len(incidents))
	}
	if incidents[0].Lifecycle != "open" {
		t.Errorf("expected lifecycle %q, got %q", "open", incidents[0].Lifecycle)
	}
	if incidents[0].Service != "svc-a" {
		t.Errorf("expected service %q, got %q", "svc-a", incidents[0].Service)
	}
}

func TestIncidentTracker_CoolingAndClear(t *testing.T) {
	tracker := NewIncidentTracker()

	// Open an incident
	tracker.Tick("svc-a", "degraded", 0.1, 3000, t0)
	tracker.Tick("svc-a", "degraded", 0.1, 3000, t0.Add(time.Minute))

	incidents := tracker.Incidents()
	if len(incidents) != 1 || incidents[0].Lifecycle != "open" {
		t.Fatalf("expected open incident, got %v", incidents)
	}

	// First healthy tick — transitions to cooling
	tracker.Tick("svc-a", "healthy", 0.001, 100, t0.Add(2*time.Minute))
	incidents = tracker.Incidents()
	if len(incidents) != 1 {
		t.Fatalf("after 1st healthy tick: expected 1 incident (cooling), got %d", len(incidents))
	}
	if incidents[0].Lifecycle != "cooling" {
		t.Errorf("after 1st healthy tick: expected lifecycle %q, got %q", "cooling", incidents[0].Lifecycle)
	}

	// Ticks 2–4 of healthy — still cooling
	for i := 2; i <= 4; i++ {
		tracker.Tick("svc-a", "healthy", 0.001, 100, t0.Add(time.Duration(i+1)*time.Minute))
		incidents = tracker.Incidents()
		if len(incidents) != 1 || incidents[0].Lifecycle != "cooling" {
			t.Errorf("tick %d: expected 1 cooling incident, got %v", i, incidents)
		}
	}

	// 5th healthy tick — incident clears
	tracker.Tick("svc-a", "healthy", 0.001, 100, t0.Add(6*time.Minute))
	incidents = tracker.Incidents()
	if len(incidents) != 0 {
		t.Errorf("after 5 healthy ticks: expected 0 incidents, got %d", len(incidents))
	}
}

func TestIncidentTracker_CoolingReOpens(t *testing.T) {
	tracker := NewIncidentTracker()

	// Open an incident
	tracker.Tick("svc-a", "unhealthy", 0.2, 5000, t0)
	tracker.Tick("svc-a", "unhealthy", 0.2, 5000, t0.Add(time.Minute))

	// One healthy tick — cooling
	tracker.Tick("svc-a", "healthy", 0.001, 100, t0.Add(2*time.Minute))
	incidents := tracker.Incidents()
	if len(incidents) != 1 || incidents[0].Lifecycle != "cooling" {
		t.Fatalf("expected cooling incident, got %v", incidents)
	}

	// Re-degrades — back to open
	tracker.Tick("svc-a", "degraded", 0.08, 2500, t0.Add(3*time.Minute))
	incidents = tracker.Incidents()
	if len(incidents) != 1 {
		t.Fatalf("after re-degrade: expected 1 incident, got %d", len(incidents))
	}
	if incidents[0].Lifecycle != "open" {
		t.Errorf("after re-degrade: expected lifecycle %q, got %q", "open", incidents[0].Lifecycle)
	}
}

func TestIncidentTracker_TracksPeaks(t *testing.T) {
	tracker := NewIncidentTracker()

	// Two ticks to open (lower values)
	tracker.Tick("svc-a", "degraded", 0.05, 1000, t0)
	tracker.Tick("svc-a", "degraded", 0.08, 1500, t0.Add(time.Minute))

	// Third tick with higher values — peaks should update
	tracker.Tick("svc-a", "unhealthy", 0.30, 9000, t0.Add(2*time.Minute))

	incidents := tracker.Incidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}

	inc := incidents[0]
	if inc.PeakErrorRate != 0.30 {
		t.Errorf("PeakErrorRate = %v, want 0.30", inc.PeakErrorRate)
	}
	if inc.PeakP95Ms != 9000 {
		t.Errorf("PeakP95Ms = %v, want 9000", inc.PeakP95Ms)
	}
	if inc.Health != "unhealthy" {
		t.Errorf("Health = %q, want %q", inc.Health, "unhealthy")
	}
}
