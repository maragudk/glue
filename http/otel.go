package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mileusna/useragent"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	glueotel "maragu.dev/glue/otel"
)

func OpenTelemetry(next http.Handler) http.Handler {
	return otelhttp.NewHandler(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())

			// Set before the request is handled, so the span stays countable as a unit of work even
			// if the handler panics past everything below.
			span.SetAttributes(glueotel.MainSpanAttributes()...)

			// Semantic conventions make url.query conditionally required, so it is set only when the
			// request actually carried a query, never as an empty string.
			if r.URL.RawQuery != "" {
				span.SetAttributes(semconv.URLQuery(r.URL.RawQuery))
			}

			// Parse user agent and add structured attributes
			ua := useragent.Parse(r.UserAgent())

			// Add parsed user agent attributes using semconv helpers
			span.SetAttributes(
				semconv.UserAgentName(ua.Name),
				semconv.UserAgentVersion(ua.Version),
				semconv.UserAgentOSName(ua.OS),
				semconv.UserAgentOSVersion(ua.OSVersion),
			)

			// Add structured version information
			span.SetAttributes(
				attribute.Int("user_agent.version.major", ua.VersionNo.Major),
				attribute.Int("user_agent.version.minor", ua.VersionNo.Minor),
				attribute.Int("user_agent.version.patch", ua.VersionNo.Patch),
			)

			// Add structured OS version information
			span.SetAttributes(
				attribute.Int("user_agent.os.version.major", ua.OSVersionNo.Major),
				attribute.Int("user_agent.os.version.minor", ua.OSVersionNo.Minor),
				attribute.Int("user_agent.os.version.patch", ua.OSVersionNo.Patch),
			)

			// Add URL if present (typically for bots)
			if ua.URL != "" {
				span.SetAttributes(attribute.String("user_agent.url", ua.URL))
			}

			// Add browser mobile detection
			if ua.Mobile || ua.Tablet {
				span.SetAttributes(semconv.BrowserMobile(true))
			}

			// Add device type attributes (no semconv helper for device.type)
			if ua.Mobile {
				span.SetAttributes(attribute.String("device.type", "mobile"))
			} else if ua.Tablet {
				span.SetAttributes(attribute.String("device.type", "tablet"))
			} else if ua.Desktop {
				span.SetAttributes(attribute.String("device.type", "desktop"))
			} else if ua.Bot {
				span.SetAttributes(attribute.String("device.type", "bot"))
			}

			// Add bot detection
			if ua.Bot {
				span.SetAttributes(attribute.Bool("user_agent.bot", true))
			}

			// Add specific device if available using semconv helper
			if ua.Device != "" {
				span.SetAttributes(semconv.DeviceModelName(ua.Device))
			}

			// Everything which needs the request handled first runs in a defer, so a panicking handler
			// leaves a span which can still be attributed to a route instead of one which cannot.
			// Nothing recovers panics between here and the server, so this is the last chance to
			// describe the request on the span.
			defer func() {
				v := recover()

				// Record whether the client disconnected before we responded. The status code is handled
				// at the router (see [adaptPage]); here we just keep the signal queryable on the span.
				if contextCanceled(r.Context().Err()) {
					span.SetAttributes(attribute.Bool("http.client_disconnected", true))
				}

				// The route is only known now that the router below has matched, and the formatter below cannot
				// be relied on to pick it up: its second run happens only when [http.Request.Pattern] reaches
				// back up here, which any middleware in between replacing the request prevents. So the name is
				// set directly. http.route is Conditionally Required "if and only if it's available", so it
				// stays off entirely when nothing matched rather than going out as an empty string, which would
				// look like a real value when grouping. A panic mid-routing can leave the pattern partial,
				// which still says more about the request than nothing does.
				span.SetName(spanName(r))
				if routePattern := chi.RouteContext(r.Context()).RoutePattern(); routePattern != "" {
					span.SetAttributes(semconv.HTTPRoute(routePattern))
				}

				if v == nil {
					return
				}

				recordPanicOnSpan(span, v)

				// Re-panic so the server still handles this as the panic it is, logging a stack which
				// still reaches down to the handler, and closing the connection without a response.
				panic(v)
			}()

			next.ServeHTTP(w, r)
		}),
		// The operation name is unused, since [spanName] decides the name from the request. The formatter
		// is not optional though. [otelhttp.WithSpanNameFormatter] documents that it runs a second time
		// after the middleware once something has set [http.Request.Pattern], which routers do, and with
		// no formatter that run renames the span to the operation. A router either side of this middleware
		// would otherwise leave every span named the empty string.
		"",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return spanName(r)
		}),
	)
}

