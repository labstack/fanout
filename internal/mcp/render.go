package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/render"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// validReportID matches hex-encoded report IDs (genID produces 16 hex chars from 8 bytes)
var validReportID = regexp.MustCompile(`^[a-f0-9]+$`)

// Report stores a shareable report snapshot
type Report struct {
	ID        string    `json:"id"`
	Query     string    `json:"query"`
	Summary   string    `json:"summary"`
	HTML      string    `json:"html"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ReportStore persists reports to disk
type ReportStore struct {
	mu  sync.RWMutex
	dir string
}

var reports = &ReportStore{}

// InitReportStore sets the reports directory (call from NewServer)
func InitReportStore(lakeDir string) {
	reports.dir = filepath.Join(lakeDir, "reports")
}

func (rs *ReportStore) Save(r *Report) {
	if !validReportID.MatchString(r.ID) {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_ = os.MkdirAll(rs.dir, 0755)
	data, _ := json.Marshal(r)
	_ = os.WriteFile(filepath.Join(rs.dir, r.ID+".json"), data, 0644)
}

func (rs *ReportStore) Get(id string) *Report {
	if !validReportID.MatchString(id) {
		return nil
	}
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(rs.dir, id+".json"))
	if err != nil {
		return nil
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil
	}
	return &r
}

func (rs *ReportStore) List() []*Report {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	entries, err := os.ReadDir(rs.dir)
	if err != nil {
		return nil
	}
	var reports []*Report
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rs.dir, e.Name()))
		if err != nil {
			continue
		}
		var r Report
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		reports = append(reports, &r)
	}
	// Sort by created_at descending
	for i := 0; i < len(reports)-1; i++ {
		for j := i + 1; j < len(reports); j++ {
			if reports[j].CreatedAt.After(reports[i].CreatedAt) {
				reports[i], reports[j] = reports[j], reports[i]
			}
		}
	}
	return reports
}

func (rs *ReportStore) Delete(id string) bool {
	if !validReportID.MatchString(id) {
		return false
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	err := os.Remove(filepath.Join(rs.dir, id+".json"))
	return err == nil
}

func (rs *ReportStore) Cleanup() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	entries, err := os.ReadDir(rs.dir)
	if err != nil {
		return 0
	}
	now := time.Now()
	deleted := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(rs.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var r Report
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		if now.After(r.ExpiresAt) {
			os.Remove(path)
			deleted++
		}
	}
	return deleted
}

// RenderIn is the input for the render tool
type RenderIn struct {
	Title    string    `json:"title" jsonschema:"Report title,required"`
	Sections []Section `json:"sections" jsonschema:"Report sections,required"`
}

// Section represents a report section
type Section struct {
	Type   string         `json:"type"`
	Title  string         `json:"title,omitempty"`
	Config map[string]any `json:"config"`
}

// RenderOut is the output of the render tool
type RenderOut struct {
	HTML     string `json:"html"`
	ShareURL string `json:"share_url"`
	ReportID string `json:"report_id"`
}

func (s *Server) render(ctx context.Context, req *mcp.CallToolRequest, in RenderIn) (*mcp.CallToolResult, RenderOut, error) {
	var htmlParts []string

	for _, sec := range in.Sections {
		// Marshal config to JSON for registry
		cfgJSON, err := json.Marshal(sec.Config)
		if err != nil {
			return nil, RenderOut{}, fmt.Errorf("section %q: invalid config: %w", sec.Type, err)
		}

		// Validate config before rendering
		if err := render.Validate(sec.Type, cfgJSON); err != nil {
			return nil, RenderOut{}, fmt.Errorf("section %q: %w", sec.Type, err)
		}

		// Use registry to render
		out, err := render.RenderSection(sec.Type, cfgJSON, render.HTML)
		if err != nil {
			return nil, RenderOut{}, fmt.Errorf("section %q: %w", sec.Type, err)
		}

		// Wrap in titled section if title provided
		sectionHTML := out.HTML
		if sec.Title != "" && sectionHTML != "" {
			sectionHTML = `<div class="section"><div class="section-title">` + html.EscapeString(sec.Title) + `</div>` + sectionHTML + `</div>`
		}
		htmlParts = append(htmlParts, sectionHTML)
	}

	composedHTML := `<div class="compose compose-column">` + strings.Join(htmlParts, "") + `</div>`

	id := genID()
	report := &Report{
		ID:        id,
		Query:     html.EscapeString(in.Title),
		Summary:   html.EscapeString(in.Title),
		HTML:      composedHTML,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	reports.Save(report)

	return nil, RenderOut{
		HTML:     composedHTML,
		ShareURL: "/view/r/" + id,
		ReportID: id,
	}, nil
}

func genID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// GetReport retrieves a report by ID
func GetReport(id string) *Report {
	return reports.Get(id)
}

// ListReports returns all reports
func ListReports() []*Report {
	return reports.List()
}

// DeleteReport removes a report by ID
func DeleteReport(id string) bool {
	return reports.Delete(id)
}

// RunCleanup starts background cleanup goroutine
func RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	reports.Cleanup()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reports.Cleanup()
		}
	}
}

// ComponentTypes returns all available component types
func ComponentTypes() []string {
	return render.Types()
}

// ComponentToolDescription returns the tool description for all components
func ComponentToolDescription() string {
	return render.ToolDescription()
}
