package jobs

import (
	"context"
	"database/sql"
	"time"

	"maragu.dev/goqite"
	"maragu.dev/goqite/jobs"
)

type Runner = jobs.Runner

type Func = jobs.Func

type NewRunnerOpts struct {
	Extend       time.Duration
	Limit        int
	Log          logger
	PollInterval time.Duration
	Queue        *goqite.Queue
}

// NewRunner just calls [jobs.NewRunner].
func NewRunner(opts NewRunnerOpts) *Runner {
	return jobs.NewRunner(jobs.NewRunnerOpts{
		Extend:       opts.Extend,
		Limit:        opts.Limit,
		Log:          opts.Log,
		PollInterval: opts.PollInterval,
		Queue:        opts.Queue,
	})
}

type logger interface {
	Info(msg string, args ...any)
}

func Create(ctx context.Context, q *goqite.Queue, name string, m goqite.Message) error {
	_, err := jobs.Create(ctx, q, name, m)
	return err
}

func CreaCreateTx(ctx context.Context, tx *sql.Tx, q *goqite.Queue, name string, m goqite.Message) error {
	_, err := jobs.CreateTx(ctx, tx, q, name, m)
	return err
}
