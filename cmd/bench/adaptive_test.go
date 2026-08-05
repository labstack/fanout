package main

import (
	"math"
	"testing"
)

func TestAutoWorkersScalesWithCoresAndHasAFloor(t *testing.T) {
	// A single-core driver still needs enough in-flight senders to keep a
	// remote server busy: throughput here is round-trip bound, not CPU bound.
	if got := autoWorkers(1); got < 8 {
		t.Fatalf("autoWorkers(1) = %d, want at least 8", got)
	}
	if autoWorkers(8) <= autoWorkers(2) {
		t.Fatal("autoWorkers must grow with core count")
	}
	// Unbounded growth would just queue work in the driver and inflate the
	// latency it reports as the server's.
	if got := autoWorkers(256); got > maxAutoWorkers {
		t.Fatalf("autoWorkers(256) = %d, want <= %d", got, maxAutoWorkers)
	}
}

func TestDecideNextStepRampsWhileDeliveryHolds(t *testing.T) {
	p := defaultRampPolicy()
	history := []rampStep{{TargetRate: 1000, AchievedRate: 995, ExportP95Ms: 2}}

	got := decideNextStep(history, p)

	if got.Stop {
		t.Fatalf("should keep ramping while delivery holds, stopped: %s", got.Reason)
	}
	if want := 1000 * p.Growth; got.NextRate != want {
		t.Fatalf("NextRate = %v, want %v", got.NextRate, want)
	}
}

func TestDecideNextStepBisectsInsteadOfStoppingAtTheFirstMiss(t *testing.T) {
	p := defaultRampPolicy()
	// Asked for 8000, delivered 5000. Stopping here would report 4000 as the
	// capacity of a machine that might well sustain 6000 — on a doubling ramp
	// the first miss understates the answer by up to half.
	history := []rampStep{
		{TargetRate: 4000, AchievedRate: 3980, ExportP95Ms: 3},
		{TargetRate: 8000, AchievedRate: 5000, ExportP95Ms: 6},
	}

	got := decideNextStep(history, p)

	if got.Stop {
		t.Fatalf("should bisect the bracket, stopped: %s", got.Reason)
	}
	if got.NextRate <= 4000 || got.NextRate >= 8000 {
		t.Fatalf("NextRate = %v, want a probe strictly inside (4000, 8000)", got.NextRate)
	}
}

func TestDecideNextStepStopsOnceTheBracketIsTight(t *testing.T) {
	p := defaultRampPolicy()
	// 3800 held, 4000 did not: a 5% bracket. Refining further measures noise.
	history := []rampStep{
		{TargetRate: 3800, AchievedRate: 3790, ExportP95Ms: 3},
		{TargetRate: 4000, AchievedRate: 3000, ExportP95Ms: 6},
	}

	got := decideNextStep(history, p)

	if !got.Stop {
		t.Fatalf("must stop once converged, got next rate %v", got.NextRate)
	}
	if got.Reason == "" {
		t.Fatal("a stop must explain itself; the reason lands in the report")
	}
}

func TestDecideNextStepStopsWhenEvenTheOpeningRateMisses(t *testing.T) {
	p := defaultRampPolicy()
	// No lower bound exists, so there is nothing to bisect toward.
	history := []rampStep{{TargetRate: 500, AchievedRate: 100, ExportP95Ms: 40}}

	if got := decideNextStep(history, p); !got.Stop {
		t.Fatalf("must stop with no good lower bound, got %v", got.NextRate)
	}
}

func TestDecideNextStepStopsAtLatencyKnee(t *testing.T) {
	p := defaultRampPolicy()
	// Delivery is fine, but latency has blown out relative to the best step.
	// Capacity that only exists at 40x the latency is not capacity.
	history := []rampStep{
		{TargetRate: 1000, AchievedRate: 1000, ExportP95Ms: 2},
		{TargetRate: 2000, AchievedRate: 1990, ExportP95Ms: 80},
	}

	got := decideNextStep(history, p)

	if !got.Stop {
		t.Fatal("must stop at the latency knee even when delivery holds")
	}
}

func TestDecideNextStepStopsAtMaxSteps(t *testing.T) {
	p := defaultRampPolicy()
	history := make([]rampStep, p.MaxSteps)
	for i := range history {
		history[i] = rampStep{TargetRate: 1000, AchievedRate: 1000, ExportP95Ms: 1}
	}

	if got := decideNextStep(history, p); !got.Stop {
		t.Fatal("must stop at MaxSteps so a fast server cannot ramp forever")
	}
}

func TestDecideNextStepSeedsTheFirstStep(t *testing.T) {
	got := decideNextStep(nil, defaultRampPolicy())

	if got.Stop {
		t.Fatal("an empty history must produce a first step, not a stop")
	}
	if got.NextRate <= 0 {
		t.Fatalf("NextRate = %v, want positive", got.NextRate)
	}
}

func TestSustainableRateIgnoresStepsThatMissedTheirTarget(t *testing.T) {
	p := defaultRampPolicy()
	history := []rampStep{
		{TargetRate: 1000, AchievedRate: 1000, ExportP95Ms: 2},
		{TargetRate: 2000, AchievedRate: 1990, ExportP95Ms: 3},
		// Saturated: achieved is well past the last good step but the server
		// never accepted what was asked, so it is not a sustainable rate.
		{TargetRate: 4000, AchievedRate: 2600, ExportP95Ms: 90},
	}

	if got := sustainableRate(history, p); got != 1990 {
		t.Fatalf("sustainableRate = %v, want 1990 (highest step that held its target)", got)
	}
}

func TestSaturationRateIsTheBestObservedThroughput(t *testing.T) {
	history := []rampStep{
		{TargetRate: 1000, AchievedRate: 1000},
		{TargetRate: 4000, AchievedRate: 2600},
		{TargetRate: 8000, AchievedRate: 2550},
	}

	if got := saturationRate(history); got != 2600 {
		t.Fatalf("saturationRate = %v, want 2600", got)
	}
}

func TestSustainableRateFallsBackWhenNoStepHeldItsTarget(t *testing.T) {
	p := defaultRampPolicy()
	// Even the very first step missed. Reporting zero would imply the server
	// did nothing, when in fact it sustained 400/s.
	history := []rampStep{{TargetRate: 1000, AchievedRate: 400, ExportP95Ms: 50}}

	if got := sustainableRate(history, p); got != 400 {
		t.Fatalf("sustainableRate = %v, want the observed 400", got)
	}
}

func TestQueryKneeUsesUnloadedBaseline(t *testing.T) {
	p := defaultRampPolicy()
	// 60ms unloaded; 200ms under load is a normal cost of concurrency.
	if kneeExceeded(60, 200, p.LatencyKnee) {
		t.Fatal("a modest multiple of the baseline is not a knee")
	}
	// 60ms unloaded to 6s is queueing, not serving.
	if !kneeExceeded(60, 6000, p.LatencyKnee) {
		t.Fatal("a 100x baseline must register as a knee")
	}
}

func TestKneeIgnoresImmeasurablyFastBaselines(t *testing.T) {
	// A sub-millisecond baseline would make any real latency look like a knee.
	if kneeExceeded(0, 5, defaultRampPolicy().LatencyKnee) {
		t.Fatal("a zero baseline must not trigger a knee")
	}
}

func TestSeedRateIsPositiveForAnyCoreCount(t *testing.T) {
	for _, cores := range []int{0, 1, 2, 64} {
		if got := seedRate(cores); got <= 0 || math.IsNaN(got) {
			t.Fatalf("seedRate(%d) = %v, want positive", cores, got)
		}
	}
}
