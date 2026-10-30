package fanql

import (
	"context"
	"math"
	"slices"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/panel"
)

type budgetParser struct {
	panel.Parser
	parseCalls, renderCalls int
}

func (p *budgetParser) ParseSQL(ctx context.Context, text string) (map[string]any, error) {
	p.parseCalls++
	return p.Parser.ParseSQL(ctx, text)
}

func (p *budgetParser) RenderSQL(ctx context.Context, node map[string]any) (string, error) {
	p.renderCalls++
	return p.Parser.RenderSQL(ctx, node)
}

func latencyPercentile(samples []time.Duration, percentile float64) time.Duration {
	ordered := slices.Clone(samples)
	slices.Sort(ordered)
	return ordered[max(0, int(math.Ceil(float64(len(ordered))*percentile))-1)]
}

// Latency requirements: ../../docs/specs/2026-10-29-no-llm-ui-design.md,
// section 0 (especially 0.14 and 0.16). Tests bound work deterministically;
// benchmarks report the strict spec percentiles on an uninstrumented build.
func TestLatencyBudgets(t *testing.T) {
	if testing.Short() {
		t.Skip("latency acceptance is excluded by -short")
	}
	fixtures := readCorpus(t)
	if len(fixtures) == 0 {
		t.Fatal("latency needs the acceptance corpus")
	}
	engine, _ := newTestEngine(t)
	parser := &budgetParser{Parser: engine}
	var schemaFetches atomic.Int32
	warm := NewCompleter(completionSchemaFunc(func(context.Context, panel.SchemaRequest) (*panel.Schema, error) {
		schemaFetches.Add(1)
		return completionFixtureSchema(), nil
	}))
	// Freeze cache time so CI pauses cannot expire the warm schema.
	now := time.Now()
	warm.now = func() time.Time { return now }
	waitCompletion(t, warm, completionRequest("spans "), func(r CompleteResponse) bool { return r.Schema.Warm })
	registryOnly := NewCompleter(nil)
	var compileSamples, formatSamples, completeSamples, parseRankSamples []time.Duration
	maxParse, maxRender := 0, 0
	measure := func(name string, allocationLimit float64, samples *[]time.Duration, run func()) {
		t.Helper()
		// AllocsPerRun includes an untimed warmup before measuring steady state.
		allocs := testing.AllocsPerRun(5, run)
		if allocs > allocationLimit {
			t.Errorf("%s allocations/request = %.0f, want <= %.0f", name, allocs, allocationLimit)
		}
		if !raceInstrumentation {
			for range 3 {
				started := time.Now()
				run()
				*samples = append(*samples, time.Since(started))
			}
		}
	}
	// Rounded ceilings leave headroom above corpus maxima (~1050, 120, 215,
	// and 2300 allocations for Compile, completion, parse/rank, and Format).
	for _, fixture := range fixtures {
		measure("Compile", 1500, &compileSamples, func() {
			_, _ = Compile(fixture.Text, Options{Viz: fixture.Viz})
		})
		req := CompleteRequest{Text: fixture.Text, Cursor: utf8.RuneCountInString(fixture.Text), Window: "1h", Viz: fixture.Viz}
		measure("Complete warm", 200, &completeSamples, func() {
			if response := warm.Complete(t.Context(), req); !response.Schema.Warm {
				t.Fatal("warm-cache measurement lost its schema")
			}
		})
		measure("expectAt plus registry parse/rank", 350, &parseRankSamples, func() {
			_, _ = expectAt(req.Text, req.Cursor)
			_ = registryOnly.Complete(t.Context(), req)
		})
	}
	for _, fixture := range latencyFormatFixtures(t) {
		measure("Format "+fixture.Name, 3500, &formatSamples, func() {
			parser.parseCalls, parser.renderCalls = 0, 0
			formatted, ps, err := Format(t.Context(), parser, *fixture.Panel.Query, Options{Viz: fixture.Panel.Viz, Variables: fixture.Variables})
			if err != nil || len(ps) != 0 || formatted == nil {
				t.Fatalf("format %q: %+v %v", fixture.Name, ps, err)
			}
			maxParse, maxRender = max(maxParse, parser.parseCalls), max(maxRender, parser.renderCalls)
		})
	}
	if maxParse > 2 || maxRender > 2 {
		t.Errorf("Format engine calls/request: ParseSQL max=%d, RenderSQL max=%d; want <= 2 each", maxParse, maxRender)
	}
	if got := schemaFetches.Load(); got != 1 {
		t.Errorf("warm completion schema fetches = %d, want exactly 1 including warmup", got)
	}
	t.Run("smoke", func(t *testing.T) {
		if raceInstrumentation {
			t.Skip("wall-clock smoke budgets exclude race instrumentation; deterministic work bounds still run")
		}
		for _, budget := range []struct {
			name       string
			samples    []time.Duration
			percentile float64
			limit      time.Duration
		}{
			// Ten times the spec budgets, to catch only gross regressions in CI.
			{"Compile", compileSamples, .99, 10 * time.Millisecond},
			{"Format", formatSamples, .99, 200 * time.Millisecond},
			{"Complete warm", completeSamples, .95, 300 * time.Millisecond},
			{"expectAt plus registry parse/rank", parseRankSamples, .95, 50 * time.Millisecond},
		} {
			if len(budget.samples) == 0 {
				t.Fatalf("no %s samples", budget.name)
			}
			got := latencyPercentile(budget.samples, budget.percentile)
			if got >= budget.limit {
				t.Errorf("%s p%.0f=%s exceeds smoke limit <%s", budget.name, budget.percentile*100, got, budget.limit)
			}
		}
	})
}

