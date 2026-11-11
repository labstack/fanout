package observability

import "github.com/labstack/pitot/internal/queryrows"

func SQLDB(db queryrows.SQLQueryer) DB { return queryrows.SQLAdapter{DB: db} }
