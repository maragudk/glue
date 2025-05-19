package sqlitestore

import (
	"context"
	"database/sql"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
)

func New(ctx context.Context, db *sql.DB) (scs.Store, error) {
	return sqlite3store.New(db), nil
}
