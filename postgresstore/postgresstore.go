package postgresstore

import (
	"context"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"maragu.dev/errors"
)

func New(ctx context.Context, databaseURL string) (scs.Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, errors.Wrap(err, "error creating database pool")
	}
	return pgxstore.New(pool), nil
}
