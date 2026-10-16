package query

import (
	"context"
	"log/slog"
	"time"

	"github.com/labstack/fanout/internal/metrics"
)

const duckDBMemorySampleInterval = 5 * time.Second

// startMemorySampler keeps engine calls off the scrape path. There is only one
// sampling goroutine, even if DuckDB ignores cancellation inside a statement.
// Its lifetime belongs to Duck, rather than the context used to open it.
func (d *Duck) startMemorySampler(interval time.Duration, sample func(context.Context) (map[string]int64, error)) {
	ctx, cancel := context.WithCancel(context.Background())
	d.stopMemorySampler = cancel
	d.memorySamplerDone = make(chan struct{})
	record, release := metrics.SetDuckDBMemorySource()
	d.releaseMemoryGauges = release
	go func() {
		defer close(d.memorySamplerDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		failures := 0
		for {
			if ctx.Err() != nil {
				return
			}
			readCtx, readCancel := context.WithTimeout(ctx, duckDBMemorySampleInterval)
			tags, err := sample(readCtx)
			if err == nil {
				err = readCtx.Err()
			}
			readCancel()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				failures++
				if failures == 3 {
					slog.Warn("DuckDB memory sampling failed repeatedly; retaining last successful sample", "err", err)
				}
			} else {
				record(tags, time.Now())
				failures = 0
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (d *Duck) sampleMemory(ctx context.Context) (map[string]int64, error) {
	rows, err := d.DB.QueryContext(ctx, "SELECT tag, sum(memory_usage_bytes) FROM duckdb_memory() GROUP BY tag")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := make(map[string]int64, 16)
	for rows.Next() {
		var tag string
		var bytes int64
		if err := rows.Scan(&tag, &bytes); err != nil {
			return nil, err
		}
		tags[tag] = bytes
	}
	// Publish only complete samples: a partial sum misstates untracked memory.
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tags, nil
}
