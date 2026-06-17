package http

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mileusna/useragent"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"
)

const contextRootSpanKey = ContextKey("rootSpan")

func OpenTelemetry(next http.Handler) http.Handler {
	return otelhttp.NewHandler(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())
			ctx := context.WithValue(r.Context(), contextRootSpanKey, span)
			r = r.WithContext(ctx)

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

			// Add URL query parameters as individual attributes for easier searching
			if len(r.URL.Query()) > 0 {
				attrs := make([]attribute.KeyValue, 0, len(r.URL.Query()))
				for key, values := range r.URL.Query() {
					attrs = append(attrs, attribute.StringSlice("url.query."+strings.ToLower(key), values))
				}
				span.SetAttributes(attrs...)
			}

			// Wrap the response writer so that a 5xx written after the client has disconnected is
			// recorded as 499 instead. A vanished client is not a server error, so we keep these
			// out of the 5xx error rate and out of error-based alerting.
			next.ServeHTTP(&clientDisconnectWriter{ResponseWriter: w, r: r}, r)

			if contextCanceled(r.Context().Err()) {
				span.SetAttributes(attribute.Bool("http.client_disconnected", true))
			}

			routePattern := chi.RouteContext(r.Context()).RoutePattern()
			span.SetName(r.Method + " " + routePattern)
			span.SetAttributes(semconv.HTTPRoute(routePattern))

			// The idea of a "main" span is from "A Practitioner's Guide to Wide Events":
			// https://jeremymorrell.dev/blog/a-practitioners-guide-to-wide-events/#:~:text=A%20convention%20to%20filter%20out%20everything%20else
			span.SetAttributes(attribute.Bool("main", true))
		}),
		"", // Setting the name here doesn't matter, it's done on the span above
	)
}

// statusClientClosedRequest is the non-standard 499 status code popularized by nginx ("Client Closed
// Request"). It is not in the IANA registry, but it is a widely recognized convention for "the client
// disconnected before we responded".
const statusClientClosedRequest = 499

// clientDisconnectWriter rewrites a 5xx status to [statusClientClosedRequest] when the request context
// has been canceled, i.e. the client disconnected before we responded. The handler still runs to
// completion (Go cannot abort it), but the response is no longer recorded as a server error.
type clientDisconnectWriter struct {
	http.ResponseWriter
	r *http.Request
}

func (w *clientDisconnectWriter) WriteHeader(statusCode int) {
	if statusCode >= 500 && contextCanceled(w.r.Context().Err()) {
		statusCode = statusClientClosedRequest
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

// Unwrap returns the wrapped [http.ResponseWriter] so that [http.ResponseController] can reach optional
// interfaces such as [http.Flusher] and [http.Hijacker].
func (w *clientDisconnectWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Flush delegates to the wrapped writer so streaming responses (e.g. server-sent events) keep working.
func (w *clientDisconnectWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack delegates to the wrapped writer so connection upgrades (e.g. WebSockets) keep working.
func (w *clientDisconnectWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
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

// GetRootSpanFromContext stored by the OpenTelemetry middleware.
func GetRootSpanFromContext(ctx context.Context) trace.Span {
	span := ctx.Value(contextRootSpanKey)
	if span == nil {
		return nil
	}
	return span.(trace.Span)
}
