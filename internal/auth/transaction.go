package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
)

// rollbackConn rolls back a raw BEGIN IMMEDIATE transaction and prevents a
// failed rollback from returning a possibly transactional connection to the pool.
func rollbackConn(conn *sql.Conn, operation string) {
	ctx, cancel := DetachedWriteContext(context.Background())
	defer cancel()
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		slog.Error("security transaction rollback failed", "operation", operation, "err", err)
		if discardErr := conn.Raw(func(any) error { return driver.ErrBadConn }); discardErr != nil && !errors.Is(discardErr, driver.ErrBadConn) {
			slog.Error("security transaction connection discard failed", "operation", operation, "err", discardErr)
		}
	}
}
