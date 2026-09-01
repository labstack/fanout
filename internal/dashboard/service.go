package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	appid "github.com/labstack/fanout/internal/id"
)

var (
	ErrNotFound = errors.New("dashboard not found")
	ErrConflict = errors.New("dashboard name already exists")
)

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(message string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(message, args...)}
}

type Filters struct {
	Window    string `json:"window"`
	Namespace string `json:"namespace"`
}

type Layout struct {
	I    string `json:"i"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	MinW int    `json:"minW,omitempty"`
	MinH int    `json:"minH,omitempty"`
}

type Widget struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Title   string         `json:"title"`
	Config  map[string]any `json:"config,omitempty"`
	Enabled bool           `json:"enabled"`
}

type State struct {
	Layout  []Layout `json:"layout"`
	Widgets []Widget `json:"widgets"`
	Filters Filters  `json:"filters"`
}

type Dashboard struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
	State       State  `json:"state"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type Summary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
	WidgetCount int    `json:"widget_count"`
	UpdatedAt   string `json:"updated_at"`
}

type CreateInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	State       State  `json:"state"`
}

type UpdateInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	State       State  `json:"state"`
}

type Service struct {
	db        *sql.DB
	maxWindow time.Duration
	now       func() time.Time
}

func New(db *sql.DB, retentionDays int) *Service {
	maxWindow := 30 * 24 * time.Hour
	if retentionDays > 0 {
		maxWindow = time.Duration(retentionDays) * 24 * time.Hour
	}
	return &Service{db: db, maxWindow: maxWindow, now: time.Now}
}

func DefaultState() State {
	return State{
		Widgets: []Widget{
			{ID: "health", Type: "overview", Title: "System health", Enabled: true},
			{ID: "topology", Type: "topology", Title: "Service map", Enabled: true},
			{ID: "activity", Type: "activity", Title: "Recent activity", Enabled: true},
			{ID: "assistant", Type: "assistant", Title: "Ask Fanout", Enabled: true},
		},
		Layout: []Layout{
			{I: "health", X: 0, Y: 0, W: 4, H: 3, MinW: 3, MinH: 2},
			{I: "topology", X: 4, Y: 0, W: 8, H: 6, MinW: 4, MinH: 4},
			{I: "activity", X: 0, Y: 3, W: 4, H: 3, MinW: 3, MinH: 2},
			{I: "assistant", X: 0, Y: 6, W: 12, H: 3, MinW: 4, MinH: 2},
		},
		Filters: Filters{Window: "1h"},
	}
}

func (s *Service) List(ctx context.Context, ownerID string) ([]Summary, error) {
	if err := s.ensureInitial(ctx, ownerID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.name,d.description,d.is_default,COUNT(w.id),d.updated_at FROM dashboards d LEFT JOIN dashboard_widgets w ON w.dashboard_id=d.id WHERE d.owner_id=? GROUP BY d.id ORDER BY d.is_default DESC,d.updated_at DESC,d.name`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var item Summary
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.IsDefault, &item.WidgetCount, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, ownerID, id string) (Dashboard, error) {
	if err := s.ensureInitial(ctx, ownerID); err != nil {
		return Dashboard{}, err
	}
	return s.get(ctx, s.db, ownerID, id)
}

func (s *Service) Default(ctx context.Context, ownerID string) (Dashboard, error) {
	if err := s.ensureInitial(ctx, ownerID); err != nil {
		return Dashboard{}, err
	}
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM dashboards WHERE owner_id=? ORDER BY is_default DESC,created_at LIMIT 1`, ownerID).Scan(&id); err != nil {
		return Dashboard{}, err
	}
	return s.get(ctx, s.db, ownerID, id)
}

