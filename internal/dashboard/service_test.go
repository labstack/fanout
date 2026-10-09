package dashboard

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/labstack/fanout/internal/panel"
	appstore "github.com/labstack/fanout/internal/store"
)

// structural validates without DuckDB; filter checks are covered in
// internal/panel and by TestDefaultSpecPassesTheFullCheck.
type structural struct{}

func (structural) Validate(_ context.Context, d *panel.Dashboard) error {
	panel.Normalize(d)
	if problems := panel.Validate(d); len(problems) > 0 {
		return problems
	}
	return nil
}

type blockingValidator struct {
	entered chan struct{}
	release chan struct{}
	blocked atomic.Bool
}

func (v *blockingValidator) Validate(ctx context.Context, d *panel.Dashboard) error {
	if d.Panels[0].Content == "first writer" && v.blocked.CompareAndSwap(false, true) {
		close(v.entered)
		select {
		case <-v.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return (structural{}).Validate(ctx, d)
}

func TestMutationUsesSuccessfulOptimisticBase(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "stale"}[explicit], func(t *testing.T) {
			s := newTestService(t)
			created, err := s.Create(t.Context(), "owner", textSpec("Concurrent"), agent)
			if err != nil {
				t.Fatal(err)
			}
			v := &blockingValidator{entered: make(chan struct{}), release: make(chan struct{})}
			s.validator = v
			type answer struct {
				mutation Mutation
				err      error
			}
			done := make(chan answer, 1)
			base := 0
			if explicit {
				base = 1
			}
			go func() {
				m, e := s.EditWithChanges(t.Context(), "owner", created.ID, []Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"content": "first writer"}}}, base, agent, "")
				done <- answer{m, e}
			}()
			<-v.entered
			second, err := s.EditWithChanges(t.Context(), "owner", created.ID, []Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"title": "Second writer"}}}, 1, agent, "")
			close(v.release)
			if err != nil {
				t.Fatal(err)
			}
			first := <-done
			if explicit {
				if !errors.Is(first.err, ErrStale) || first.mutation.Record.ID != "" {
					t.Fatalf("stale=%+v", first)
				}
			} else {
				m := first.mutation
				if first.err != nil || m.BaseVersion != 2 || m.Record.Version != 3 || m.Before.Panels[0].Title != "Second writer" || m.Before.Panels[0].Content != "hello" || m.Record.Spec.Panels[0].Title != "Second writer" {
					t.Fatalf("retry=%+v", first)
				}
				diff := Changes(m.Before, m.Record.Spec)
				if len(diff.Panels) != 1 || len(diff.Panels[0].Fields) != 1 || diff.Panels[0].Fields[0] != "content" {
					t.Fatal(diff)
				}
			}
			if second.BaseVersion != 1 || second.Record.Version != 2 || second.Record.Spec.Panels[0].Content != "hello" {
				t.Fatal(second)
			}
			versions, err := s.Versions(t.Context(), "owner", created.ID)
			want := 3
			if explicit {
				want = 2
			}
			if err != nil || len(versions) != want {
				t.Fatalf("versions=%+v err=%v", versions, err)
			}
		})
	}
}

func TestRestoreMutationUsesSuccessfulOptimisticBase(t *testing.T) {
	s := newTestService(t)
	spec := textSpec("Restore race")
	spec.Panels[0].Content = "first writer"
	created, err := s.Create(t.Context(), "owner", spec, agent)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Edit(t.Context(), "owner", created.ID, []Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"content": "second writer"}}}, 1, agent, "")
	if err != nil {
		t.Fatal(err)
	}
	v := &blockingValidator{entered: make(chan struct{}), release: make(chan struct{})}
	s.validator = v
	type answer struct {
		mutation Mutation
		err      error
	}
	done := make(chan answer, 1)
	go func() {
		mutation, err := s.RestoreWithChanges(t.Context(), "owner", created.ID, 1, agent)
		done <- answer{mutation, err}
	}()
	<-v.entered
	concurrent, err := s.EditWithChanges(t.Context(), "owner", created.ID, []Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"title": "Concurrent title"}}}, 2, agent, "")
	close(v.release)
	if err != nil {
		t.Fatal(err)
	}
	result := <-done
	m := result.mutation
	if result.err != nil || m.BaseVersion != 3 || m.Record.Version != 4 || !reflect.DeepEqual(m.Before, concurrent.Record.Spec) || m.Record.Spec.Panels[0].Title != created.Spec.Panels[0].Title || m.Record.Spec.Panels[0].Content != "first writer" {
		t.Fatalf("restore retry=%+v", result)
	}
	diff := Changes(m.Before, m.Record.Spec)
	if len(diff.Panels) != 1 || len(diff.Panels[0].Fields) != 2 || diff.Panels[0].Fields[0] != "content" || diff.Panels[0].Fields[1] != "title" {
		t.Fatal(diff)
	}
	versions, err := s.Versions(t.Context(), "owner", created.ID)
	if err != nil || len(versions) != 4 || versions[0].Message != "Restored version 1" {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
}

