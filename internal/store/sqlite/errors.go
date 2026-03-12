package sqlite

import (
	"errors"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func IsUniqueConstraint(err error) bool {
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code == sqlite3.ErrConstraint &&
		sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique
}