func (s *Service) Create(ctx context.Context, ownerID string, input CreateInput) (Dashboard, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.State.Filters.Window == "" {
		input.State.Filters.Window = "1h"
	}
	state, err := normalizeStateIDs(input.State)
	if err != nil {
		return Dashboard{}, fmt.Errorf("generate dashboard widget ids: %w", err)
	}
	input.State = state
	if err := Validate(input.Name, input.Description, input.State); err != nil {
		return Dashboard{}, err
	}
	input.State.Filters.Window = clampDashboardWindow(input.State.Filters.Window, s.maxWindow)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Dashboard{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dashboards WHERE owner_id=?`, ownerID).Scan(&count); err != nil {
		return Dashboard{}, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	id, err := appid.New()
	if err != nil {
		return Dashboard{}, fmt.Errorf("generate dashboard id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO dashboards(id,owner_id,name,description,window,namespace,is_default,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, ownerID, input.Name, strings.TrimSpace(input.Description), input.State.Filters.Window, strings.TrimSpace(input.State.Filters.Namespace), count == 0, now, now); err != nil {
		if isUnique(err) {
			return Dashboard{}, ErrConflict
		}
		return Dashboard{}, err
	}
	if err := replaceWidgets(ctx, tx, id, input.State); err != nil {
		return Dashboard{}, err
	}
	if err := tx.Commit(); err != nil {
		return Dashboard{}, err
	}
	return s.get(ctx, s.db, ownerID, id)
}

func (s *Service) Update(ctx context.Context, ownerID, id string, input UpdateInput) (Dashboard, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.State.Filters.Window == "" {
		input.State.Filters.Window = "1h"
	}
	state, err := normalizeStateIDs(input.State)
	if err != nil {
		return Dashboard{}, fmt.Errorf("generate dashboard widget ids: %w", err)
	}
	input.State = state
	if err := Validate(input.Name, input.Description, input.State); err != nil {
		return Dashboard{}, err
	}
	input.State.Filters.Window = clampDashboardWindow(input.State.Filters.Window, s.maxWindow)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Dashboard{}, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE dashboards SET name=?,description=?,window=?,namespace=?,updated_at=? WHERE id=? AND owner_id=?`, input.Name, strings.TrimSpace(input.Description), input.State.Filters.Window, strings.TrimSpace(input.State.Filters.Namespace), now, id, ownerID)
	if err != nil {
		if isUnique(err) {
			return Dashboard{}, ErrConflict
		}
		return Dashboard{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Dashboard{}, err
	}
	if affected == 0 {
		return Dashboard{}, ErrNotFound
	}
	if err := replaceWidgets(ctx, tx, id, input.State); err != nil {
		return Dashboard{}, err
	}
	if err := tx.Commit(); err != nil {
		return Dashboard{}, err
	}
	return s.get(ctx, s.db, ownerID, id)
}

func (s *Service) Delete(ctx context.Context, ownerID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var isDefault bool
	if err := tx.QueryRowContext(ctx, `SELECT is_default FROM dashboards WHERE id=? AND owner_id=?`, id, ownerID).Scan(&isDefault); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM dashboards WHERE id=? AND owner_id=?`, id, ownerID); err != nil {
		return err
	}
	if isDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE dashboards SET is_default=1 WHERE id=(SELECT id FROM dashboards WHERE owner_id=? ORDER BY updated_at DESC LIMIT 1)`, ownerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) get(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, ownerID, id string) (Dashboard, error) {
	var out Dashboard
	if err := q.QueryRowContext(ctx, `SELECT id,name,description,is_default,window,namespace,created_at,updated_at FROM dashboards WHERE id=? AND owner_id=?`, id, ownerID).Scan(&out.ID, &out.Name, &out.Description, &out.IsDefault, &out.State.Filters.Window, &out.State.Filters.Namespace, &out.CreatedAt, &out.UpdatedAt); errors.Is(err, sql.ErrNoRows) {
		return Dashboard{}, ErrNotFound
	} else if err != nil {
		return Dashboard{}, err
	}
	out.State.Filters.Window = clampDashboardWindow(out.State.Filters.Window, s.maxWindow)
	rows, err := q.QueryContext(ctx, `SELECT id,type,title,config_json,enabled,x,y,w,h,min_w,min_h FROM dashboard_widgets WHERE dashboard_id=? ORDER BY sort_order,id`, id)
	if err != nil {
		return Dashboard{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var widget Widget
		var layout Layout
		var raw string
		if err := rows.Scan(&widget.ID, &widget.Type, &widget.Title, &raw, &widget.Enabled, &layout.X, &layout.Y, &layout.W, &layout.H, &layout.MinW, &layout.MinH); err != nil {
			return Dashboard{}, err
		}
		layout.I = widget.ID
		if err := json.Unmarshal([]byte(raw), &widget.Config); err != nil {
			return Dashboard{}, fmt.Errorf("decode widget %s config: %w", widget.ID, err)
		}
		out.State.Widgets = append(out.State.Widgets, widget)
		out.State.Layout = append(out.State.Layout, layout)
	}
	return out, rows.Err()
}

func replaceWidgets(ctx context.Context, tx *sql.Tx, dashboardID string, state State) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM dashboard_widgets WHERE dashboard_id=?`, dashboardID); err != nil {
		return err
	}
	layouts := make(map[string]Layout, len(state.Layout))
	for _, item := range state.Layout {
		layouts[item.I] = item
	}
	for index, widget := range state.Widgets {
		layout := layouts[widget.ID]
		raw, err := json.Marshal(widget.Config)
		if err != nil {
			return err
		}
		if string(raw) == "null" {
			raw = []byte("{}")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dashboard_widgets(id,dashboard_id,type,title,config_json,enabled,x,y,w,h,min_w,min_h,sort_order) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, widget.ID, dashboardID, widget.Type, widget.Title, raw, widget.Enabled, layout.X, layout.Y, layout.W, layout.H, layout.MinW, layout.MinH, index); err != nil {
			return err
		}
	}
	return nil
}

// ensureInitial lazily provisions an owner's first dashboard. The legacy
// single-canvas dashboard_state row (see the 20260720200000_dashboard_state
// migration) is retained for exactly this one-way lazy migration: on the
// owner's first dashboard read, valid legacy state seeds the initial
// "System overview" dashboard; otherwise the default layout is used. The
// migration is one-shot — once any dashboard exists for the owner, the
// legacy row is never consulted again — so a transient read failure must be
// surfaced rather than treated as "no legacy state".
func (s *Service) ensureInitial(ctx context.Context, ownerID string) error {
	if strings.TrimSpace(ownerID) == "" {
		return errors.New("dashboard owner is required")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dashboards WHERE owner_id=?`, ownerID).Scan(&count); err != nil || count > 0 {
		return err
	}
	state := DefaultState()
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT state_json FROM dashboard_state WHERE owner_id=?`, ownerID).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// No legacy canvas; seed from the default layout.
	case err != nil:
		// A real read failure (locked database, etc.) is not "no legacy
		// state". Proceeding would create the default dashboard and the
		// count check above would skip migration forever, silently
		// orphaning the user's saved layout.
		return fmt.Errorf("read legacy dashboard state: %w", err)
	default:
		if jsonErr := json.Unmarshal([]byte(raw), &state); jsonErr != nil {
			slog.Warn("discarding legacy dashboard state; falling back to default layout", "owner_id", ownerID, "reason", "corrupt state_json", "error", jsonErr)
			state = DefaultState()
		} else if normalized, normalizeErr := normalizeStateIDs(state); normalizeErr != nil {
			slog.Warn("discarding legacy dashboard state; falling back to default layout", "owner_id", ownerID, "reason", "widget id normalization failed", "error", normalizeErr)
			state = DefaultState()
		} else if validateErr := Validate("System overview", "", normalized); validateErr != nil {
			slog.Warn("discarding legacy dashboard state; falling back to default layout", "owner_id", ownerID, "reason", "validation failed", "error", validateErr)
			state = DefaultState()
		} else {
			state = normalized
		}
	}
	_, err = s.Create(ctx, ownerID, CreateInput{Name: "System overview", Description: "Live health, dependencies, and recent activity.", State: state})
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// WidgetTypes is the single source of truth for the dashboard widget-type
// allowlist. The agent system prompt and the frontend widget registry mirror
// this list; derive any validation or documentation from it rather than
// duplicating the literals.
var WidgetTypes = []string{"overview", "topology", "activity", "assistant", "performance", "trace", "logs"}

var dashboardWindows = []struct {
	value    string
	duration time.Duration
}{
	{"15m", 15 * time.Minute},
	{"1h", time.Hour},
	{"6h", 6 * time.Hour},
	{"24h", 24 * time.Hour},
	{"168h", 7 * 24 * time.Hour},
	{"720h", 30 * 24 * time.Hour},
}

func Validate(name, description string, state State) error {
	if len(strings.TrimSpace(name)) == 0 || len([]rune(strings.TrimSpace(name))) > 80 {
		return invalid("dashboard name must be between 1 and 80 characters")
	}
	if len([]rune(strings.TrimSpace(description))) > 280 {
		return invalid("dashboard description is limited to 280 characters")
	}
	if len(state.Widgets) == 0 || len(state.Widgets) > 32 || len(state.Layout) != len(state.Widgets) {
		return invalid("dashboard must contain between 1 and 32 positioned widgets")
	}
	allowed := make(map[string]bool, len(WidgetTypes))
	for _, widgetType := range WidgetTypes {
		allowed[widgetType] = true
	}
	ids := make(map[string]bool, len(state.Widgets))
	for _, widget := range state.Widgets {
		if !appid.IsV7(widget.ID) || ids[widget.ID] {
			return invalid("widget ids must be unique UUIDv7 values")
		}
		if !allowed[widget.Type] {
			return invalid("unsupported widget type %q", widget.Type)
		}
		if len(strings.TrimSpace(widget.Title)) == 0 || len([]rune(widget.Title)) > 80 {
			return invalid("widget titles must be between 1 and 80 characters")
		}
		ids[widget.ID] = true
	}
	seen := make(map[string]bool, len(state.Layout))
	for _, item := range state.Layout {
		if !ids[item.I] || seen[item.I] || item.W < 1 || item.H < 1 || item.X < 0 || item.Y < 0 || item.X+item.W > 12 {
			return invalid("invalid widget layout")
		}
		seen[item.I] = true
	}
	for i := 0; i < len(state.Layout); i++ {
		for j := i + 1; j < len(state.Layout); j++ {
			a, b := state.Layout[i], state.Layout[j]
			if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				return invalid("widgets must not overlap")
			}
		}
	}
	if _, ok := dashboardWindowDuration(state.Filters.Window); !ok {
		return invalid("unsupported dashboard window")
	}
	return nil
}

func dashboardWindowDuration(value string) (time.Duration, bool) {
	for _, option := range dashboardWindows {
		if option.value == value {
			return option.duration, true
		}
	}
	return 0, false
}

func clampDashboardWindow(value string, maxWindow time.Duration) string {
	requested, ok := dashboardWindowDuration(value)
	if !ok || requested <= maxWindow {
		return value
	}
	for index := len(dashboardWindows) - 1; index >= 0; index-- {
		if dashboardWindows[index].duration <= maxWindow {
			return dashboardWindows[index].value
		}
	}
	return dashboardWindows[0].value
}

// normalizeStateIDs makes the service, rather than callers or the model, the
// authority for widget identifiers. Existing UUIDv7 values remain stable;
// omitted, descriptive, and legacy values are replaced and layout references
// are updated atomically before validation and persistence.
func normalizeStateIDs(state State) (State, error) {
	state.Widgets = append([]Widget(nil), state.Widgets...)
	state.Layout = append([]Layout(nil), state.Layout...)
	replacements := make(map[string]string, len(state.Widgets))
	for index := range state.Widgets {
		oldID := state.Widgets[index].ID
		newID, exists := replacements[oldID]
		if !exists {
			newID = oldID
			if !appid.IsV7(newID) {
				var err error
				newID, err = appid.New()
				if err != nil {
					return State{}, err
				}
			}
			replacements[oldID] = newID
		}
		state.Widgets[index].ID = newID
	}
	for index := range state.Layout {
		if newID, ok := replacements[state.Layout[index].I]; ok {
			state.Layout[index].I = newID
		}
	}
	return state, nil
}

// isUnique detects SQLite unique-constraint violations by substring match on
// the driver error message; this is driver-version-sensitive but pragmatic.
func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
