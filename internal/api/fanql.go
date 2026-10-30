package api

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/fanql"
	"github.com/labstack/fanout/internal/query"
)

type FanQLHandler struct {
	parser    query.Parser
	engine    QueryEngine
	completer *fanql.Completer
}

func RegisterFanQLRoutes(e *echo.Echo, parser query.Parser, engine QueryEngine) {
	h := &FanQLHandler{parser: parser, engine: engine, completer: fanql.NewCompleter(engine)}
	read := RequireCapability(ReadTelemetry)
	e.POST("/api/fanql/parse", h.parse, read)
	e.POST("/api/fanql/format", h.format, read)
	e.POST("/api/fanql/complete", h.complete, read)
	e.GET("/api/fanql/reference", h.reference, read)
}

type fanqlPreview struct {
	Text string `json:"text"`
	Note string `json:"note"`
}

// FanQL adds editor-only metadata at its own error boundary.
type fanqlFailure struct {
	*apiError
	Preview *fanqlPreview `json:"preview,omitempty"`
}

func (e *fanqlFailure) ResponseBody() any { return e }
func fanqlNotices(notices []fanql.Notice) []fanql.Notice {
	if notices == nil {
		return []fanql.Notice{}
	}
	return notices
}

func (h *FanQLHandler) parse(c *echo.Context) error {
	var req struct {
		fanql.Options
		Text    string `json:"text"`
		Preview bool   `json:"preview,omitempty"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	compiled, problems := fanql.Compile(req.Text, req.Options)
	if len(problems) != 0 {
		failure := &fanqlFailure{apiError: newAPIError(http.StatusBadRequest, "invalid_fanql", "The query has problems.")}
		failure.Problems = query.PresentProblems(problems)
		if req.Preview {
			failure.Preview = &fanqlPreview{Text: req.Text, Note: "not executable"}
		}
		return failure
	}
	return c.JSON(http.StatusOK, struct {
		Query     query.Spec `json:"query"`
		Canonical string     `json:"canonical"`
		Viz       struct {
			Requested  string            `json:"requested,omitempty"`
			Compatible []query.VizCompat `json:"compatible"`
			Edits      []fanql.VizEdit   `json:"edits"`
		} `json:"viz"`
		Notices []fanql.Notice `json:"notices"`
	}{
		Query: compiled.Query, Canonical: compiled.Canonical,
		Viz: struct {
			Requested  string            `json:"requested,omitempty"`
			Compatible []query.VizCompat `json:"compatible"`
			Edits      []fanql.VizEdit   `json:"edits"`
		}{Requested: req.Viz, Compatible: compiled.Compatible, Edits: compiled.Edits},
		Notices: fanqlNotices(compiled.Notices),
	})
}

func (h *FanQLHandler) format(c *echo.Context) error {
	var req struct {
		fanql.Options
		Query query.Spec `json:"query"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	formatted, problems, err := fanql.Format(ctx, h.parser, req.Query, req.Options)
	if err != nil {
		return fanqlError(err)
	}
	if len(problems) != 0 {
		failure := newAPIError(http.StatusBadRequest, "invalid_spec", "The query spec is invalid.")
		failure.Problems = query.PresentProblems(problems)
		return failure
	}
	formatted.Notices = fanqlNotices(formatted.Notices)
	return c.JSON(http.StatusOK, formatted)
}

func fanqlError(err error) error {
	return newAPIError(http.StatusServiceUnavailable, "unavailable", query.PresentError(err, query.ReadError))
}

func (h *FanQLHandler) complete(c *echo.Context) error {
	var req struct {
		fanql.Options
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
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	out := h.completer.Complete(ctx, fanql.CompleteRequest{
		Text: req.Text, Cursor: req.Cursor, Options: req.Options,
		Window: window, From: req.From, To: req.To, Namespace: req.Namespace,
	})
	return c.JSON(http.StatusOK, out)
}

func (h *FanQLHandler) reference(c *echo.Context) error {
	return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", []byte(fanql.Reference()))
}