func TestMutationBeforeDoesNotShareNestedFields(t *testing.T) {
	s := newTestService(t)
	spec := threePanels()
	spec.Panels[0].Thresholds = []panel.Threshold{{Value: 10, Status: "warn"}}
	created, err := s.CreateWithChanges(t.Context(), "owner", spec, agent)
	if err != nil {
		t.Fatal(err)
	}
	if created.BaseVersion != 0 || !created.Record.IsDefault || len(created.Before.Panels) != 0 || spec.Panels[0].Grid != nil {
		t.Fatalf("create=%+v input=%+v", created, spec)
	}
	m, err := s.update(t.Context(), "owner", created.Record.ID, 1, 0, agent, "", func(d panel.Dashboard) (panel.Dashboard, bool, error) {
		d.Panels[0].Title = "Updated"
		d.Panels[0].Thresholds[0].Value = 20
		d.Panels[0].Grid.X++
		return d, false, nil
	})
	if err != nil || m.Before.Panels[0].Title != "A" || m.Before.Panels[0].Thresholds[0].Value != 10 || m.Before.Panels[0].Grid.X != 0 {
		t.Fatalf("mutation=%+v err=%v", m, err)
	}
	m.Record.Spec.Panels[0].Thresholds[0].Value = 30
	if m.Before.Panels[0].Thresholds[0].Value != 10 {
		t.Fatal("shared threshold")
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	for _, id := range []string{"owner", "other"} {
		if _, err := sqlite.DB.Exec(`INSERT INTO users (id, email) VALUES (?, ?)`, id, id+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	return New(sqlite.DB, structural{})
}

func textSpec(name string) panel.Dashboard {
	return panel.Dashboard{Name: name, Panels: []panel.Panel{{ID: "notes", Title: "Notes", Viz: "text", Content: "hello"}}}
}

var agent = Author{Kind: "agent", ID: "owner"}

func TestFirstVisitCreatesTheDefaultDashboard(t *testing.T) {
	s := newTestService(t)
	items, err := s.List(t.Context(), "owner")
	if err != nil || len(items) != 1 || !items[0].IsDefault || items[0].PanelCount != len(DefaultSpec().Panels) {
		t.Fatalf("items = %+v err %v", items, err)
	}
	record, err := s.Get(t.Context(), "owner", items[0].ID)
	if err != nil || record.Spec.Panels[0].Grid == nil {
		t.Fatalf("default record = %+v err %v", record, err)
	}
}

func TestCreateEditRestoreAndVersions(t *testing.T) {
	s := newTestService(t)
	created, err := s.Create(t.Context(), "owner", textSpec("Ops"), agent)
	if err != nil || created.Version != 1 || created.Spec.Panels[0].Grid == nil {
		t.Fatalf("created = %+v err %v", created, err)
	}
	edited, err := s.Edit(t.Context(), "owner", created.ID, []Operation{{Op: "update_panel", ID: "notes", Set: map[string]any{"content": "changed"}}}, 1, agent, "Reword the note")
	if err != nil || edited.Version != 2 || edited.Spec.Panels[0].Content != "changed" {
		t.Fatalf("edited = %+v err %v", edited, err)
	}
	restored, err := s.Restore(t.Context(), "owner", created.ID, 1, Author{Kind: "user", ID: "owner"})
	if err != nil || restored.Version != 3 || restored.Spec.Panels[0].Content != "hello" {
		t.Fatalf("restored = %+v err %v", restored, err)
	}
	versions, err := s.Versions(t.Context(), "owner", created.ID)
	if err != nil || len(versions) != 3 || versions[0].Version != 3 || versions[1].Message != "Reword the note" || versions[1].AuthorKind != "agent" {
		t.Fatalf("versions = %+v err %v", versions, err)
	}
}

func TestStaleVersionsAreRejected(t *testing.T) {
	s := newTestService(t)
	created, _ := s.Create(t.Context(), "owner", textSpec("Ops"), agent)
	if _, err := s.Replace(t.Context(), "owner", created.ID, textSpec("Ops 2"), 1, agent, ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.Replace(t.Context(), "owner", created.ID, textSpec("Ops 3"), 1, Author{Kind: "user", ID: "owner"}, "")
	if !errors.Is(err, ErrStale) {
		t.Fatalf("stale replace err = %v", err)
	}
	if _, err := s.Edit(t.Context(), "owner", created.ID, []Operation{{Op: "rename", Name: "Ops 4"}}, 0, agent, ""); err != nil {
		t.Fatalf("base version 0 means latest: %v", err)
	}
}

func TestOwnershipNamesAndValidation(t *testing.T) {
	s := newTestService(t)
	created, _ := s.Create(t.Context(), "owner", textSpec("Ops"), agent)
	if _, err := s.Get(t.Context(), "other", created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner read err = %v", err)
	}
	if _, err := s.Create(t.Context(), "owner", textSpec("ops"), agent); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name err = %v", err)
	}
	bad := textSpec("Bad")
	bad.Panels[0].Viz = "piechart"
	var problems panel.Problems
	if _, err := s.Create(t.Context(), "owner", bad, agent); !errors.As(err, &problems) {
		t.Fatalf("invalid spec err = %v", err)
	}
	if err := s.Delete(t.Context(), "owner", created.ID); err != nil {
		t.Fatal(err)
	}
	items, _ := s.List(t.Context(), "owner")
	if len(items) != 1 || !items[0].IsDefault {
		t.Fatalf("after delete = %+v", items)
	}
}

func threePanels() panel.Dashboard {
	return panel.Dashboard{Name: "Layout", Panels: []panel.Panel{
		{ID: "a", Title: "A", Viz: "text", Content: "a", Width: 6, Height: "s"},
		{ID: "b", Title: "B", Viz: "text", Content: "b", Width: 6, Height: "s"},
		{ID: "c", Title: "C", Viz: "text", Content: "c", Width: 12, Height: "s"},
	}}
}

func TestReplaceRepacksOnlyWhenLayoutChanges(t *testing.T) {
	s := newTestService(t)
	created, err := s.Create(t.Context(), "owner", threePanels(), agent)
	if err != nil {
		t.Fatal(err)
	}
	// Removing a panel leaves no hole.
	removed := created.Spec
	removed.Panels = []panel.Panel{removed.Panels[0], removed.Panels[2]}
	rec, err := s.Replace(t.Context(), "owner", created.ID, removed, 1, agent, "")
	if err != nil {
		t.Fatal(err)
	}
	if g := *rec.Spec.Panels[1].Grid; g != (panel.Grid{X: 0, Y: 3, W: 12, H: 3}) {
		t.Fatalf("after removal c = %+v", g)
	}
	// A width change takes effect.
	wide := rec.Spec
	wide.Panels = append([]panel.Panel(nil), wide.Panels...)
	wide.Panels[0].Width = 12
	rec, err = s.Replace(t.Context(), "owner", created.ID, wide, 2, agent, "")
	if err != nil || rec.Spec.Panels[0].Grid.W != 12 {
		t.Fatalf("width change = %+v err %v", rec.Spec.Panels[0].Grid, err)
	}
	// Same ids and sizes with moved grids keep their coordinates.
	moved := rec.Spec
	moved.Panels = append([]panel.Panel(nil), moved.Panels...)
	moved.Panels[1].Grid = &panel.Grid{X: 0, Y: 8, W: 12, H: 3}
	rec, err = s.Replace(t.Context(), "owner", created.ID, moved, 3, agent, "")
	if err != nil || *rec.Spec.Panels[1].Grid != (panel.Grid{X: 0, Y: 8, W: 12, H: 3}) {
		t.Fatalf("layout save = %+v err %v", rec.Spec.Panels[1].Grid, err)
	}
}

func TestConcurrentFirstVisitsCreateOneDefault(t *testing.T) {
	for round := 0; round < 10; round++ {
		sqlite, err := appstore.NewSQLite(filepath.Join(t.TempDir(), "control.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlite.DB.Exec(`INSERT INTO users (id, email) VALUES ('owner', 'owner@example.com')`); err != nil {
			t.Fatal(err)
		}
		s := New(sqlite.DB, structural{})
		var wg sync.WaitGroup
		errs := make(chan error, 16)
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := s.List(t.Context(), "owner")
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		var total, defaults int
		if err := sqlite.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(is_default), 0) FROM dashboards WHERE owner_id = 'owner'`).Scan(&total, &defaults); err != nil || total != 1 || defaults != 1 {
			t.Fatalf("round %d: total %d defaults %d err %v", round, total, defaults, err)
		}
		_ = sqlite.Close()
	}
}
