package dashboard

import (
	"context"
	"errors"
	"fmt"
	"github.com/labstack/fanout/internal/panel"
	"strings"
	"sync"
	"testing"
	"time"
)

type deadlineValidator struct {
	t    *testing.T
	seen bool
}

func (v *deadlineValidator) Validate(ctx context.Context, _ *panel.Dashboard) error {
	deadline, ok := ctx.Deadline()
	v.seen = true
	if !ok || time.Until(deadline) > 20*time.Second || time.Until(deadline) < 19*time.Second {
		v.t.Errorf("validator deadline=%v present=%v", deadline, ok)
		return context.DeadlineExceeded
	}
	// A blocking validator must be cancellable through the supplied context.
	child, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	<-child.Done()
	return child.Err()
}
func TestWriteValidationDeadline(t *testing.T) {
	for _, op := range []string{"create", "replace", "edit", "restore"} {
		t.Run(op, func(t *testing.T) {
			s := newTestService(t)
			r, err := s.Create(t.Context(), "owner", textSpec("Ops"), agent)
			if err != nil {
				t.Fatal(err)
			}
			v := &deadlineValidator{t: t}
			s.validator = v
			switch op {
			case "create":
				_, err = s.Create(t.Context(), "owner", textSpec("Next"), agent)
			case "replace":
				_, err = s.Replace(t.Context(), "owner", r.ID, r.Spec, 1, agent, "")
			case "edit":
				_, err = s.Edit(t.Context(), "owner", r.ID, []Operation{{Op: "rename", Name: "Next"}}, 1, agent, "")
			case "restore":
				_, err = s.Restore(t.Context(), "owner", r.ID, 1, agent)
			}
			if !v.seen || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("validation=%v err=%v", v.seen, err)
			}
		})
	}
}
func TestRemoveAndReplacePreserveGrid(t *testing.T) {
	s := newTestService(t)
	r, err := s.Create(t.Context(), "owner", threePanels(), agent)
	if err != nil {
		t.Fatal(err)
	}
	r.Spec.Panels[0].Grid = &panel.Grid{X: 0, Y: 0, W: 8, H: 3}
	r.Spec.Panels[1].Grid = &panel.Grid{X: 8, Y: 0, W: 4, H: 3}
	r, err = s.Replace(t.Context(), "owner", r.ID, r.Spec, 1, Author{Kind: "user"}, "Widen A")
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Edit(t.Context(), "owner", r.ID, []Operation{{Op: "remove_panel", ID: "b"}}, 2, Author{Kind: "user"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if *r.Spec.Panels[0].Grid != (panel.Grid{X: 0, Y: 0, W: 8, H: 3}) || *r.Spec.Panels[1].Grid != (panel.Grid{X: 0, Y: 3, W: 12, H: 3}) {
		t.Fatalf("removed layout=%+v %+v", r.Spec.Panels[0].Grid, r.Spec.Panels[1].Grid)
	}
	for i := range r.Spec.Panels {
		r.Spec.Panels[i].Grid = nil
	}
	r, err = s.Replace(t.Context(), "owner", r.ID, r.Spec, 3, agent, "")
	if err != nil || r.Spec.Panels[0].Grid.W != 8 {
		t.Fatalf("omitted grids=%+v err=%v", r.Spec.Panels, err)
	}
}
func TestDashboardOwnerLimit(t *testing.T) {
	s := newTestService(t)
	for i := range 500 {
		if _, err := s.Create(t.Context(), "owner", textSpec(fmt.Sprintf("d%d", i)), agent); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Create(t.Context(), "owner", textSpec("overflow"), agent)
	var p panel.Problems
	if !errors.As(err, &p) || !strings.Contains(p.Error(), "500") {
		t.Fatalf("limit error=%v", err)
	}
	if _, err = s.Create(t.Context(), "other", textSpec("separate"), agent); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCreatesRespectOwnerLimit(t *testing.T) {
	s := newTestService(t)
	for i := range 499 {
		if _, err := s.Create(t.Context(), "owner", textSpec(fmt.Sprintf("d%d", i)), agent); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	success := make(chan bool, 16)
	for i := range 16 {
		wg.Go(func() {
			_, err := s.Create(t.Context(), "owner", textSpec(fmt.Sprintf("last%d", i)), agent)
			var p panel.Problems
			if err != nil && !errors.As(err, &p) {
				t.Errorf("create error=%v", err)
			}
			success <- err == nil
		})
	}
	wg.Wait()
	close(success)
	created := 0
	for ok := range success {
		if ok {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created %d, want one remaining owner slot", created)
	}
}

func TestReplaceWithoutGridsKeepsExistingLayout(t *testing.T) {
	s := newTestService(t)
	spec := threePanels()
	spec.Panels[0].Grid = &panel.Grid{X: 1, Y: 0, W: 8, H: 4}
	spec.Panels[1].Grid = &panel.Grid{X: 9, Y: 0, W: 3, H: 3}
	spec.Panels[2].Grid = &panel.Grid{X: 0, Y: 4, W: 12, H: 3}
	r, err := s.Create(t.Context(), "owner", spec, agent)
	if err != nil {
		t.Fatal(err)
	}
	for i := range spec.Panels {
		spec.Panels[i].Grid = nil
	}
	r, err = s.Replace(t.Context(), "owner", r.ID, spec, 1, agent, "")
	if err != nil {
		t.Fatal(err)
	}
	if *r.Spec.Panels[0].Grid != (panel.Grid{X: 1, Y: 0, W: 8, H: 4}) {
		t.Fatalf("carryover grid=%+v", r.Spec.Panels[0].Grid)
	}
}
