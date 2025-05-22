package jobs

import (
	"time"

	"maragu.dev/goqite"
	"maragu.dev/goqite/jobs"
)

type Runner = jobs.Runner

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
