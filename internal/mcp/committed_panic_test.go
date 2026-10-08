package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type panicValidator struct{ armed atomic.Bool }

func (v *panicValidator) Validate(ctx context.Context, d *panel.Dashboard) error {
	if v.armed.Load() {
		panic("private panic detail")
	}
	return (structural{}).Validate(ctx, d)
}

func TestMCPPanicDistinguishesCommittedSave(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, tool := range []string{"create_dashboard", "restore_dashboard_version"} {
			t.Run(tool+"/"+map[bool]string{false: "before_commit", true: "after_commit"}[commit], func(t *testing.T) {
				panicValidation := &panicValidator{}
				var validator dashboard.Validator = panicValidation
				if commit {
					validator = structural{}
				}
				s := newToolServer(t, validator, nil)
				args := map[string]any{"dashboard": textDashboard("Panic")}
				if tool == "restore_dashboard_version" {
					record, err := s.dashboards.Create(t.Context(), "owner", textDashboard("Panic"), agentAuthor("owner"))
					if err != nil {
						t.Fatal(err)
					}
					args = map[string]any{"id": record.ID, "version": 1}
				}
				panicValidation.armed.Store(true)
				if commit {
					s.panels = receiptExecutor{run: func(context.Context, panel.RunRequest) ([]panel.Result, error) { panic("private panic detail") }}
				}
				var logs bytes.Buffer
				old := slog.Default()
				slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
				defer slog.SetDefault(old)
				session := connectTestClient(t, s, nil)
				res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args, Meta: mcp.Meta{dashboard.OwnerMetaKey: "owner"}})
				if err != nil || !res.IsError {
					t.Fatalf("result=%+v err=%v", res, err)
				}
				raw, _ := json.Marshal(res)
				if commit && !strings.Contains(string(raw), "interrupted") || !commit && strings.Contains(string(raw), "interrupted") {
					t.Fatalf("commit=%v result=%s", commit, raw)
				}
				if strings.Contains(string(raw), "private panic detail") || strings.Contains(string(raw), "goroutine") || !strings.Contains(logs.String(), "goroutine") || !strings.Contains(logs.String(), tool) {
					t.Fatalf("missing private stack log or wire leak: %s", logs.String())
				}
				want := 0
				if tool == "restore_dashboard_version" {
					want++
				}
				if commit {
					want++
				}
				if want > 0 {
					items, err := s.dashboards.List(t.Context(), "owner")
					if err != nil || len(items) != 1 || items[0].Version != want {
						t.Fatal(items, err)
					}
				}
			})
		}
	}
}
