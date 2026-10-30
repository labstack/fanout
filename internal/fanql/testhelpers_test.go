package fanql

import (
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func newTestEngine(t testing.TB) (*query.Duck, *telemetrystore.Repository) {
	t.Helper()
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 4, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	return d, repo
}

// Parse returns no query whenever a problem is present. source is empty for a
// whole query, or the page-supplied source for a search-bar filter.
func parse(text string, source string) (*queryTree, panel.Problems) {
	return parseWith(text, source, currentVocabulary())
}

// Resolve annotates fields and values in place, retaining entry boundaries.
func resolve(q *queryTree, opts Options) (*resolved, panel.Problems) {
	return resolveWith(q, opts, currentVocabulary())
}
