package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mileusna/useragent"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"

	glueotel "maragu.dev/glue/otel"
)

const contextRootSpanKey = ContextKey("rootSpan")

func OpenTelemetry(next http.Handler) http.Handler {
	return otelhttp.NewHandler(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())
			ctx := context.WithValue(r.Context(), contextRootSpanKey, span)
			r = r.WithContext(ctx)

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

			next.ServeHTTP(w, r)

			// Record whether the client disconnected before we responded. The status code is handled
			// at the router (see [adaptPage]); here we just keep the signal queryable on the span.
			if contextCanceled(r.Context().Err()) {
				span.SetAttributes(attribute.Bool("http.client_disconnected", true))
			}

			routePattern := chi.RouteContext(r.Context()).RoutePattern()
			span.SetName(r.Method + " " + routePattern)
			span.SetAttributes(semconv.HTTPRoute(routePattern))
		}),
		"", // Setting the name here doesn't matter, it's done on the span above
	)
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
