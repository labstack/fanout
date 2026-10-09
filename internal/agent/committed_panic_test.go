package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/labstack/fanout/internal/dashboard"
	fanoutmcp "github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/panel"
	controlstore "github.com/labstack/fanout/internal/store"
)

func TestRuntimePanicDistinguishesCommittedSave(t *testing.T) {
	for _, source := range []string{"before_commit", "after_service_commit", "after_registry_commit"} {
		t.Run(source, func(t *testing.T) {
			commit := source != "before_commit"
			db, err := controlstore.NewSQLite(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.DB.Exec(`INSERT INTO users(id,email) VALUES ('owner','owner@example.test')`); err != nil {
				t.Fatal(err)
			}
			service := dashboard.New(db.DB, panel.NewExecutor(nil, 30))
			server := fanoutmcp.NewWithIntelligence(registryQueries{}, service, panel.NewExecutor(nil, 30), nil, "test")
			registry, err := NewToolRegistry(t.Context(), server.MCP())
			if err != nil {
				t.Fatal(err)
			}
			defer registry.Close()
			spec := panel.Dashboard{Name: "Committed", Panels: []panel.Panel{{ID: "note", Title: "Note", Viz: "text", Content: "hello"}}}
			input, err := json.Marshal(map[string]any{"dashboard": spec})
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(old)
			tools := errorPathTools{execute: func(ctx context.Context, call ToolCall) (ToolExecution, error) {
				if source == "after_registry_commit" {
					result, err := registry.Execute(ctx, call)
					if err != nil || result.IsError {
						t.Fatalf("registry=%+v err=%v", result, err)
					}
				} else if commit {
					if _, err := service.Create(ctx, "owner", spec, dashboard.Author{Kind: "agent", ID: "owner"}); err != nil {
						t.Fatal(err)
					}
				}
				panic("private panic detail")
			}}
			call := ToolCall{ID: "save", Name: "create_dashboard", Input: string(input)}
			provider := &scriptedProvider{steps: [][]StreamEvent{{{Type: EventToolUse, ToolCall: &call}, {Type: EventStop, StopReason: "tool_calls"}}, {{Type: EventStop, StopReason: "end_turn"}}}}
			messages := []agtypes.Message{{ID: "user", Role: agtypes.RoleUser, Content: "Build"}}
			emitter, wire := newTestEmitter()
			if _, err := NewRuntime(provider, tools, nil).execute(dashboard.WithOwner(t.Context(), "owner"), "thread", "run", &messages, emitter); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, m := range messages {
				if m.Role != agtypes.RoleTool {
					continue
				}
				found = true
				var p map[string]any
				if json.Unmarshal([]byte(messageText(m.Content)), &p) != nil {
					t.Fatal(m)
				}
				failure, ok := p["error"].(map[string]any)
				want := "tool_failed"
				if commit {
					want = "interrupted"
				}
				if !ok || failure["code"] != want || failure["message"] == "" || p["isError"] != true || commit && m.Error != "interrupted" || !commit && m.Error == "interrupted" {
					t.Fatalf("commit=%v message=%+v", commit, m)
				}
			}
			if !found {
				t.Fatal("missing tool result")
			}
			var count int
			if err := db.DB.QueryRow(`SELECT count(*) FROM dashboards`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if (count == 1) != commit {
				t.Fatal("commit state mismatch")
			}
			if strings.Contains(wire.String(), "private panic detail") || strings.Contains(wire.String(), "goroutine") || !strings.Contains(logs.String(), "goroutine") || !strings.Contains(logs.String(), "create_dashboard") {
				t.Fatalf("missing private stack log or wire leak: %s", logs.String())
			}
		})
	}
}
