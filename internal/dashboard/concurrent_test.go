package dashboard

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/db/generated"
	"github.com/labstack/fanout/internal/panel"
	appstore "github.com/labstack/fanout/internal/store"
	"modernc.org/sqlite"
)

// Hold the first writer after SQLite has committed, before database/sql's
// Commit returns. A second connection can publish v3 in this window. This
// makes a post-commit get reliably return the wrong save without a production
// hook or a scheduler-dependent delay.
type commitBarrier struct {
	armed              atomic.Bool
	committed, release chan struct{}
}

type commitBarrierDriver struct {
	driver.Driver
	barrier *commitBarrier
}

func (d commitBarrierDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return commitBarrierConn{Conn: conn, barrier: d.barrier}, nil
}

type commitBarrierConn struct {
	driver.Conn
	barrier *commitBarrier
}

func (c commitBarrierConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c commitBarrierConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	conn, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, errors.New("sqlite driver connection does not implement driver.ConnBeginTx")
	}
	tx, err := conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return commitBarrierTx{Tx: tx, barrier: c.barrier, ctx: ctx}, nil
}

type commitBarrierTx struct {
	driver.Tx
	barrier *commitBarrier
	ctx     context.Context
}

func (tx commitBarrierTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if tx.barrier.armed.CompareAndSwap(true, false) {
		close(tx.barrier.committed)
		select {
		case <-tx.barrier.release:
		case <-tx.ctx.Done():
			return tx.ctx.Err()
		}
	}
	return nil
}

var barrierDriverID atomic.Uint64

func TestConcurrentCommitReturnsItsOwnSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sqlite")
	database, err := appstore.NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.DB.Exec(`INSERT INTO users(id,email) VALUES ('owner','owner@example.test')`); err != nil {
		t.Fatal(err)
	}
	s := New(database.DB, structural{})
	created, err := s.Create(t.Context(), "owner", concurrentSpec(2), agent)
	if err != nil {
		t.Fatal(err)
	}
	barrier := &commitBarrier{committed: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	name := fmt.Sprintf("dashboard_commit_barrier_%d", barrierDriverID.Add(1))
	sql.Register(name, commitBarrierDriver{Driver: &sqlite.Driver{}, barrier: barrier})
	db, err := sql.Open(name, path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release.Do(func() { close(barrier.release) }); _ = db.Close() }()
	db.SetMaxOpenConns(8)
	s = New(db, structural{})
	barrier.armed.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type answer struct {
		mutation Mutation
		err      error
	}
	done := make(chan answer, 1)
	go func() {
		m, e := s.EditWithChanges(ctx, "owner", created.ID, []Operation{{Op: "update_panel", ID: "p0", Set: map[string]any{"content": "first"}}}, 1, agent, "")
		done <- answer{m, e}
	}()
	select {
	case <-barrier.committed:
	case a := <-done:
		t.Fatalf("first writer returned before the commit barrier: %v", a.err)
	case <-ctx.Done():
		t.Fatal("first writer did not commit")
	}
	second, err := s.EditWithChanges(ctx, "owner", created.ID, []Operation{{Op: "update_panel", ID: "p1", Set: map[string]any{"content": "second"}}}, 2, agent, "")
	release.Do(func() { close(barrier.release) })
	if err != nil {
		t.Fatal(err)
	}
	first := <-done
	if first.err != nil {
		t.Fatal(first.err)
	}
	for i, m := range []Mutation{first.mutation, second} {
		if m.Record.Version != i+2 || m.BaseVersion != i+1 || snapshotJSON(m.Record.Spec) != snapshotJSON(storedVersionSpec(t, s, created.ID, m.Record.Version)) || snapshotJSON(m.Before) != snapshotJSON(storedVersionSpec(t, s, created.ID, m.BaseVersion)) {
			t.Errorf("writer %d: version %d base %d, expected its own committed snapshot", i, m.Record.Version, m.BaseVersion)
		}
		diff := Changes(m.Before, m.Record.Spec)
		if len(diff.Panels) != 1 || diff.Panels[0].PanelID != fmt.Sprintf("p%d", i) || len(diff.Panels[0].Fields) != 1 || diff.Panels[0].Fields[0] != "content" {
			t.Errorf("writer %d reports other writer's edit: %+v", i, diff)
		}
	}
}

