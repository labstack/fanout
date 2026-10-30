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

// RawMessage distinguishes an omitted version from every explicitly supplied
// value, including null and values of the wrong JSON type.
func checkFanQLVersion(version json.RawMessage) (bool, error) {
	if len(version) == 0 {
		return true, nil
	}
	var n int
	if err := json.Unmarshal(version, &n); err == nil && n == fanql.Version {
		return false, nil
	}
	failure := newAPIError(http.StatusBadRequest, "unsupported_fanql_version", "This server cannot read the requested FanQL version.")
	failure.Hint = "this server reads FanQL " + strconv.Itoa(fanql.Version)
	return false, failure
}

func fanqlNotices(notices []fanql.Notice, missing bool) []fanql.Notice {
	if notices == nil {
		notices = []fanql.Notice{}
	}
	if missing {
		notices = append(notices, fanql.Notice{Path: "version", Message: "version missing; assuming FanQL " + strconv.Itoa(fanql.Version)})
	}
	return notices
}

func (h *FanQLHandler) parse(c *echo.Context) error {
	var req struct {
		Version   json.RawMessage  `json:"version"`
		Text      string           `json:"text"`
		Source    string           `json:"source,omitempty"`
		Viz       string           `json:"viz,omitempty"`
		Variables []panel.Variable `json:"variables,omitempty"`
		Preview   bool             `json:"preview,omitempty"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	missing, err := checkFanQLVersion(req.Version)
	if err != nil {
		return err
	}
	compiled, problems := fanql.Compile(req.Text, fanql.Options{Source: req.Source, Viz: req.Viz, Variables: req.Variables})
	if len(problems) != 0 {
		failure := newAPIError(http.StatusBadRequest, "invalid_fanql", "The query has problems.")
		failure.Problems = panel.PresentProblems(problems)
		if req.Preview {
			failure.Preview = &fanqlPreview{Text: req.Text, Note: "not executable"}
		}
		return failure
	}
	return c.JSON(http.StatusOK, struct {
		Version   int         `json:"version"`
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
		Version   json.RawMessage  `json:"version"`
		Query     panel.Query      `json:"query"`
		Viz       string           `json:"viz,omitempty"`
		Variables []panel.Variable `json:"variables,omitempty"`
		Panel     *panel.Panel     `json:"panel,omitempty"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	missing, err := checkFanQLVersion(req.Version)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	formatted, problems, err := fanql.Format(ctx, h.parser, req.Query, fanql.Options{Viz: req.Viz, Variables: req.Variables})
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
		Version int `json:"version"`
		*fanql.Formatted
	}{Version: fanql.Version, Formatted: formatted})
}

func fanqlError(err error) error {
	return newAPIError(http.StatusServiceUnavailable, "unavailable", panel.PresentError(err, panel.ReadError))
}

func (h *FanQLHandler) complete(c *echo.Context) error {
	var req struct {
		Version   json.RawMessage  `json:"version"`
		Text      string           `json:"text"`
		Cursor    int              `json:"cursor"`
		Source    string           `json:"source,omitempty"`
		Viz       string           `json:"viz,omitempty"`
		Window    *string          `json:"window,omitempty"`
		Namespace string           `json:"namespace,omitempty"`
		Variables []panel.Variable `json:"variables,omitempty"`
	}
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	missing, err := checkFanQLVersion(req.Version)
	if err != nil {
		return err
	}
	window := "1h"
	if req.Window != nil {
		window = *req.Window
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	out := h.completer.Complete(ctx, fanql.CompleteRequest{
		Text: req.Text, Cursor: req.Cursor, Source: req.Source, Viz: req.Viz,
		Window: window, Namespace: req.Namespace, Variables: req.Variables,
	})
	var notices []fanql.Notice
	if missing {
		notices = fanqlNotices(nil, true)
	}
	return c.JSON(http.StatusOK, struct {
		Version int `json:"version"`
		fanql.CompleteResponse
		Notices []fanql.Notice `json:"notices,omitempty"`
	}{Version: fanql.Version, CompleteResponse: out, Notices: notices})
}

func (h *FanQLHandler) reference(c *echo.Context) error {
	var version json.RawMessage
	if values, ok := c.Request().URL.Query()["version"]; ok {
		if len(values) != 1 {
			version = json.RawMessage(`null`)
		} else {
			version, _ = json.Marshal(values[0])
			if values[0] == strconv.Itoa(fanql.Version) {
				version = json.RawMessage(values[0])
			}
		}
	}
	missing, err := checkFanQLVersion(version)
	if err != nil {
		return err
	}
	text := fanql.Reference()
	if missing {
		text += "\n\nversion missing; assuming FanQL " + strconv.Itoa(fanql.Version) + "\n"
	}
	return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", []byte(text))
}
