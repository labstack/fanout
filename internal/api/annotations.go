package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/annotations"
	"github.com/labstack/fanout/internal/query"
)

type AnnotationReader interface {
	Read(context.Context, annotations.Request) (annotations.Response, error)
}

func RegisterAnnotationRoutes(e *echo.Echo, service AnnotationReader) {
	e.POST("/api/annotations", func(c *echo.Context) error {
		var req annotations.Request
		if err := decodeStrict(c, &req, 32<<10); err != nil {
			return err
		}
		out, err := service.Read(c.Request().Context(), req)
		if err != nil {
			return annotationError(err)
		}
		return c.JSON(http.StatusOK, out)
	}, RequireCapability(ReadTelemetry))
}

func annotationError(err error) error {
	switch {
	case errors.Is(err, annotations.ErrRequest):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error()).Wrap(err)
	case errors.Is(err, context.DeadlineExceeded):
		return echo.NewHTTPError(http.StatusGatewayTimeout, "Annotations took longer than 10 seconds").Wrap(err)
	case errors.Is(err, query.ErrParquetReadWait):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "annotations unavailable").Wrap(err)
	case errors.Is(err, context.Canceled):
		return nil
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, "annotations unavailable").Wrap(err)
	}
}
