package http

import (
	"context"
	"net/http"

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

			next.ServeHTTP(w, r)

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

// GetRootSpanFromContext stored by the OpenTelemetry middleware.
func GetRootSpanFromContext(ctx context.Context) trace.Span {
	span := ctx.Value(contextRootSpanKey)
	if span == nil {
		return nil
	}
	return span.(trace.Span)
}
