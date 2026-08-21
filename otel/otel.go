// Package otel has helpers for OpenTelemetry instrumentation.
package otel

import (
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// processStart, as close to the start of the process as package initialization gets, which is before main runs.
var processStart = time.Now()

// MainSpanAttributes for the one span which covers a whole unit of work, such as an HTTP request or a
// background job. Marking exactly one span per unit of work makes it possible to filter every other span
// out in a tracing backend, so counts and error rates are computed over units of work instead of over
// spans. The idea is from "A Practitioner's Guide to Wide Events":
// https://jeremymorrell.dev/blog/a-practitioners-guide-to-wide-events/#:~:text=A%20convention%20to%20filter%20out%20everything%20else
//
// The attributes also describe the process which did the work, so a trace answers what state the process
// was in without leaving the tracing backend: uptime_sec is whole seconds since process start, and
// uptime_sec_log_10 is the base 10 logarithm of those seconds, which keeps a process up for seconds and
// one up for days readable on the same heatmap.
//
// The result is a new slice on each call, and can either be passed to a span at start time or set on a
// span which is already running, whichever fits. Applications have units of work of their own -- a
// scheduler tick, a CLI invocation, a consumer of some queue -- and should mark those spans with these
// too, otherwise that work is missing from every query which filters on main spans.
func MainSpanAttributes() []attribute.KeyValue {
	uptime := uptimeAttributes(time.Since(processStart))

	// Sized exactly, so a caller appending to the result reallocates instead of writing into spare capacity
	attrs := make([]attribute.KeyValue, 0, 1+len(uptime))
	attrs = append(attrs, attribute.Bool("main", true))
	return append(attrs, uptime...)
}

// uptimeAttributes for the given process uptime, as whole seconds and as the base 10 logarithm of those
// seconds. [math.Log10] is -Inf at zero and NaN below it, and neither belongs in telemetry, so an uptime
// shorter than a second gets a logarithm of zero. The seconds are clamped at zero as well, so the two stay
// consistent for an uptime which somehow reads as negative. A logarithm of zero therefore covers everything
// up to and including one second, rather than one second alone.
func uptimeAttributes(uptime time.Duration) []attribute.KeyValue {
	seconds := int64(uptime / time.Second)
	if seconds < 0 {
		seconds = 0
	}

	var logOfSeconds float64
	if seconds > 0 {
		logOfSeconds = math.Log10(float64(seconds))
	}

	return []attribute.KeyValue{
		attribute.Int64("uptime_sec", seconds),
		attribute.Float64("uptime_sec_log_10", logOfSeconds),
	}
}
