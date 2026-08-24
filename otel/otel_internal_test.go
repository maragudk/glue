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
		name    string
		uptime  time.Duration
		seconds int64
		bucket  int64
	}{
		{name: "a process with no uptime yet gets bucket zero, not -Inf", uptime: 0, seconds: 0, bucket: 0},
		{name: "a process younger than one second gets bucket zero, not -Inf", uptime: 999 * time.Millisecond, seconds: 0, bucket: 0},
		{name: "a negative uptime is clamped to zero, so the bucket is zero and not NaN", uptime: -time.Hour, seconds: 0, bucket: 0},
		{name: "a process up for one second", uptime: time.Second, seconds: 1, bucket: 0},
		{name: "a partial second is truncated, not rounded up", uptime: 1900 * time.Millisecond, seconds: 1, bucket: 0},

		// The bucket is a floor, so every boundary is a power of ten, and those are exactly where
		// floating point error in [math.Log10] would push a value into the wrong bucket.
		{name: "just below the first boundary", uptime: 9 * time.Second, seconds: 9, bucket: 0},
		{name: "on the first boundary", uptime: 10 * time.Second, seconds: 10, bucket: 1},
		{name: "just below the second boundary", uptime: 99 * time.Second, seconds: 99, bucket: 1},
		{name: "on the second boundary", uptime: 100 * time.Second, seconds: 100, bucket: 2},
		{name: "just below the third boundary", uptime: 999 * time.Second, seconds: 999, bucket: 2},
		{name: "on the third boundary", uptime: 1000 * time.Second, seconds: 1000, bucket: 3},
		{name: "on the fourth boundary", uptime: 10000 * time.Second, seconds: 10000, bucket: 4},
		{name: "on the fifth boundary", uptime: 100000 * time.Second, seconds: 100000, bucket: 5},
		{name: "on the sixth boundary", uptime: 1000000 * time.Second, seconds: 1000000, bucket: 6},

		{name: "a process up for three days", uptime: 72 * time.Hour, seconds: 259200, bucket: 5},
		{name: "the longest uptime a duration can hold", uptime: math.MaxInt64, seconds: 9223372036, bucket: 9},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attrs := uptimeAttributes(test.uptime)

			is.True(t, oteltest.HasAttribute(attrs, attribute.Int64("uptime_sec", test.seconds)),
				fmt.Sprint("uptime_sec should be ", test.seconds, " in ", attrs))
			is.True(t, oteltest.HasAttribute(attrs, attribute.Int64("uptime_sec_log_10", test.bucket)),
				fmt.Sprint("uptime_sec_log_10 should be ", test.bucket, " in ", attrs))
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
		// The bucket has to stay the bucket of the seconds actually emitted, or the two columns disagree
		is.True(t, oteltest.HasAttribute(attrs, attribute.Int64("uptime_sec_log_10", 3)))
	})
}
