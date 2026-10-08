package dashboard

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestHistoricalSpecReadIsOwnerScopedAndImmutable(t *testing.T) {
	s := newTestService(t)
	first, err := s.Create(t.Context(), "owner", textSpec("History"), agent)
	if err != nil {
		t.Fatal(err)
	}
	changed := textSpec("History")
	changed.Panels[0].Content = "second"
	s.now = func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }
	if _, err = s.Replace(t.Context(), "owner", first.ID, changed, first.Version, agent, "Updated note"); err != nil {
		t.Fatal(err)
	}
	old, err := s.VersionRecord(t.Context(), "owner", first.ID, 1)
	if err != nil || old.Dashboard.Version != 1 || old.Dashboard.Spec.Panels[0].Content != "hello" {
		t.Fatalf("old=%+v err=%v", old, err)
	}
	if old.CreatedAt != first.UpdatedAt || old.Dashboard.UpdatedAt != first.UpdatedAt || old.Dashboard.CreatedAt != first.CreatedAt || old.AuthorKind != "agent" || old.AuthorID != "owner" || old.Message != "Created" {
		t.Fatal(old)
	}
	if !old.ChangesAvailable || old.LayoutChanged || len(old.DashboardFields) != 0 || len(old.Changes) != 1 || old.Changes[0].Kind != "added" {
		t.Fatal(old)
	}
	if _, err = s.VersionRecord(t.Context(), "other", first.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ownership error=%v", err)
	}
	for _, version := range []int{-1, 0, 999} {
		if _, err = s.VersionRecord(t.Context(), "owner", first.ID, version); !errors.Is(err, ErrNotFound) {
			t.Fatalf("version %d: %v", version, err)
		}
	}
}

func TestHistoryChangesMatchCommittedSaveSemantics(t *testing.T) {
	s := newTestService(t)
	first, err := s.Create(t.Context(), "owner", threePanels(), agent)
	if err != nil {
		t.Fatal(err)
	}
	edits := [][]Operation{
		{{Op: "update_panel", ID: first.Spec.Panels[0].ID, Set: map[string]any{"title": "New title"}}},
		{{Op: "remove_panel", ID: first.Spec.Panels[0].ID}},
		{{Op: "move_panel", ID: first.Spec.Panels[1].ID, After: first.Spec.Panels[2].ID}},
	}
	for _, ops := range edits {
		saved, err := s.EditWithChanges(t.Context(), "owner", first.ID, ops, 0, agent, "Edit")
		if err != nil {
			t.Fatal(err)
		}
		historic, err := s.VersionRecord(t.Context(), "owner", first.ID, saved.Record.Version)
		if err != nil {
			t.Fatal(err)
		}
		want := Changes(saved.Before, saved.Record.Spec)
		if !historic.ChangesAvailable || !reflect.DeepEqual(historic.Changes, want.Panels) || historic.LayoutChanged != want.LayoutChanged || !reflect.DeepEqual(historic.DashboardFields, want.DashboardFields) {
			t.Fatalf("history=%+v want=%+v", historic, want)
		}
	}
}

func TestPrunedHistoryReportsUnavailableChangesAndRestoreAppendsUserVersion(t *testing.T) {
	s := newTestService(t)
	first, err := s.Create(t.Context(), "owner", textSpec("History"), agent)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < keepVersions; i++ {
		if _, err := s.Replace(t.Context(), "owner", first.ID, textSpec("History"), 0, agent, "Edit"); err != nil {
			t.Fatal(err)
		}
	}
	versions, err := s.Versions(t.Context(), "owner", first.ID)
	if err != nil || len(versions) != 100 || versions[0].Version != 101 || versions[99].Version != 2 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if _, err := s.VersionRecord(t.Context(), "owner", first.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	old, err := s.VersionRecord(t.Context(), "owner", first.ID, 2)
	if err != nil || old.ChangesAvailable || len(old.Changes) != 0 || len(old.DashboardFields) != 0 {
		t.Fatalf("old=%+v err=%v", old, err)
	}
	if _, err := s.Restore(t.Context(), "other", first.ID, 2, Author{Kind: "user", ID: "other"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	restored, err := s.Restore(t.Context(), "owner", first.ID, 2, Author{Kind: "user", ID: "owner"})
	if err != nil || restored.Version != 102 {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	latest, err := s.VersionRecord(t.Context(), "owner", first.ID, 102)
	if err != nil || latest.AuthorKind != "user" || latest.AuthorID != "owner" || latest.Message != "Restored version 2" {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}
