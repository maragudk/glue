package otel

import (
	"fmt"
	"math"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"maragu.dev/is"

	"maragu.dev/glue/oteltest"
)

func TestUptimeAttributes(t *testing.T) {
	tests := []struct {
		name     string
		uptime   time.Duration
		seconds  int64
		logOfSec float64
	}{
		{name: "a process with no uptime yet gets a zero logarithm, not -Inf", uptime: 0, seconds: 0, logOfSec: 0},
		{name: "a process younger than one second gets a zero logarithm, not -Inf", uptime: 999 * time.Millisecond, seconds: 0, logOfSec: 0},
		{name: "a negative uptime is clamped to zero, so the logarithm is zero and not NaN", uptime: -time.Hour, seconds: 0, logOfSec: 0},
		{name: "a process up for one second", uptime: time.Second, seconds: 1, logOfSec: 0},
		{name: "a partial second is truncated, not rounded up", uptime: 1900 * time.Millisecond, seconds: 1, logOfSec: 0},
		{name: "a process up for ten seconds", uptime: 10 * time.Second, seconds: 10, logOfSec: 1},
		{name: "a process up for a hundred seconds", uptime: 100 * time.Second, seconds: 100, logOfSec: 2},
		{name: "a process up for three days", uptime: 72 * time.Hour, seconds: 259200, logOfSec: math.Log10(259200)},
		{name: "the longest uptime a duration can hold", uptime: math.MaxInt64, seconds: 9223372036, logOfSec: math.Log10(9223372036)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attrs := uptimeAttributes(test.uptime)

			is.True(t, oteltest.HasAttribute(attrs, attribute.Int64("uptime_sec", test.seconds)),
				fmt.Sprint("uptime_sec should be ", test.seconds, " in ", attrs))
			is.True(t, oteltest.HasAttribute(attrs, attribute.Float64("uptime_sec_log_10", test.logOfSec)),
				fmt.Sprint("uptime_sec_log_10 should be ", test.logOfSec, " in ", attrs))
		})
	}
}

// TestProcessStart replaces the package-level [processStart], so no test in this package may be parallel.
func TestProcessStart(t *testing.T) {
	t.Run("is what the uptime is measured from", func(t *testing.T) {
		original := processStart
		t.Cleanup(func() {
			processStart = original
		})

		processStart = time.Now().Add(-time.Hour)

		attrs := MainSpanAttributes()
		is.True(t, oteltest.HasAttribute(attrs, attribute.Int64("uptime_sec", 3600)))
		// The logarithm has to stay the logarithm of the seconds actually emitted, or the two columns disagree
		is.True(t, oteltest.HasAttribute(attrs, attribute.Float64("uptime_sec_log_10", math.Log10(3600))))
	})
}
