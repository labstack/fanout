package dashboard

import (
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestDefaultSpecPassesTheFullCheck(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DuckDBMemory: "256MB", DuckDBThreads: 2, DuckDBMaxConns: 2, RollupInterval: time.Hour}
	repo, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := query.NewDuck(t.Context(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(); _ = repo.Close() })
	spec := DefaultSpec()
	if err := panel.NewExecutor(d, 30).Validate(t.Context(), &spec); err != nil {
		t.Fatal(err)
	}
}
