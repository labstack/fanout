package engine

import (
	"database/sql"
	"github.com/labstack/fanout/internal/engine/querytest"
)

func CreateTables(db *sql.DB) error { return querytest.CreateTables(db, CreateCacheTables) }
