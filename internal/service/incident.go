package service

import (
	"sync"
	"time"
)

// Incident tracks the lifecycle of a service health issue.
type Incident struct {
	Service       string
	Health        string
	Lifecycle     string // "open" or "cooling"
	StartedAt     time.Time
	PeakErrorRate float64
	PeakP95Ms     float64
}

type incidentState struct {
	lifecycle      string // "", "open", "cooling"
	startedAt      time.Time
	firstPendingAt time.Time
	peakErrRate    float64
	peakP95        float64
	pendingTicks   int
	coolingTicks   int
	lastHealth     string
}

const (
	openThreshold  = 2
	clearThreshold = 5
)

// IncidentTracker manages incident state for all services.
type IncidentTracker struct {
	mu     sync.Mutex
	states map[string]*incidentState
}

func NewIncidentTracker() *IncidentTracker {
	return &IncidentTracker{states: make(map[string]*incidentState)}
}

func (t *IncidentTracker) Tick(service, health string, errorRate, p95Ms float64, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s, ok := t.states[service]
	if !ok {
		s = &incidentState{}
		t.states[service] = s
	}

	bad := health == "degraded" || health == "unhealthy"

	switch s.lifecycle {
	case "":
		if bad {
			if s.pendingTicks == 0 {
				s.firstPendingAt = now
			}
			s.pendingTicks++
			if s.pendingTicks >= openThreshold {
				s.lifecycle = "open"
				s.startedAt = s.firstPendingAt
				s.peakErrRate = errorRate
				s.peakP95 = p95Ms
				s.pendingTicks = 0
			}
		} else {
			s.pendingTicks = 0
		}

	case "open":
		if bad {
			if errorRate > s.peakErrRate {
				s.peakErrRate = errorRate
			}
			if p95Ms > s.peakP95 {
				s.peakP95 = p95Ms
			}
		} else {
			s.lifecycle = "cooling"
			s.coolingTicks = 1
		}

	case "cooling":
		if bad {
			s.lifecycle = "open"
			s.coolingTicks = 0
			if errorRate > s.peakErrRate {
				s.peakErrRate = errorRate
			}
			if p95Ms > s.peakP95 {
				s.peakP95 = p95Ms
			}
		} else {
			s.coolingTicks++
			if s.coolingTicks >= clearThreshold {
				s.lifecycle = ""
				s.pendingTicks = 0
				s.coolingTicks = 0
				s.peakErrRate = 0
				s.peakP95 = 0
			}
		}
	}

	s.lastHealth = health
}

func (t *IncidentTracker) Incidents() []Incident {
	t.mu.Lock()
	defer t.mu.Unlock()

	var out []Incident
	for svc, s := range t.states {
		if s.lifecycle == "open" || s.lifecycle == "cooling" {
			out = append(out, Incident{
				Service:       svc,
				Health:        s.lastHealth,
				Lifecycle:     s.lifecycle,
				StartedAt:     s.startedAt,
				PeakErrorRate: s.peakErrRate,
				PeakP95Ms:     s.peakP95,
			})
		}
	}
	return out
}