func newConcurrentFileService(t *testing.T) *Service {
	t.Helper()
	sqlite, err := appstore.NewSQLite(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	for _, id := range []string{"owner", "other"} {
		if _, err := sqlite.DB.Exec(`INSERT INTO users (id, email) VALUES (?, ?)`, id, id+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	sqlite.DB.SetMaxOpenConns(8)
	sqlite.DB.SetMaxIdleConns(8)
	return New(sqlite.DB, structural{})
}

func concurrentSpec(n int) panel.Dashboard {
	d := panel.Dashboard{Name: "Race"}
	for i := 0; i < n; i++ {
		d.Panels = append(d.Panels, panel.Panel{ID: fmt.Sprintf("p%d", i), Title: fmt.Sprintf("P%d", i), Viz: "text", Content: "base", Width: 12})
	}
	return d
}

func storedVersionSpec(t *testing.T, s *Service, id string, version int) panel.Dashboard {
	t.Helper()
	raw, err := generated.New(s.db).GetDashboardVersion(t.Context(), generated.GetDashboardVersionParams{DashboardID: id, Version: int64(version)})
	if err != nil {
		t.Fatalf("version %d: %v", version, err)
	}
	var d panel.Dashboard
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func snapshotJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// Many writers race on the latest version (base 0, one retry). Every success
// must report its own committed version, the exact stored base it applied to,
// and a diff that names only its own panel.
func TestConcurrentLatestMutationsReportOnlyTheirOwnCommittedChange(t *testing.T) {
	for round := 0; round < 12; round++ {
		s := newConcurrentFileService(t)
		const n = 8
		created, err := s.Create(t.Context(), "owner", concurrentSpec(n), agent)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		gate := make(chan struct{})
		muts := make([]Mutation, n)
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				muts[i], errs[i] = s.EditWithChanges(t.Context(), "owner", created.ID, []Operation{{Op: "update_panel", ID: fmt.Sprintf("p%d", i), Set: map[string]any{"content": fmt.Sprintf("w%d", i)}}}, 0, agent, "")
			}()
		}
		close(gate)
		wg.Wait()
		seen := map[int]bool{}
		ok := 0
		for i := 0; i < n; i++ {
			if errs[i] != nil {
				if !errors.Is(errs[i], ErrStale) {
					t.Errorf("round %d writer %d: unexpected error %v", round, i, errs[i])
				}
				if muts[i].Record.ID != "" || muts[i].BaseVersion != 0 || len(muts[i].Before.Panels) != 0 {
					t.Errorf("failed writer %d returned mutation %+v", i, muts[i])
				}
				continue
			}
			ok++
			m := muts[i]
			if seen[m.Record.Version] {
				t.Errorf("duplicate version %d", m.Record.Version)
			}
			seen[m.Record.Version] = true
			if m.Record.Version != m.BaseVersion+1 {
				t.Errorf("writer %d: version %d base %d", i, m.Record.Version, m.BaseVersion)
			}
			if got := m.Record.Spec.Panels[i].Content; got != fmt.Sprintf("w%d", i) {
				t.Errorf("writer %d: record shows content %q", i, got)
			}
			if snapshotJSON(storedVersionSpec(t, s, created.ID, m.BaseVersion)) != snapshotJSON(m.Before) {
				t.Errorf("writer %d: Before differs from stored base v%d", i, m.BaseVersion)
			}
			if snapshotJSON(storedVersionSpec(t, s, created.ID, m.Record.Version)) != snapshotJSON(m.Record.Spec) {
				t.Errorf("writer %d: Record differs from stored v%d", i, m.Record.Version)
			}
			d := Changes(m.Before, m.Record.Spec)
			if len(d.Panels) != 1 || d.Panels[0].PanelID != fmt.Sprintf("p%d", i) || len(d.Panels[0].Fields) != 1 || d.Panels[0].Fields[0] != "content" || d.LayoutChanged || len(d.DashboardFields) != 0 {
				t.Errorf("writer %d: diff reports others' edits: %+v", i, d)
			}
		}
		if ok == 0 {
			t.Errorf("round %d: no writer succeeded", round)
		}
		t.Logf("round %d: %d/%d latest-base writers committed", round, ok, n)
	}
}

// Writers with the same explicit base: exactly one wins, all others are stale
// and get no mutation.
func TestConcurrentExplicitBaseMutationsHaveOneWinner(t *testing.T) {
	for round := 0; round < 12; round++ {
		s := newConcurrentFileService(t)
		const n = 8
		created, err := s.Create(t.Context(), "owner", concurrentSpec(n), agent)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		gate := make(chan struct{})
		muts := make([]Mutation, n)
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				if i%2 == 0 {
					muts[i], errs[i] = s.EditWithChanges(t.Context(), "owner", created.ID, []Operation{{Op: "update_panel", ID: fmt.Sprintf("p%d", i), Set: map[string]any{"title": fmt.Sprintf("T%d", i)}}}, 1, agent, "")
				} else {
					spec := concurrentSpec(n)
					spec.Panels = spec.Panels[:n-1]
					spec.Panels[0].Title = "Replaced"
					spec.Name = fmt.Sprintf("Race %d", i)
					muts[i], errs[i] = s.ReplaceWithChanges(t.Context(), "owner", created.ID, spec, 1, agent, "")
				}
			}()
		}
		close(gate)
		wg.Wait()
		winners := 0
		for i := 0; i < n; i++ {
			if errs[i] == nil {
				winners++
				m := muts[i]
				if m.BaseVersion != 1 || m.Record.Version != 2 {
					t.Errorf("winner %d: %+v", i, m)
				}
				d := Changes(m.Before, m.Record.Spec)
				if i%2 == 0 {
					if len(d.Panels) != 1 || d.Panels[0].PanelID != fmt.Sprintf("p%d", i) || d.Panels[0].Fields[0] != "title" {
						t.Errorf("edit winner %d diff %+v", i, d)
					}
				} else if len(d.Panels) != 2 || d.Panels[0].PanelID != "p0" || d.Panels[0].Fields[0] != "title" || d.Panels[1].Kind != "removed" || len(d.DashboardFields) != 1 || d.DashboardFields[0] != "name" {
					t.Errorf("replace winner %d diff %+v", i, d)
				}
				continue
			}
			if !errors.Is(errs[i], ErrStale) {
				t.Errorf("loser %d: unexpected error %v", i, errs[i])
			}
			if muts[i].Record.ID != "" {
				t.Errorf("loser %d returned a record", i)
			}
		}
		if winners != 1 {
			t.Errorf("round %d: %d winners", round, winners)
		}
	}
}
