package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/fanql"
	"github.com/labstack/fanout/internal/panel"
)

type FanQLHandler struct {
	parser    panel.Parser
	engine    PanelEngine
	completer *fanql.Completer
}

func RegisterFanQLRoutes(e *echo.Echo, parser panel.Parser, engine PanelEngine) {
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
	Hint    string        `json:"hint,omitempty"`
	Preview *fanqlPreview `json:"preview,omitempty"`
}

func (e *fanqlFailure) ResponseBody() any { return e }
func checkFanQLVersion(version fanql.VersionRequest) (bool, error) {
	notice, problem := version.Validate()
	if problem != nil {
		return false, &fanqlFailure{apiError: newAPIError(http.StatusBadRequest, problem.Code, problem.Message), Hint: problem.Hint}
	}
	return notice != nil, nil
}
func fanqlNotices(notices []fanql.Notice, missing bool) []fanql.Notice {
	if notices == nil {
		notices = []fanql.Notice{}
	}
	if missing {
		notice, _ := (fanql.VersionRequest{}).Validate()
		notices = append(notices, *notice)
	}
	return notices
}

func (h *FanQLHandler) parse(c *echo.Context) error {
	var req struct {
		fanql.VersionRequest
		fanql.Options
		Text    string `json:"text"`
		Preview bool   `json:"preview,omitempty"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	missing, err := checkFanQLVersion(req.VersionRequest)
	if err != nil {
		return err
	}
	compiled, problems := fanql.Compile(req.Text, req.Options)
	if len(problems) != 0 {
		failure := &fanqlFailure{apiError: newAPIError(http.StatusBadRequest, "invalid_fanql", "The query has problems.")}
		failure.Problems = panel.PresentProblems(problems)
		if req.Preview {
			failure.Preview = &fanqlPreview{Text: req.Text, Note: "not executable"}
		}
		return failure
	}
	return c.JSON(http.StatusOK, struct {
		Version   int         `json:"fanql_version"`
		Query     panel.Query `json:"query"`
		Canonical string      `json:"canonical"`
		Viz       struct {
			Requested  string            `json:"requested,omitempty"`
			Compatible []panel.VizCompat `json:"compatible"`
			Edits      []fanql.VizEdit   `json:"edits"`
		} `json:"viz"`
		Notices []fanql.Notice `json:"notices"`
	}{
		Version: fanql.Version, Query: compiled.Query, Canonical: compiled.Canonical,
		Viz: struct {
			Requested  string            `json:"requested,omitempty"`
			Compatible []panel.VizCompat `json:"compatible"`
			Edits      []fanql.VizEdit   `json:"edits"`
		}{Requested: req.Viz, Compatible: compiled.Compatible, Edits: compiled.Edits},
		Notices: fanqlNotices(compiled.Notices, missing),
	})
}

func (h *FanQLHandler) format(c *echo.Context) error {
	var req struct {
		fanql.VersionRequest
		fanql.Options
		Query panel.Query  `json:"query"`
		Panel *panel.Panel `json:"panel,omitempty"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	missing, err := checkFanQLVersion(req.VersionRequest)
	if err != nil {
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
		failure.Problems = panel.PresentProblems(problems)
		return failure
	}
	formatted.Notices = fanqlNotices(formatted.Notices, missing)
	return c.JSON(http.StatusOK, struct {
		Version int `json:"fanql_version"`
		*fanql.Formatted
	}{Version: fanql.Version, Formatted: formatted})
}

func fanqlError(err error) error {
	return newAPIError(http.StatusServiceUnavailable, "unavailable", panel.PresentError(err, panel.ReadError))
}

func (h *FanQLHandler) complete(c *echo.Context) error {
	var req struct {
		fanql.VersionRequest
		fanql.Options
		Text      string  `json:"text"`
		Cursor    int     `json:"cursor"`
		Window    *string `json:"window,omitempty"`
		Namespace string  `json:"namespace,omitempty"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	missing, err := checkFanQLVersion(req.VersionRequest)
	if err != nil {
		return err
	}
	window := ""
	if req.Window != nil {
		window = *req.Window
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	out := h.completer.Complete(ctx, fanql.CompleteRequest{
		Text: req.Text, Cursor: req.Cursor, Options: req.Options,
		Window: window, Namespace: req.Namespace,
	})
	var notices []fanql.Notice
	if missing {
		notices = fanqlNotices(nil, true)
	}
	return c.JSON(http.StatusOK, struct {
		Version int `json:"fanql_version"`
		fanql.CompleteResponse
		Notices []fanql.Notice `json:"notices,omitempty"`
	}{Version: fanql.Version, CompleteResponse: out, Notices: notices})
}

func (h *FanQLHandler) reference(c *echo.Context) error {
	var version json.RawMessage
	if values, ok := c.Request().URL.Query()["fanql_version"]; ok {
		if len(values) != 1 {
			version = json.RawMessage(`null`)
		} else {
			version, _ = json.Marshal(values[0])
			if values[0] == strconv.Itoa(fanql.Version) {
				version = json.RawMessage(values[0])
			}
		}
	}
	missing, err := checkFanQLVersion(fanql.VersionRequest{FanQLVersion: version})
	if err != nil {
		return err
	}
	text := fanql.Reference()
	c.Response().Header().Set("FanQL-Version", strconv.Itoa(fanql.Version))
	if missing {
		notice, _ := (fanql.VersionRequest{}).Validate()
		c.Response().Header().Set("FanQL-Notice", notice.Message)
	}
	return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", []byte(text))
}
