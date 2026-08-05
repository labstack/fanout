package main

import "runtime"

// numCPU is the driver's usable parallelism. GOMAXPROCS rather than NumCPU so
// a container CPU limit is respected: sizing to the host's cores inside a
// two-core cgroup would oversubscribe the driver and inflate its own latency.
func numCPU() int { return runtime.GOMAXPROCS(0) }

// Adaptive ramping. The harness discovers what a machine sustains instead of
// being told a rate that was calibrated on different hardware.
//
// The shape is deliberately simple: offer a rate, measure what comes back,
// multiply, repeat until the server stops keeping up or latency turns the
// corner. Everything here is a pure function of the steps already run, so the
// policy can be tested without a server.

// maxAutoWorkers bounds self-sizing. Past this the driver is mostly queueing
// work inside itself, and reporting that queue delay as the server's latency
// would be measuring the wrong process.
const maxAutoWorkers = 256

// rampStep is one completed fixed-rate trial.
type rampStep struct {
	TargetRate   float64 `json:"target_traces_per_sec"`
	AchievedRate float64 `json:"achieved_traces_per_sec"`
	ExportP95Ms  float64 `json:"export_p95_ms"`
	QueryP95Ms   float64 `json:"query_p95_ms,omitempty"`
}

// held reports whether the server actually accepted what this step offered.
func (s rampStep) held(floor float64) bool {
	return s.TargetRate > 0 && s.AchievedRate >= s.TargetRate*floor
}

// rampPolicy is the ramp's stopping rule.
type rampPolicy struct {
	// Growth multiplies the target after a step the server kept up with.
	Growth float64
	// MaxSteps bounds the ramp so a fast server cannot run indefinitely.
	MaxSteps int
	// DeliveryFloor is the fraction of the offered rate that must actually
	// arrive for the step to count as sustained.
	DeliveryFloor float64
	// LatencyKnee is the multiple of the best observed latency past which
	// throughput is being bought with unacceptable delay.
	LatencyKnee float64
	// RefineTolerance stops the bisection once the bracket is this tight,
	// relative to its upper bound. Chasing precision past this measures noise.
	RefineTolerance float64
}

// bracket returns the highest offered rate the server sustained without
// crossing the latency knee, and the lowest rate that failed either test. A
// zero upper bound means nothing has missed yet.
func bracket(history []rampStep, p rampPolicy) (lastGood, firstBad float64) {
	best := bestExportP95(history)
	for _, s := range history {
		if s.held(p.DeliveryFloor) && !kneeExceeded(best, s.ExportP95Ms, p.LatencyKnee) {
			if s.TargetRate > lastGood {
				lastGood = s.TargetRate
			}
			continue
		}
		if firstBad == 0 || s.TargetRate < firstBad {
			firstBad = s.TargetRate
		}
	}
	return lastGood, firstBad
}

func defaultRampPolicy() rampPolicy {
	return rampPolicy{
		Growth:   2,
		MaxSteps: 8,
		// 0.95, not 0.90. A step that scrapes past a looser floor gets reported
		// as sustainable and then fails to reproduce in the confirmation pass:
		// measured on two dedicated cores, 90.8% delivery was accepted as
		// sustainable at 7267/s and the confirmation managed 6503/s. Requiring
		// near-complete delivery makes the quoted rate one the machine repeats.
		DeliveryFloor:   0.95,
		LatencyKnee:     10,
		RefineTolerance: 0.15,
	}
}

type rampDecision struct {
	NextRate float64
	Stop     bool
	Reason   string
}

// decideNextStep chooses the next offered rate, or stops the ramp.
func decideNextStep(history []rampStep, p rampPolicy) rampDecision {
	if len(history) == 0 {
		return rampDecision{NextRate: seedRate(numCPU())}
	}
	if len(history) >= p.MaxSteps {
		return rampDecision{Stop: true, Reason: "reached the step limit"}
	}

	last := history[len(history)-1]
	lastGood, firstBad := bracket(history, p)

	// Nothing has missed yet: keep doubling to find the upper bound quickly.
	if firstBad == 0 {
		return rampDecision{NextRate: last.TargetRate * p.Growth}
	}
	// The very first offer already missed. There is no lower bound to bisect
	// toward, and halving indefinitely would just measure a smaller machine.
	if lastGood == 0 {
		return rampDecision{Stop: true, Reason: "server did not keep up even with the opening rate"}
	}
	// Bracketed. Bisect rather than stopping at the first miss: a single noisy
	// step would otherwise end the ramp and report the last doubling, which on
	// a geometric ramp understates capacity by up to half.
	if (firstBad-lastGood)/firstBad <= p.RefineTolerance {
		return rampDecision{Stop: true, Reason: "converged on the sustainable rate"}
	}
	return rampDecision{NextRate: (lastGood + firstBad) / 2}
}

// bestExportP95 is the lowest export p95 seen, i.e. the machine's behaviour
// when it is not under strain. Later steps are judged against it.
func bestExportP95(history []rampStep) float64 {
	best := 0.0
	for _, s := range history {
		if s.ExportP95Ms > 0 && (best == 0 || s.ExportP95Ms < best) {
			best = s.ExportP95Ms
		}
	}
	return best
}

// kneeExceeded compares a loaded latency against an unloaded baseline. A
// baseline at or below zero is unmeasurable, and dividing by it would call
// every subsequent step a knee.
func kneeExceeded(baselineMs, loadedMs, knee float64) bool {
	if baselineMs <= 0 {
		return false
	}
	return loadedMs > baselineMs*knee
}

// sustainableRate is the highest throughput the server both accepted and
// served without passing the latency knee.
func sustainableRate(history []rampStep, p rampPolicy) float64 {
	best := bestExportP95(history)
	sustained := 0.0
	for _, s := range history {
		if !s.held(p.DeliveryFloor) || kneeExceeded(best, s.ExportP95Ms, p.LatencyKnee) {
			continue
		}
		if s.AchievedRate > sustained {
			sustained = s.AchievedRate
		}
	}
	if sustained == 0 {
		// Nothing held its target. The machine still did something, and
		// reporting zero would misdescribe it as having done nothing.
		return saturationRate(history)
	}
	return sustained
}

// saturationRate is the most throughput observed at any offered rate,
// regardless of whether the server kept up or what it cost in latency.
func saturationRate(history []rampStep) float64 {
	peak := 0.0
	for _, s := range history {
		if s.AchievedRate > peak {
			peak = s.AchievedRate
		}
	}
	return peak
}

// autoWorkers sizes in-flight senders from the driver's cores. Sending is
// round-trip bound rather than CPU bound, so this deliberately oversubscribes:
// one sender per core would leave a remote server mostly idle.
func autoWorkers(cores int) int {
	if cores < 1 {
		cores = 1
	}
	workers := cores * 16
	if workers < 8 {
		workers = 8
	}
	if workers > maxAutoWorkers {
		workers = maxAutoWorkers
	}
	return workers
}

// seedRate is the ramp's first offer. Low enough that even a small machine
// clears it, so the first step establishes the unloaded latency baseline that
// every later knee check is measured against.
func seedRate(cores int) float64 {
	if cores < 1 {
		cores = 1
	}
	return float64(cores) * 250
}
