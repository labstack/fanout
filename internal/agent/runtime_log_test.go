package agent

import (
	"bytes"
	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/auth"
	controlstore "github.com/labstack/fanout/internal/store"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeFailureLogPreservesOperationalDetails(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	db, err := controlstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runtime := NewRuntime(textProvider{}, &fakeTools{}, NewStore(db.DB))
	runtime.maxSteps = 0
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"threadId":"thread","runId":"run","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	c := echo.New().NewContext(req, httptest.NewRecorder())
	c.Set("auth_user", &auth.User{ID: "owner"})
	if err := runtime.Run(c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "exceeded 0 tool steps") {
		t.Fatalf("operational error detail lost: %s", logs.String())
	}
}
