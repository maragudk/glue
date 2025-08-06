package http

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
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
