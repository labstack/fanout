package query

import (
	"database/sql"
	"github.com/labstack/fanout/internal/query/querytest"
)

func CreateTables(db *sql.DB) error { return querytest.CreateTables(db, CreateCacheTables) }
