package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/db/generated"
	appid "github.com/labstack/fanout/internal/id"
	"github.com/labstack/fanout/internal/panel"
)

var (
	ErrNotFound = errors.New("dashboard not found")
	ErrConflict = errors.New("a dashboard with that name already exists")
	ErrStale    = errors.New("the dashboard changed since it was read")
)

// Validator checks a spec completely, including its filters and SQL.
// *panel.Executor implements it.
type Validator interface {
	Validate(ctx context.Context, d *panel.Dashboard) error
}

type Author struct {
	Kind string // user, agent or system
	ID   string
}

type Record struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	Version     int             `json:"version"`
	Spec        panel.Dashboard `json:"spec"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// Mutation identifies exactly one committed save and its successful optimistic base.
type Mutation struct {
	Record      Record
	Before      panel.Dashboard
	BaseVersion int
}

type Summary struct {
	Origin      *BuildOrigin `json:"origin,omitempty"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	IsDefault   bool         `json:"is_default"`
	Version     int          `json:"version"`
	PanelCount  int          `json:"panel_count"`
	UpdatedAt   string       `json:"updated_at"`
}

type VersionInfo struct {
	Version    int    `json:"version"`
	AuthorKind string `json:"author_kind"`
	AuthorID   string `json:"author_id,omitempty"`
	Message    string `json:"message,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type Service struct {
	db        *sql.DB
	validator Validator
	now       func() time.Time
}

const keepVersions = 100

func New(db *sql.DB, validator Validator) *Service {
	return &Service{db: db, validator: validator, now: time.Now}
}

func (s *Service) List(ctx context.Context, ownerID string) ([]Summary, error) {
	if err := s.ensureInitial(ctx, ownerID); err != nil {
		return nil, err
	}
	rows, err := generated.New(s.db).ListDashboards(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(rows))
	for _, r := range rows {
		item := Summary{ID: r.ID, Name: r.Name, Description: r.Description, IsDefault: r.IsDefault == 1, Version: int(r.Version), PanelCount: int(r.PanelCount), UpdatedAt: r.UpdatedAt}
		if r.OriginThreadID.Valid {
			item.Origin = &BuildOrigin{ThreadID: r.OriginThreadID.String, MessageID: r.OriginMessageID.String, RequestExcerpt: r.OriginRequestExcerpt.String}
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, ownerID, id string) (Record, error) {
	if err := s.ensureInitial(ctx, ownerID); err != nil {
		return Record{}, err
	}
	return s.get(ctx, ownerID, id)
}

func (s *Service) get(ctx context.Context, ownerID, id string) (Record, error) {
	row, err := generated.New(s.db).GetDashboard(ctx, generated.GetDashboardParams{ID: id, OwnerID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	return decodeRecord(row)
}

func decodeRecord(row generated.Dashboard) (Record, error) {
	record := Record{ID: row.ID, Name: row.Name, Description: row.Description, IsDefault: row.IsDefault == 1, Version: int(row.Version), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if err := json.Unmarshal([]byte(row.SpecJson), &record.Spec); err != nil {
		return Record{}, fmt.Errorf("decode dashboard %s: %w", row.ID, err)
	}
	return record, nil
}

// prepare normalizes, validates and packs a spec for storage.
func (s *Service) prepare(ctx context.Context, spec *panel.Dashboard, repack bool) error {
	panel.Normalize(spec)
	validationCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.validator.Validate(validationCtx, spec); err != nil {
		return err
	}
	if repack {
		for i := range spec.Panels {
			spec.Panels[i].Grid = nil
		}
	}
	if repack || needsPack(spec.Panels) {
		PackMissing(spec.Panels)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, ownerID string, spec panel.Dashboard, author Author) (Record, error) {
	mutation, err := s.CreateWithChanges(ctx, ownerID, spec, author)
	return mutation.Record, err
}

func (s *Service) CreateWithChanges(ctx context.Context, ownerID string, spec panel.Dashboard, author Author) (Mutation, error) {
	var err error
	spec, err = clone(spec)
	if err != nil {
		return Mutation{}, err
	}
	if err := s.prepare(ctx, &spec, false); err != nil {
		return Mutation{}, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return Mutation{}, err
	}
	id, err := appid.New()
	if err != nil {
		return Mutation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Mutation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := generated.New(tx)
	now := s.now().UTC().Format(time.RFC3339Nano)
	affected, err := q.InsertDashboardBelowOwnerLimit(ctx, generated.InsertDashboardBelowOwnerLimitParams{ID: id, OwnerID: ownerID, Name: spec.Name, Description: spec.Description, Version: 1, SpecJson: string(raw), PanelCount: int64(len(spec.Panels)), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		if isUnique(err) {
			return Mutation{}, ErrConflict
		}
		return Mutation{}, err
	}
	if affected == 0 {
		return Mutation{}, panel.Problems{{Path: "dashboards", Message: "at most 500 dashboards per owner; delete a dashboard before creating another"}}
	}
	if origin, ok := BuildOriginFromContext(ctx); ok {
		if strings.TrimSpace(origin.ThreadID) == "" || strings.TrimSpace(origin.MessageID) == "" {
			return Mutation{}, errors.New("invalid dashboard build origin")
		}
		rows, err := q.InsertDashboardOrigin(ctx, generated.InsertDashboardOriginParams{DashboardID: id, ThreadID: origin.ThreadID, MessageID: origin.MessageID, RequestExcerpt: origin.RequestExcerpt, OwnerID: ownerID})
		if err != nil {
			return Mutation{}, err
		}
		if rows != 1 {
			return Mutation{}, errors.New("dashboard build origin thread is not owned by the dashboard owner")
		}
	}
	if err := q.InsertDashboardVersion(ctx, generated.InsertDashboardVersionParams{DashboardID: id, Version: 1, SpecJson: string(raw), AuthorKind: author.Kind, AuthorID: author.ID, Message: "Created", CreatedAt: now}); err != nil {
		return Mutation{}, err
	}
	row, err := q.GetDashboard(ctx, generated.GetDashboardParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return Mutation{}, err
	}
	record, err := decodeRecord(row)
	if err != nil {
		return Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Mutation{}, err
	}
	return Mutation{Record: record}, nil
}

// Replace stores a whole new spec. baseVersion 0 means "the latest".
func (s *Service) Replace(ctx context.Context, ownerID, id string, spec panel.Dashboard, baseVersion int, author Author, message string) (Record, error) {
	mutation, err := s.ReplaceWithChanges(ctx, ownerID, id, spec, baseVersion, author, message)
	return mutation.Record, err
}

func (s *Service) ReplaceWithChanges(ctx context.Context, ownerID, id string, spec panel.Dashboard, baseVersion int, author Author, message string) (Mutation, error) {
	return s.update(ctx, ownerID, id, baseVersion, author, message, func(current panel.Dashboard) (panel.Dashboard, bool, error) {
		replacement, err := clone(spec)
		if err != nil {
			return panel.Dashboard{}, false, err
		}
		spec := replacement
		// Carry coordinates by id, including browser-resized dimensions.
		grids := map[string]*panel.Grid{}
		prior := map[string]panel.Panel{}
		for _, p := range current.Panels {
			grids[p.ID] = p.Grid
			prior[p.ID] = p
		}
		for i := range spec.Panels {
			p := &spec.Panels[i]
			if p.Grid == nil {
				p.Grid = grids[p.ID]
			}
			old, exists := prior[p.ID]
			// A deliberate authored size change gets a new placement. Explicit
			// grids accompanying a browser layout save remain authoritative.
			if exists && p.Width != 0 && p.Width != old.Width && p.Grid != nil && old.Grid != nil && *p.Grid == *old.Grid {
				p.Grid = nil
			}
			if exists && p.Height != "" && p.Height != old.Height && p.Grid != nil && old.Grid != nil && *p.Grid == *old.Grid {
				p.Grid = nil
			}
		}
		if len(spec.Panels) < len(current.Panels) {
			panel.Normalize(&spec)
			Compact(spec.Panels)
		}
		return spec, false, nil
	})
}

// Edit applies typed operations atomically. baseVersion 0 means "the latest".
func (s *Service) Edit(ctx context.Context, ownerID, id string, ops []Operation, baseVersion int, author Author, message string) (Record, error) {
	mutation, err := s.EditWithChanges(ctx, ownerID, id, ops, baseVersion, author, message)
	return mutation.Record, err
}

func (s *Service) EditWithChanges(ctx context.Context, ownerID, id string, ops []Operation, baseVersion int, author Author, message string) (Mutation, error) {
	return s.update(ctx, ownerID, id, baseVersion, author, message, func(current panel.Dashboard) (panel.Dashboard, bool, error) {
		return Apply(current, ops)
	})
}

func (s *Service) Restore(ctx context.Context, ownerID, id string, version int, author Author) (Record, error) {
	mutation, err := s.RestoreWithChanges(ctx, ownerID, id, version, author)
	return mutation.Record, err
}

// RestoreWithChanges appends a historical spec as a new save against the latest
// optimistic base, retaining the committed snapshots for the shared diff.
func (s *Service) RestoreWithChanges(ctx context.Context, ownerID, id string, version int, author Author) (Mutation, error) {
	if _, err := s.get(ctx, ownerID, id); err != nil {
		return Mutation{}, err
	}
	raw, err := generated.New(s.db).GetDashboardVersion(ctx, generated.GetDashboardVersionParams{DashboardID: id, Version: int64(version)})
	if errors.Is(err, sql.ErrNoRows) {
		return Mutation{}, ErrNotFound
	}
	if err != nil {
		return Mutation{}, err
	}
	var spec panel.Dashboard
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return Mutation{}, err
	}
	return s.update(ctx, ownerID, id, 0, author, fmt.Sprintf("Restored version %d", version), func(panel.Dashboard) (panel.Dashboard, bool, error) {
		return spec, false, nil
	})
}

// update reads, changes, validates and writes with an optimistic version
// check. With baseVersion 0 a concurrent write is retried once on the newer
// version; with an explicit baseVersion it fails with ErrStale instead of
// overwriting what someone else saved.
func (s *Service) update(ctx context.Context, ownerID, id string, baseVersion int, author Author, message string, change func(panel.Dashboard) (panel.Dashboard, bool, error)) (Mutation, error) {
	for attempt := 0; attempt < 2; attempt++ {
		current, err := s.get(ctx, ownerID, id)
		if err != nil {
			return Mutation{}, err
		}
		if baseVersion != 0 && current.Version != baseVersion {
			return Mutation{}, ErrStale
		}
		before, err := clone(current.Spec)
		if err != nil {
			return Mutation{}, err
		}
		next, repack, err := change(current.Spec)
		if err != nil {
			return Mutation{}, err
		}
		if err := s.prepare(ctx, &next, repack); err != nil {
			return Mutation{}, err
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return Mutation{}, err
		}
		record, written, err := s.write(ctx, ownerID, id, current.Version, next, string(raw), author, message)
		if err != nil {
			return Mutation{}, err
		}
		if written {
			return Mutation{Record: record, Before: before, BaseVersion: current.Version}, nil
		}
		if baseVersion != 0 {
			return Mutation{}, ErrStale
		}
	}
	return Mutation{}, ErrStale
}

func (s *Service) write(ctx context.Context, ownerID, id string, base int, spec panel.Dashboard, raw string, author Author, message string) (Record, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	q := generated.New(tx)
	now := s.now().UTC().Format(time.RFC3339Nano)
	next := int64(base + 1)
	affected, err := q.UpdateDashboard(ctx, generated.UpdateDashboardParams{Name: spec.Name, Description: spec.Description, NextVersion: next, SpecJson: raw, PanelCount: int64(len(spec.Panels)), UpdatedAt: now, ID: id, OwnerID: ownerID, BaseVersion: int64(base)})
	if err != nil {
		if isUnique(err) {
			return Record{}, false, ErrConflict
		}
		return Record{}, false, err
	}
	if affected == 0 {
		return Record{}, false, nil
	}
	if err := q.InsertDashboardVersion(ctx, generated.InsertDashboardVersionParams{DashboardID: id, Version: next, SpecJson: raw, AuthorKind: author.Kind, AuthorID: author.ID, Message: strings.TrimSpace(message), CreatedAt: now}); err != nil {
		return Record{}, false, err
	}
	if err := q.PruneDashboardVersions(ctx, generated.PruneDashboardVersionsParams{DashboardID: id, KeepFrom: next - keepVersions + 1}); err != nil {
		return Record{}, false, err
	}
	row, err := q.GetDashboard(ctx, generated.GetDashboardParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return Record{}, false, err
	}
	record, err := decodeRecord(row)
	if err != nil {
		return Record{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

func (s *Service) Delete(ctx context.Context, ownerID, id string) error {
	current, err := s.get(ctx, ownerID, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := generated.New(tx)
	if _, err := q.DeleteDashboard(ctx, generated.DeleteDashboardParams{ID: id, OwnerID: ownerID}); err != nil {
		return err
	}
	if current.IsDefault {
		if err := q.PromoteNewestDashboard(ctx, ownerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) Versions(ctx context.Context, ownerID, id string) ([]VersionInfo, error) {
	if _, err := s.get(ctx, ownerID, id); err != nil {
		return nil, err
	}
	rows, err := generated.New(s.db).ListDashboardVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]VersionInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, VersionInfo{Version: int(r.Version), AuthorKind: r.AuthorKind, AuthorID: r.AuthorID, Message: r.Message, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// ensureInitial gives an owner with no dashboards the default one.
func (s *Service) ensureInitial(ctx context.Context, ownerID string) error {
	if strings.TrimSpace(ownerID) == "" {
		return errors.New("dashboard owner is required")
	}
	count, err := generated.New(s.db).CountDashboards(ctx, ownerID)
	if err != nil || count > 0 {
		return err
	}
	_, err = s.Create(WithBuildOrigin(ctx, BuildOrigin{}), ownerID, DefaultSpec(), Author{Kind: "system"})
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// isUnique detects SQLite unique-constraint violations by the driver message.
func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
