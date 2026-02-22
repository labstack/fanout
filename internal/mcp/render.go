package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// isValidReportID checks if the string is a valid UUID
func isValidReportID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}

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
	if !isValidReportID(r.ID) {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	_ = os.MkdirAll(rs.dir, 0755)
	data, _ := json.Marshal(r)
	_ = os.WriteFile(filepath.Join(rs.dir, r.ID+".json"), data, 0644)
}

func (rs *ReportStore) Get(id string) *Report {
	if !isValidReportID(id) {
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
		id := strings.TrimSuffix(e.Name(), ".json")
		if !isValidReportID(id) {
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
	sort.Slice(reports, func(i, j int) bool {
		return reports[j].CreatedAt.Before(reports[i].CreatedAt)
	})
	return reports
}

func (rs *ReportStore) Delete(id string) bool {
	if !isValidReportID(id) {
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

func genID() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic("uuid v7 generation failed: " + err.Error())
	}
	return id.String()
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