// spanName as semantic conventions want it: "{method} {route}" where a low-cardinality route is available
// and "{method}" alone where it is not, so the name carries no trailing space when nothing matched.
// [chi.Context.RoutePattern] returns "" both for a request no route matched and for a handler served
// outside a chi router, which are the same case here.
func spanName(r *http.Request) string {
	if routePattern := chi.RouteContext(r.Context()).RoutePattern(); routePattern != "" {
		return r.Method + " " + routePattern
	}

	return r.Method
}

func contextCanceled(errs ...error) bool {
	for _, err := range errs {
		if err == nil {
			continue
		}

		if errors.Is(err, context.Canceled) {
			return true
		}

		if strings.Contains(err.Error(), "context canceled") {
			return true
		}
	}

	return false
}

// setSpanDuration as an attribute in milliseconds on the current span in the context, if it is recording.
// This is the contract the three sibling helpers share: the attribute lands on whatever span the context
// carries. Under [OpenTelemetry] that is the main server span. Under other tracing middleware it is
// whichever span that middleware made current. Anything which starts a span in between takes the
// attribute with it, which is the accepted cost of writing where the context points instead of requiring
// a span this package placed itself. [trace.SpanFromContext] never returns nil, so a context with no span
// yields a noop span which is not recording, and this does nothing.
//
// Milliseconds as a float, because the segments being measured are routinely well under one millisecond
// and an integer would report most of them as zero. Each key names the segment it covers, because
// duration_ms is the whole of whichever span the attribute landed on, and a segment that reads like the
// total invites subtracting one from the other.
func setSpanDuration(ctx context.Context, key string, d time.Duration) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	span.SetAttributes(attribute.Float64(key, float64(d.Nanoseconds())/float64(time.Millisecond)))
}

// recordPanicOnSpan as an exception event with an error status, for a value recovered on the way out of
// a handler. A panic value which is already an error is recorded as it is, so exception.type keeps naming
// the type the handler panicked with, and only anything else is wrapped. The stack trace is the one being
// unwound, which is the only record of where the panic came from once the span is all that is left of
// the request.
//
// The SDK records an exception of its own when a panic unwinds through [trace.Span.End], so a panicking
// request can end up with two exception events. Recording here anyway, because that behaviour belongs to
// whichever tracer provider the application configured: it can be turned off, and it only fires where
// End is deferred directly. The status is never set by it, and neither is the stack trace unless the
// caller asked End for one, which the instrumentation around this middleware does not.
//
// [http.ErrAbortHandler] is left unrecorded, because it is how a handler says it is giving up on purpose
// and the server suppresses it as well, so counting it would put deliberate aborts in the error rate.
func recordPanicOnSpan(span trace.Span, v any) {
	err, ok := v.(error)
	if !ok {
		err = fmt.Errorf("panic: %v", v)
	}

	if errors.Is(err, http.ErrAbortHandler) {
		return
	}

	span.RecordError(err, trace.WithStackTrace(true))
	span.SetStatus(codes.Error, "panic")
}

// recordErrorOnSpan with the given description, on the current span in the context, if it is recording.
// See [setSpanDuration] for which span that is.
//
// This is for errors raised by this package's own middleware, which fail the request before any
// application code runs and so are invisible to it. Errors from an application's own handlers are its
// own to record.
//
// The description is wrapped around the error rather than left to carry the status alone, because the
// status description does not survive. Middleware which records an error goes on to respond 5xx, tracing
// middleware above sets the span status from the response code once the chain returns, and the SDK lets
// an equal status code overwrite, which replaces the description with the empty string. The exception
// event is not touched by anything above, so that is where the description has to be.
func recordErrorOnSpan(ctx context.Context, err error, description string) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	span.RecordError(fmt.Errorf("%v: %w", description, err))
	span.SetStatus(codes.Error, description)
}