func latencyFormatFixtures(t testing.TB) []storedQueryFixture {
	t.Helper()
	var queries []storedQueryFixture
	for _, fixture := range storedQueryFixtures(t) {
		q := fixture.Panel.Query
		if q == nil || len(q.Where) > 16 {
			continue
		}
		if problems, _ := panel.QueryShape(q, fixture.Panel.Viz, nil); len(problems) != 0 {
			t.Fatalf("stored fixture %q is not shape-valid: %+v", fixture.Name, problems)
		}
		queries = append(queries, fixture)
	}
	q := generatedStructuredQuery([]byte{0}) // 16 complete stored entries.
	return append(queries, storedQueryFixture{Name: "16 entries", Panel: panel.Panel{Query: &q}})
}

func benchmarkLatency(b *testing.B, percentile float64, metric string, run func(int)) {
	b.Helper()
	if raceInstrumentation {
		b.Skip("spec latency benchmarks require an uninstrumented build")
	}
	samples := make([]time.Duration, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		started := time.Now()
		run(i)
		samples[i] = time.Since(started)
	}
	b.StopTimer()
	b.ReportMetric(float64(latencyPercentile(samples, percentile)), metric)
}

// BenchmarkCompile measures the section 0 spec budget: Compile p99 < 1 ms.
func BenchmarkCompile(b *testing.B) {
	fixtures := readCorpus(b)
	for _, fixture := range fixtures {
		_, _ = Compile(fixture.Text, Options{Viz: fixture.Viz})
	}
	benchmarkLatency(b, .99, "p99-ns/op", func(i int) {
		fixture := fixtures[i%len(fixtures)]
		_, _ = Compile(fixture.Text, Options{Viz: fixture.Viz})
	})
}

// BenchmarkFormat measures the section 0 spec budget: Format p99 < 20 ms for up to 16 entries.
func BenchmarkFormat(b *testing.B) {
	engine, _ := newTestEngine(b)
	queries := latencyFormatFixtures(b)
	format := func(i int) {
		fixture := queries[i%len(queries)]
		if f, ps, err := Format(b.Context(), engine, *fixture.Panel.Query, Options{Viz: fixture.Panel.Viz, Variables: fixture.Variables}); err != nil || len(ps) != 0 || f == nil {
			b.Fatal(ps, err)
		}
	}
	// Prime both Format and the engine before measuring steady-state requests.
	for i := range queries {
		format(i)
	}
	benchmarkLatency(b, .99, "p99-ns/op", format)
}

// BenchmarkComplete measures the section 0 spec budget: warm completion p95 < 30 ms.
func BenchmarkComplete(b *testing.B) {
	fixtures := readCorpus(b)
	c := NewCompleter(nil)
	now := time.Now()
	c.now = func() time.Time { return now }
	c.cache[completionKey{window: "1h"}] = completionSchemaEntry{schema: completionFixtureSchema(), expires: now.Add(time.Hour)}
	complete := func(i int) {
		fixture := fixtures[i%len(fixtures)]
		_ = c.Complete(b.Context(), CompleteRequest{Text: fixture.Text, Cursor: utf8.RuneCountInString(fixture.Text), Window: "1h", Viz: fixture.Viz})
	}
	for i := range fixtures {
		complete(i)
	}
	benchmarkLatency(b, .95, "p95-ns/op", complete)
}

// BenchmarkParseRank measures the section 0 spec budget: completion parse/rank p95 < 5 ms.
func BenchmarkParseRank(b *testing.B) {
	fixtures := readCorpus(b)
	c := NewCompleter(nil)
	parseRank := func(i int) {
		fixture := fixtures[i%len(fixtures)]
		req := CompleteRequest{Text: fixture.Text, Cursor: utf8.RuneCountInString(fixture.Text), Window: "1h", Viz: fixture.Viz}
		_, _ = expectAt(req.Text, req.Cursor)
		_ = c.Complete(b.Context(), req)
	}
	for i := range fixtures {
		parseRank(i)
	}
	benchmarkLatency(b, .95, "p95-ns/op", parseRank)
}
