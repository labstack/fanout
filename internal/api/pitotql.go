package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/pitot/internal/pitotql"
	"github.com/labstack/pitot/internal/query"
)

type PitotQLHandler struct {
	parser    query.Parser
	engine    QueryEngine
	completer *pitotql.Completer
}

func RegisterPitotQLRoutes(e *echo.Echo, parser query.Parser, engine QueryEngine) {
	h := &PitotQLHandler{parser: parser, engine: engine, completer: pitotql.NewCompleter(engine)}
	read := RequireCapability(ReadTelemetry)
	e.POST("/api/pitotql/parse", h.parse, read)
	e.POST("/api/pitotql/format", h.format, read)
	e.POST("/api/pitotql/complete", h.complete, read)
	e.GET("/api/pitotql/reference", h.reference, read)
}

type pitotqlPreview struct {
	Text string `json:"text"`
	Note string `json:"note"`
}

// PitotQL adds editor-only metadata at its own error boundary.
type pitotqlFailure struct {
	*apiError
	Preview *pitotqlPreview `json:"preview,omitempty"`
}

func (e *pitotqlFailure) ResponseBody() any { return e }
func pitotqlNotices(notices []pitotql.Notice) []pitotql.Notice {
	if notices == nil {
		return []pitotql.Notice{}
	}
	return notices
}

func (h *PitotQLHandler) parse(c *echo.Context) error {
	var req struct {
		pitotql.Options
		Text      string `json:"text"`
		Preview   bool   `json:"preview,omitempty"`
		Window    string `json:"window,omitempty"`
		Namespace string `json:"namespace,omitempty"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	req.Schema, req.SchemaPartial = h.cachedSchema(c.Request().Context(), req.Window, req.Namespace)
	compiled, problems := pitotql.Compile(req.Text, req.Options)
	if len(problems) != 0 {
		failure := &pitotqlFailure{apiError: newAPIError(http.StatusBadRequest, "invalid_pitotql", "The query has problems.")}
		failure.Problems = query.Diagnoses(problems)
		if req.Preview {
			failure.Preview = &pitotqlPreview{Text: req.Text, Note: "not executable"}
		}
		return failure
	}
	return c.JSON(http.StatusOK, struct {
		Query     query.Spec `json:"query"`
		Canonical string     `json:"canonical"`
		Viz       struct {
			Requested  string            `json:"requested,omitempty"`
			Compatible []query.VizCompat `json:"compatible"`
			Edits      []pitotql.VizEdit `json:"edits"`
		} `json:"viz"`
		Notices []pitotql.Notice `json:"notices"`
	}{
		Query: compiled.Query, Canonical: compiled.Canonical,
		Viz: struct {
			Requested  string            `json:"requested,omitempty"`
			Compatible []query.VizCompat `json:"compatible"`
			Edits      []pitotql.VizEdit `json:"edits"`
		}{Requested: req.Viz, Compatible: compiled.Compatible, Edits: compiled.Edits},
		Notices: pitotqlNotices(compiled.Notices),
	})
}

// cachedSchema is a schema already cached, so parse can compare keys with
// those seen without waiting for a discovery. One for the query's window, or
// the widest, and its namespace covers the query; otherwise the recent
// schema is partial.
func (h *PitotQLHandler) cachedSchema(ctx context.Context, window, namespace string) (*query.Schema, bool) {
	if h.engine == nil {
		return nil, false
	}
	cached := func(window, namespace string) (*query.Schema, error) {
		return h.engine.Schema(ctx, query.SchemaRequest{Window: window, Namespace: namespace, Cached: true})
	}
	if window != "" {
		// An invalid or longer window is an error other than not cached.
		schema, err := cached(window, namespace)
		if err == nil && schema != nil {
			return schema, false
		}
		if errors.Is(err, query.ErrSchemaNotCached) {
			if schema, err := cached("24h", namespace); err == nil && schema != nil {
				return schema, false
			}
		}
	}
	for _, window := range []string{"1h", "24h"} {
		if schema, err := cached(window, ""); err == nil && schema != nil {
			return schema, true
		}
	}
	return nil, false
}

func (h *PitotQLHandler) format(c *echo.Context) error {
	var req struct {
		pitotql.Options
		Query query.Spec `json:"query"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	formatted, problems, err := pitotql.Format(ctx, h.parser, req.Query, req.Options)
	if err != nil {
		return pitotqlError(err)
	}
	if len(problems) != 0 {
		failure := newAPIError(http.StatusBadRequest, "invalid_spec", "The query spec is invalid.")
		failure.Problems = query.PresentProblems(problems)
		return failure
	}
	formatted.Notices = pitotqlNotices(formatted.Notices)
	return c.JSON(http.StatusOK, formatted)
}

func pitotqlError(err error) error {
	return newAPIError(http.StatusServiceUnavailable, "unavailable", query.PresentError(err, query.ReadError))
}

func (h *PitotQLHandler) complete(c *echo.Context) error {
	var req struct {
		pitotql.Options
		Text      string     `json:"text"`
		Cursor    int        `json:"cursor"`
		Window    *string    `json:"window,omitempty"`
		From      *time.Time `json:"from,omitempty"`
		To        *time.Time `json:"to,omitempty"`
		Namespace string     `json:"namespace,omitempty"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	if req.From != nil || req.To != nil {
		if req.From == nil || req.To == nil || !req.From.Before(*req.To) || req.Window != nil {
			return newAPIError(http.StatusBadRequest, "invalid_input", "use from and to with the start before the end, or a window duration")
		}
	}
	window := ""
	if req.Window != nil {
		window = *req.Window
	}
	creq := pitotql.CompleteRequest{
		Text: req.Text, Cursor: req.Cursor, Options: req.Options,
		Window: window, From: req.From, To: req.To, Namespace: req.Namespace,
	}
	if err := pitotql.CheckCompleteRequest(creq); err != nil {
		return newAPIError(http.StatusBadRequest, "invalid_input", err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	out := h.completer.Complete(ctx, creq)
	return c.JSON(http.StatusOK, out)
}

func (h *PitotQLHandler) reference(c *echo.Context) error {
	return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", []byte(pitotql.Reference()))
}
