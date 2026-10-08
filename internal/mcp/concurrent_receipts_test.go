package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	controlstore "github.com/labstack/fanout/internal/store"
)

func newConcurrentFileServer(t *testing.T) *Server {
	t.Helper()
	database, err := controlstore.NewSQLite(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, owner := range []string{"owner", "other"} {
		if _, err := database.DB.Exec(`INSERT INTO users(id,email,name,role,active) VALUES(?,?,?,'admin',1)`, owner, owner+"@example.test", owner); err != nil {
			t.Fatal(err)
		}
	}
	database.DB.SetMaxOpenConns(8)
	database.DB.SetMaxIdleConns(8)
	s := NewWithIntelligence(&fakeObservability{}, dashboard.New(database.DB, structural{}), nil, nil, "test")
	return s
}

// Every receipt's save_check must have executed exactly the committed spec of
// its own version, and its changes must name only its own panel.
func TestConcurrentFileBackedSaveReceiptsNameOnlyTheirOwnEdit(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint("explicit=", explicit), func(t *testing.T) {
			s := newConcurrentFileServer(t)
			const n = 6
			spec := panel.Dashboard{Name: "Race"}
			for i := 0; i < n; i++ {
				spec.Panels = append(spec.Panels, panel.Panel{ID: fmt.Sprintf("p%d", i), Title: fmt.Sprintf("P%d", i), Viz: "text", Content: "base", Width: 12})
			}
			var mu sync.Mutex
			checkedSpecs := map[string]panel.Dashboard{} // content signature -> spec checked
			s.panels = receiptExecutor{run: func(_ context.Context, req panel.RunRequest) ([]panel.Result, error) {
				out := make([]panel.Result, 0, len(req.Dashboard.Panels))
				sig := ""
				for _, p := range req.Dashboard.Panels {
					out = append(out, panel.Result{ID: p.ID, Status: "ok"})
					sig += p.Content + ","
				}
				mu.Lock()
				checkedSpecs[sig] = req.Dashboard
				mu.Unlock()
				return out, nil
			}}
			_, created, err := s.dashboardCreate(t.Context(), ownerRequest(), DashboardCreateInput{Dashboard: spec})
			if err != nil {
				t.Fatal(err)
			}
			base := 0
			if explicit {
				base = 1
			}
			type answer struct {
				out dashboardOutput
				err error
			}
			answers := make([]answer, n)
			var wg sync.WaitGroup
			gate := make(chan struct{})
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-gate
					_, out, err := s.dashboardEdit(t.Context(), ownerRequest(), DashboardEditInput{ID: created.Dashboard.ID, BaseVersion: base, Operations: []dashboard.Operation{{Op: "update_panel", ID: fmt.Sprintf("p%d", i), Set: map[string]any{"content": fmt.Sprintf("w%d", i)}}}})
					answers[i] = answer{out, err}
				}()
			}
			close(gate)
			wg.Wait()
			winners := 0
			versions := map[int]bool{}
			for i, a := range answers {
				if a.err != nil {
					if !strings.Contains(a.err.Error(), "the dashboard changed since you read it") {
						t.Errorf("loser %d: unexpected error %v", i, a.err)
					}
					if a.out.Receipt != nil || a.out.Dashboard.ID != "" {
						t.Errorf("loser %d has receipt %+v", i, a.out)
					}
					continue
				}
				winners++
				r := a.out.Receipt
				if r == nil || r.Version != a.out.Dashboard.Version || r.BaseVersion != r.Version-1 || !r.SaveCheck.Checked {
					t.Errorf("winner %d receipt %+v", i, r)
					continue
				}
				if versions[r.Version] {
					t.Errorf("duplicate receipt version %d", r.Version)
				}
				versions[r.Version] = true
				if len(r.Changes) != 1 || r.Changes[0].PanelID != fmt.Sprintf("p%d", i) || len(r.Changes[0].Fields) != 1 || r.Changes[0].Fields[0] != "content" || r.LayoutChanged || len(r.DashboardFields) != 0 {
					t.Errorf("winner %d reports other writers' edits: %+v", i, r)
				}
				sig := ""
				for _, p := range a.out.Dashboard.Spec.Panels {
					sig += p.Content + ","
				}
				mu.Lock()
				_, ok := checkedSpecs[sig]
				mu.Unlock()
				if !ok {
					t.Errorf("winner %d: its committed spec %q was never checked", i, sig)
				}
			}
			if explicit && winners != 1 {
				t.Errorf("explicit base: %d winners", winners)
			}
			if winners == 0 {
				t.Error("no winners")
			}
			t.Logf("explicit=%v winners=%d/%d", explicit, winners, n)
		})
	}
}
