package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/engine"
	"github.com/labstack/fanout/internal/query"
)

type QueryEngine interface {
	Exemplars(context.Context, query.ExemplarRequest) (query.ExemplarResponse, error)
	Run(context.Context, query.RunRequest) ([]query.Result, error)
	Stream(context.Context, query.RunRequest, func(query.Result)) error
	ResolveVariables(context.Context, query.ResolveRequest) (map[string][]query.Option, error)
	Schema(context.Context, query.SchemaRequest) (*query.Schema, error)
}

type QueryHandler struct{ engine QueryEngine }

// panelDeadline bounds one batch. Each panel has its own 10-second timeout;
// the batch allows a little more for queueing behind the read pool.
const panelDeadline = 20 * time.Second

func RegisterQueryRoutes(e *echo.Echo, engine QueryEngine) {
	h := &QueryHandler{engine: engine}
	read := RequireCapability(ReadTelemetry)
	e.POST("/api/queries/run", h.query, read)
	e.POST("/api/queries/exemplars", h.exemplars, read)
	e.POST("/api/queries/variables/resolve", h.resolve, read)
	e.GET("/api/telemetry/schema", h.schema, read)
}

// streamType asks /api/queries/run for one JSON result per line, written as
// each panel finishes, instead of one object once the slowest panel is done.
const streamType = "application/x-ndjson"

// streamWriteTimeout bounds writing one streamed result to the client.
const streamWriteTimeout = 10 * time.Second

func (h *QueryHandler) query(c *echo.Context) error {
	var req query.RunRequest
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	if !strings.Contains(c.Request().Header.Get(echo.HeaderAccept), streamType) {
		results, err := h.engine.Run(c.Request().Context(), req)
		if err != nil {
			return panelError(c, err, "panels unavailable")
		}
		return c.JSON(http.StatusOK, map[string]any{"results": results})
	}
	response := c.Response()
	control := http.NewResponseController(response)
	// A client that stops reading must not hold the stream open: each write
	// has its own deadline, and a failed write cancels the panels still running.
	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel()
	defer func() { _ = control.SetWriteDeadline(time.Time{}) }()
	encoder := json.NewEncoder(response)
	started, failed := false, false
	begin := func() {
		started = true
		response.Header().Set(echo.HeaderContentType, streamType)
		// Not no-store: Chrome cancels a fully read no-store fetch body whose
		// end arrives after its last chunk, so the stream shows as aborted.
		// A POST response is not cached; no-transform keeps proxies from
		// buffering or compressing the stream.
		response.Header().Set(echo.HeaderCacheControl, "no-cache, no-transform")
		response.WriteHeader(http.StatusOK)
	}
	err := h.engine.Stream(ctx, req, func(result query.Result) {
		if failed {
			return
		}
		_ = control.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
		if !started {
			begin()
		}
		if encoder.Encode(result) != nil || control.Flush() != nil {
			failed = true
			cancel()
		}
	})
	switch {
	case err != nil && started:
		// Only the client's departure ends a stream early; nobody is reading.
		return nil
	case err != nil:
		// Problems and failures before the first panel still get a status.
		return panelError(c, err, "panels unavailable")
	case !started:
		begin()
	}
	return nil
}

func (h *QueryHandler) resolve(c *echo.Context) error {
	var req query.ResolveRequest
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	options, err := h.engine.ResolveVariables(ctx, req)
	if err != nil {
		return panelError(c, err, "variables unavailable")
	}
	return c.JSON(http.StatusOK, map[string]any{"options": options})
}

// schemaWait bounds how long a request waits for a schema discovery. The
// discovery itself runs on, so a caller told to retry finds it cached.
const schemaWait = 15 * time.Second

func (h *QueryHandler) schema(c *echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), schemaWait)
	defer cancel()
	schema, err := h.engine.Schema(ctx, query.SchemaRequest{Window: c.QueryParam("window"), Namespace: c.QueryParam("namespace")})
	if errors.Is(err, query.ErrSchemaBusy) {
		c.Response().Header().Set("Retry-After", "5")
		return newAPIError(http.StatusServiceUnavailable, "schema_busy", "The schema is still being read. Try again in a few seconds.")
	}
	var problems query.Problems
	switch {
	case errors.As(err, &problems):
		failure := newAPIError(http.StatusBadRequest, "invalid_request", problems[0].Message)
		failure.Problems = query.PresentProblems(problems)
		return failure
	case errors.Is(err, context.DeadlineExceeded):
		return echo.NewHTTPError(http.StatusGatewayTimeout, "Reading the schema took too long. Try a shorter window.").Wrap(err)
	case err != nil:
		return panelError(c, err, "schema unavailable")
	}
	return c.JSON(http.StatusOK, schema)
}

// panelError maps an engine failure to a response: spec problems are 400, a
// blown deadline is 504, a departed client gets no body, and anything else is
// a 500.
func panelError(c *echo.Context, err error, unavailable string) error {
	if handled, writeErr := writeProblems(c, err); handled {
		return writeErr
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return echo.NewHTTPError(http.StatusGatewayTimeout, "Panels took longer than 20 seconds. Narrow the time range or reduce the number of panels.").Wrap(err)
	case errors.Is(err, context.Canceled):
		// The client is gone; there is nobody to answer.
		return nil
	}
	return echo.NewHTTPError(http.StatusInternalServerError, unavailable).Wrap(err)
}

// decodeStrict reads one JSON object, rejecting unknown fields and bodies
// over limit, so a misspelled field fails loudly instead of being ignored.
func decodeStrict(c *echo.Context, value any, limit int64) error {
	reader := &io.LimitedReader{R: c.Request().Body, N: limit + 1}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body: "+err.Error())
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body: trailing data after JSON value")
	}
	if reader.N <= 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body: body exceeds limit")
	}
	return nil
}

// writeProblems answers a spec validation failure with 400 and the problem
// list. It reports whether it handled err.
func writeProblems(c *echo.Context, err error) (bool, error) {
	var problems query.Problems
	if !errors.As(err, &problems) {
		return false, nil
	}
	failure := newAPIError(http.StatusBadRequest, "invalid_spec", "The dashboard spec is invalid.")
	failure.Problems = query.PresentProblems(problems)
	return true, failure
}

func (h *QueryHandler) exemplars(c *echo.Context) error {
	var req query.ExemplarRequest
	if err := decodeStrict(c, &req, 512<<10); err != nil {
		return err
	}
	out, err := h.engine.Exemplars(c.Request().Context(), req)
	if err != nil {
		return exemplarHTTPError(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

func exemplarHTTPError(c *echo.Context, err error) error {
	if handled, writeErr := writeProblems(c, err); handled {
		return writeErr
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return echo.NewHTTPError(http.StatusGatewayTimeout, "Exemplars took longer than 10 seconds").Wrap(err)
	case errors.Is(err, context.Canceled):
		return nil
	case errors.Is(err, engine.ErrParquetReadWait):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "exemplars unavailable").Wrap(err)
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, "exemplars unavailable").Wrap(err)
	}
}
