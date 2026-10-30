package fanql

import (
	"context"
	"math"
	"os/exec"
	"slices"
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

func TestLatencyBudgets(t *testing.T) {
	if testing.Short() {
		t.Skip("latency acceptance is excluded by -short")
	}
	if raceInstrumentation {
		// Race instrumentation changes CPU cost by an order of magnitude. Keep
		// the production budgets unchanged and actually assert them in a plain
		// child build; all functional tests still run under the race detector.
		// The pinned engine's GOFLAGS/CGO environment comes from the parent.
		cmd := exec.CommandContext(t.Context(), "go", "test", "-race=false", "-run", "^TestLatencyBudgets$", "-count=1", "-v", ".")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("production latency under race runner: %v\n%s", err, out)
		}
		t.Logf("production latency (uninstrumented):\n%s", out)
		return
	}
	fixtures := readCorpus(t)
	if len(fixtures) == 0 {
		t.Fatal("latency needs the acceptance corpus")
	}
	engine, _ := newTestEngine(t)
	parser := &budgetParser{Parser: engine}
	warm := warmCompleter(t)
	registryOnly := NewCompleter(nil)
	var compileSamples, formatSamples, completeSamples, parseRankSamples []time.Duration
	maxParse, maxRender := 0, 0
	// Prime code and data before measuring steady-state editing work.
	for _, fixture := range fixtures {
		_, _ = Compile(fixture.Text, Options{Viz: fixture.Viz})
	}
	for range 3 {
		for _, fixture := range fixtures {
			started := time.Now()
			_, _ = Compile(fixture.Text, Options{Viz: fixture.Viz})
			compileSamples = append(compileSamples, time.Since(started))
			req := CompleteRequest{Text: fixture.Text, Cursor: utf8.RuneCountInString(fixture.Text), Window: "1h", Viz: fixture.Viz}
			started = time.Now()
			response := warm.Complete(t.Context(), req)
			completeSamples = append(completeSamples, time.Since(started))
			if !response.Schema.Warm {
				t.Fatal("warm-cache measurement lost its schema")
			}
			started = time.Now()
			_, _ = expectAt(req.Text, req.Cursor)
			_ = registryOnly.Complete(t.Context(), req)
			parseRankSamples = append(parseRankSamples, time.Since(started))
		}
	}
	stored := storedQueryFixtures(t)
	for range 3 {
		for _, fixture := range stored {
			q := fixture.Panel.Query
			if q == nil || len(q.Where) > 16 {
				continue
			}
			if problems, _ := panel.QueryShape(q, fixture.Panel.Viz, nil); len(problems) != 0 {
				t.Fatalf("stored fixture %q is not shape-valid: %+v", fixture.Name, problems)
			}
			parser.parseCalls, parser.renderCalls = 0, 0
			started := time.Now()
			formatted, ps, err := Format(t.Context(), parser, *q, FormatOptions{Viz: fixture.Panel.Viz, Variables: fixture.Variables})
			formatSamples = append(formatSamples, time.Since(started))
			if err != nil || len(ps) != 0 || formatted == nil {
				t.Fatalf("format %q: %+v %v", fixture.Name, ps, err)
			}
			maxParse, maxRender = max(maxParse, parser.parseCalls), max(maxRender, parser.renderCalls)
		}
		q := generatedStructuredQuery([]byte{0}) // 16 complete stored entries.
		parser.parseCalls, parser.renderCalls = 0, 0
		started := time.Now()
		formatted, ps, err := Format(t.Context(), parser, q, FormatOptions{})
		formatSamples = append(formatSamples, time.Since(started))
		if err != nil || len(ps) != 0 || formatted == nil {
			t.Fatalf("format 16 entries: %+v %v", ps, err)
		}
		maxParse, maxRender = max(maxParse, parser.parseCalls), max(maxRender, parser.renderCalls)
	}
	for _, budget := range []struct {
		name       string
		samples    []time.Duration
		percentile float64
		limit      time.Duration
	}{
		{"Compile", compileSamples, .99, time.Millisecond},
		{"Format", formatSamples, .99, 20 * time.Millisecond},
		{"Complete warm", completeSamples, .95, 30 * time.Millisecond},
		{"expectAt plus registry parse/rank", parseRankSamples, .95, 5 * time.Millisecond},
	} {
		if len(budget.samples) == 0 {
			t.Fatalf("no %s samples", budget.name)
		}
		got := latencyPercentile(budget.samples, budget.percentile)
		t.Logf("%s p%.0f=%s, budget <%s, samples=%d", budget.name, budget.percentile*100, got, budget.limit, len(budget.samples))
		if got >= budget.limit {
			t.Errorf("%s p%.0f=%s exceeds <%s", budget.name, budget.percentile*100, got, budget.limit)
		}
	}
	t.Logf("Format engine calls per request: ParseSQL max=%d, RenderSQL max=%d", maxParse, maxRender)
}

func BenchmarkCompile(b *testing.B) {
	fixtures := readCorpus(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fixture := fixtures[i%len(fixtures)]
		_, _ = Compile(fixture.Text, Options{Viz: fixture.Viz})
	}
}

func BenchmarkFormat(b *testing.B) {
	engine, _ := newTestEngine(b)
	fixtures := storedQueryFixtures(b)
	queries := make([]storedQueryFixture, 0, len(fixtures))
	for _, fixture := range fixtures {
		if fixture.Panel.Query != nil {
			queries = append(queries, fixture)
		}
	}
	if len(queries) == 0 {
		b.Fatal("no stored query fixtures")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fixture := queries[i%len(queries)]
		if _, ps, err := Format(b.Context(), engine, *fixture.Panel.Query, FormatOptions{Viz: fixture.Panel.Viz, Variables: fixture.Variables}); err != nil || len(ps) != 0 {
			b.Fatal(ps, err)
		}
	}
}

func BenchmarkComplete(b *testing.B) {
	fixtures := readCorpus(b)
	c := NewCompleter(nil)
	c.cache[completionKey{window: "1h"}] = completionSchemaEntry{schema: completionFixtureSchema(), expires: time.Now().Add(time.Hour)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fixture := fixtures[i%len(fixtures)]
		_ = c.Complete(b.Context(), CompleteRequest{Text: fixture.Text, Cursor: utf8.RuneCountInString(fixture.Text), Window: "1h", Viz: fixture.Viz})
	}
}
