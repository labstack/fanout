package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/fanql"
	"github.com/labstack/fanout/internal/panel"
)

type nonExecutingFanQLPanels struct {
	fakePanels
	calls int
}

func (p *nonExecutingFanQLPanels) Run(context.Context, panel.RunRequest) ([]panel.Result, error) {
	p.calls++
	return nil, fmt.Errorf("FanQL parsing must not execute telemetry")
}

func TestNonExecution(t *testing.T) {
	file, err := os.Open("../fanql/testdata/corpus.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	s := newTestAuthServer(t)
	if _, err := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	viewer, err := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, viewer)
	engine := &nonExecutingFanQLPanels{}
	RegisterFanQLRoutes(s.e, nil, engine)

	decoder := json.NewDecoder(file)
	negative := 0
	for fixture := 0; ; fixture++ {
		var tc struct {
			Text         string         `json:"text"`
			Viz          string         `json:"viz"`
			Query        *panel.Query   `json:"query"`
			Problems     panel.Problems `json:"problems"`
			GateProblems bool           `json:"gate_problems"`
		}
		if err := decoder.Decode(&tc); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("corpus fixture %d: %v", fixture, err)
		}
		// Raw SQL rejected only by the existing execution gate is clean FanQL.
		// The parse route must retain the same Compile-only contract.
		if tc.Query != nil || len(tc.Problems) == 0 || tc.GateProblems {
			continue
		}
		negative++
		t.Run(fmt.Sprintf("%d/%s", fixture, tc.Text), func(t *testing.T) {
			compiled, problems := fanql.Compile(tc.Text, fanql.Options{Viz: tc.Viz})
			if compiled != nil || len(problems) == 0 {
				t.Fatalf("negative corpus text became executable: %+v %+v", compiled, problems)
			}
			for _, preview := range []bool{false, true} {
				input, err := json.Marshal(map[string]any{"text": tc.Text, "viz": tc.Viz, "preview": preview})
				if err != nil {
					t.Fatal(err)
				}
				req := sessionRequest(http.MethodPost, "/api/fanql/parse", strings.NewReader(string(input)), cookie)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				s.e.ServeHTTP(rec, req)
				var body map[string]json.RawMessage
				fanqlResponse(t, rec, http.StatusBadRequest, &body)
				if string(body["code"]) != `"invalid_fanql"` || body["problems"] == nil {
					t.Fatalf("preview=%t: wrong error contract: %s", preview, rec.Body)
				}
				if _, exists := body["query"]; exists {
					t.Fatalf("preview=%t: problem response contains executable query: %s", preview, rec.Body)
				}
				if preview {
					var got struct {
						Text string `json:"text"`
						Note string `json:"note"`
					}
					if err := json.Unmarshal(body["preview"], &got); err != nil {
						t.Fatal(err)
					}
					if got.Text != tc.Text || got.Note != "not executable" {
						t.Fatalf("preview lost authored text or execution warning: %s", rec.Body)
					}
				} else if _, exists := body["preview"]; exists {
					t.Fatalf("unrequested preview: %s", rec.Body)
				}
			}
		})
	}
	if negative == 0 {
		t.Fatal("corpus has no non-executable texts")
	}
	if engine.calls != 0 {
		t.Fatalf("parse executed %d telemetry queries", engine.calls)
	}
}
