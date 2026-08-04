package store

import (
	"errors"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsUniqueConstraint reports only SQLite UNIQUE and PRIMARY KEY violations.
func IsUniqueConstraint(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE ||
		sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}
