package engine

import (
	"context"
	"time"
)

// stallLimit is how long the read pool may go without completing a statement
// before it counts as wedged: three of the longest statements a read is
// allowed to run. A busy pool completes one well within it; a wedged one never
// does.
const stallLimit = 3 * maxQueryTimeoutMs * time.Millisecond

// noteProgress records that a read statement completed. Only success counts:
// a wedged engine still hands out free connections and returns every hung
// statement at its deadline, and an engine error may come from a broken
// engine, so neither shows that statements run.
func (d *Duck) noteProgress() { d.progress.Store(time.Now().UnixNano()) }

// Stalled reports whether the read pool has made no progress for stallLimit.
// An idle pool also makes none, so a caller confirms with Ping.
func (d *Duck) Stalled() bool {
	last := d.progress.Load()
	return last != 0 && time.Since(time.Unix(0, last)) > stallLimit
}

// Ping runs a trivial statement on the read pool, recording progress when it
// answers.
func (d *Duck) Ping(ctx context.Context) error {
	conn, err := d.acquireRead(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var one int
	if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	d.noteProgress()
	return nil
}
