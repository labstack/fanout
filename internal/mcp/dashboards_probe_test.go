package mcp

import (
	"context"
	"strings"
	"testing"

	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/labstack/fanout/internal/dashboard"
	controlstore "github.com/labstack/fanout/internal/store"
)

func newDashboardToolFixture(t *testing.T, queries Observability) (*Server, *mcp.CallToolRequest) {
	t.Helper()
	database, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.DB.ExecContext(context.Background(), `INSERT INTO users(id,email,name,role,active) VALUES('owner','owner@example.test','Owner','admin',1)`); err != nil {
		t.Fatal(err)
	}
	server := New(queries, dashboard.New(database.DB, 30), "test")
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}, Extra: &mcp.RequestExtra{TokenInfo: &mcpgoauth.TokenInfo{UserID: "owner", Scopes: []string{dashboard.OAuthScope}}}}
	return server, req
}

func filteredLogsDashboard() dashboard.State {
	return dashboard.State{
		Filters: dashboard.Filters{Window: "6h"},
		Widgets: []dashboard.Widget{
			{ID: "errors", Type: "logs", Title: "Checkout Error Logs", Config: map[string]any{"service": "checkout", "severity": "ERROR"}, Enabled: true},
			{ID: "health", Type: "overview", Title: "System health", Enabled: true},
		},
		Layout: []dashboard.Layout{
			{I: "errors", X: 0, Y: 0, W: 6, H: 3},
			{I: "health", X: 6, Y: 0, W: 6, H: 3},
		},
	}
}

func createText(t *testing.T, server *Server, req *mcp.CallToolRequest, name string, state dashboard.State) string {
	t.Helper()
	result, _, err := server.dashboardCreate(context.Background(), req, DashboardCreateInput{Name: name, State: state})
	if err != nil {
		t.Fatalf("dashboardCreate: %v", err)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if message, ok := content.(*mcp.TextContent); ok {
			text.WriteString(message.Text)
		}
	}
	return text.String()
}

func TestDashboardCreateReportsWidgetsWithNothingToShow(t *testing.T) {
	// The model composes a dashboard from what it believes is there. A filter
	// that matches nothing produced a card that was empty on arrival and was
	// still described to the user as a working view.
	server, req := newDashboardToolFixture(t, &fakeObservability{})
	text := createText(t, server, req, "Checkout Order Path", filteredLogsDashboard())
	if !strings.Contains(text, "Checkout Error Logs") {
		t.Fatalf("summary = %q, want the empty card named", text)
	}
	if !strings.Contains(text, "render empty") {
		t.Fatalf("summary = %q, want it to say the card has nothing to show", text)
	}
}

func TestDashboardCreateStaysQuietWhenWidgetsHaveData(t *testing.T) {
	server, req := newDashboardToolFixture(t, &fakeObservability{logEntries: 3})
	text := createText(t, server, req, "Checkout Order Path", filteredLogsDashboard())
	if strings.Contains(text, "render empty") {
		t.Fatalf("summary = %q, want no warning when the card has data", text)
	}
}
